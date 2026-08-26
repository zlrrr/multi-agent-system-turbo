# 详细设计（LLD）：HTTP API 的认证与授权

> **特性 ID**：`009-api-authentication` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

```
internal/config/
  config.go     + ServerConfig.Auth、ServerConfig.TLS
  validate.go   + 加载时的 scope 与 token 校验
internal/httpapi/
  auth.go       新增：Authorizer、路由表、中间件
  admission.go  新增：监听地址规则，在监听器打开之前运行
  server.go     mux 被包裹；处理函数从 context 读取主体
internal/core/
  model.go      + DiagnoseRequest.Principal、RunRecord.Principal
pkg/errs/
  registry.go   MAS-7010…MAS-7014
```

## 2. 配置

```yaml
server:
  addr: "0.0.0.0:8080"
  auth:
    tokens:
      - name: dashboard          # 主体名称，也是审计行中出现的名字
        token: "${MAS_DASHBOARD_TOKEN}"
        scopes: [read]
      - name: oncall
        token: "file:/etc/mas/oncall.token"
        scopes: [read, diagnose]
  tls:
    cert_file: /etc/mas/tls.crt
    key_file: /etc/mas/tls.key
    terminated_by_proxy: false
```

```go
type ServerConfig struct {
    Addr         string      `yaml:"addr"`
    ReadTimeout  Duration    `yaml:"read_timeout"`
    WriteTimeout Duration    `yaml:"write_timeout"`
    Auth         ServerAuth  `yaml:"auth"`
    TLS          ServerTLS   `yaml:"tls"`
}

type ServerAuth struct {
    Tokens []APIToken `yaml:"tokens"`
}

type APIToken struct {
    Name   string   `yaml:"name"`
    Token  Secret   `yaml:"token"`
    Scopes []string `yaml:"scopes"`
}

type ServerTLS struct {
    CertFile          string `yaml:"cert_file"`
    KeyFile           string `yaml:"key_file"`
    TerminatedByProxy bool   `yaml:"terminated_by_proxy"`
}
```

`Token` 是 `Secret`，因此它本就无法被打印进日志、`mas config` 与 JSON，
并且本就支持 `${ENV}` 与 `file:` 引用（FR-004）。

加载时的校验（FR-013）：

- token 没有 `name`，或两个 token 同名 → `MAS-1003`；
- token 没有任何 scope → `MAS-7013`：在一份其他条目都能做事的列表里，
  一个什么都做不了的凭据，几乎总是一个错误；
- 出现本版本不认识的 scope → `MAS-7013`。
  一个被忽略的 scope，是一次运维人员以为自己已经授出的授权；
- 有 `cert_file` 却没有 `key_file`，或反之 → `MAS-1003`。

## 3. 准入

```go
// Admit 返回"为什么这份配置不得打开监听器"，若无问题则返回 nil。
func Admit(cfg config.ServerConfig) error
```

由 `Serve` 在监听器打开之前调用，且可独立测试：

```go
switch {
case isLoopback(cfg.Addr):
    return nil                      // 主机本身就是边界
case len(cfg.Auth.Tokens) == 0:
    return errs.New("MAS-7010", cfg.Addr)
case !cfg.TLS.Enabled() && !cfg.TLS.TerminatedByProxy:
    return errs.New("MAS-7011", cfg.Addr)
}
return nil
```

`isLoopback` 解析地址的主机部分：空主机、`0.0.0.0` 与 `::` **不是**回环，
`127.0.0.0/8`、`::1` 与 `localhost` 是。无法解析的主机名按非回环处理 ——
这是安全的方向，也是能产出一条清晰错误、而不是一次静默暴露的方向。

## 4. 授权器

```go
type Scope string

const (
    ScopeRead     Scope = "read"
    ScopeDiagnose Scope = "diagnose"
)

// Principal 是请求来自谁。
type Principal struct {
    Name   string
    Scopes map[Scope]bool
}

type Authorizer struct {
    tokens map[string]Principal // 摘要 → 主体
    routes map[string]Scope     // 路由模式 → 所需 scope
    on     bool                 // 未配置任何 token 时为 false
}
```

**按摘要查找。** token 以其密钥的 `sha256` 存储，
呈递上来的凭据也以同样方式做摘要，因此 `subtle.ConstantTimeCompare`
比较的是两个定长数组，长度永远不产生分支（RSK-4）。
map 查找本身相对摘要并非常数时间，但这不泄露关于 token 的任何信息：
摘要不可逆，而已经拿到摘要的攻击者，本来就已经拿到了 token。

**路由表是穷举的，且默认拒绝。**

| 路由 | scope |
|---|---|
| `POST /api/v1/diagnoses` | `diagnose` |
| `GET /api/v1/diagnoses`、`/api/v1/diagnoses/…` | `read` |
| `GET /api/v1/targets`、`/topologies`、`/packs` | `read` |
| `GET /metrics` | `read` |
| `/healthz`、`/readyz` | *按设计即匿名* |
| 其余任何 | **拒绝** |

没有条目的路由是被拒绝而不是被放行，
因此"新增了处理函数却没配 scope"会以关闭状态失败（RSK-1）。
`TestEveryRouteIsGuarded` 会遍历已注册的路由模式，
断言每一个要么在表中，要么在匿名集合中。

**中间件**包裹整个 mux：

```go
func (a *Authorizer) Wrap(next http.Handler) http.Handler
```

1. 匿名路由 → 直接放行；
2. 授权未开启（无 token、回环）→ 放行，并带上 `Principal{Name: "anonymous"}`；
3. 没有或格式错误的 `Authorization: Bearer …` → `401`，`MAS-7012`；
4. 未知 token → `401`，`MAS-7012`。刻意与 (3) 使用同一个错误码与响应体：
   区分"没有 token"与"token 错误"，等于告诉攻击者该在哪一半上下功夫；
5. 已知 token 但缺少该 scope → `403`，`MAS-7014`；
6. 其余 → `next`，并把主体放入请求 context。

每一次判定都会以 info 级别记录主体名称、路由与结果 ——
绝不记录凭据本身，也绝不记录 `Authorization` 头（FR-012）。
拒绝**与**放行都记：一份只显示拒绝的日志回答不了"这是谁跑的"，
而事后真正被问到的正是这个问题。

## 5. 运行上的主体

`core.DiagnoseRequest` 新增 `Principal string`，
由处理函数从 context 设置，**绝不**取自请求体 ——
客户端自报的主体，是任何人都能伪造的归属。
`core.RunRecord` 新增同名字段，由 service 从请求复制过来。

CLI 会让它保持为空，在展示记录的地方渲染为 "local"：
一次来自 CLI 的运行，是由"能执行这个二进制的人"发起的，
而为它编造一个名字，等于凭空造出一个系统并不掌握的事实。

## 6. TLS

当 `cert_file` 与 `key_file` 都已设置时，`Serve` 使用 `ListenAndServeTLS`，
否则使用 `ListenAndServe` —— 而准入阶段已经确认过：
此时要么是回环，要么处于一个已声明的代理之后。`MinVersion: tls.VersionTLS12`。

## 7. `mas doctor`

新增一节：监听地址、它是否为回环、配置了多少个 token 及它们各自的 scope、
TLS 是自行提供还是已声明由代理终止。不含 token、不含摘要、不含长度。

## 8. 错误码

| 错误码 | 含义 |
|---|---|
| `MAS-7010` | API 将在 %s 上监听，但未配置任何认证 |
| `MAS-7011` | API 将在 %s 上以明文传输凭据 |
| `MAS-7012` | 请求未携带可用的凭据 |
| `MAS-7013` | 某个 API token 未声明任何 scope，或声明了本版本不认识的 scope |
| `MAS-7014` | 该凭据缺少此路由所需的 scope |

## 9. 测试

| 测试 | 它钉住了什么 |
|---|---|
| `TestAnonymousRequestIsRefused` | FR-001 |
| `TestScopeIsEnforcedPerRoute` | FR-002 |
| `TestTokenComparisonIsConstantTime` | FR-003，以结构方式 |
| `TestCredentialsAreNeverEchoed` | FR-004、CON-004 |
| `TestHealthEndpointsStayAnonymous` | FR-005 |
| `TestEveryRouteIsGuarded` | FR-006、CON-001、CON-002 |
| `TestUnauthenticatedPublicBindIsRefused` | FR-007 |
| `TestPlaintextCredentialsOffHostAreRefused` | FR-008、CON-003 |
| `TestLoopbackNeedsNoConfiguration` | FR-009、NFR-004 |
| `TestServesTLSDirectly` | FR-010 |
| `TestRunRecordCarriesThePrincipal` | FR-011 |
| `TestAuthDecisionsAreAudited` | FR-012 |
| `TestScopelessOrUnknownScopeIsRejectedAtLoad` | FR-013 |
| `TestDoctorReportsAPIExposure` | FR-014 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版详细设计 | tasks、代码 |
