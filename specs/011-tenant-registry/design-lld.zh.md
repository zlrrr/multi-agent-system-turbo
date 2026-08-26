# 详细设计（LLD）：给目标与凭据加上租户维度

> **特性 ID**：`011-tenant-registry` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

```
internal/config/
  config.go     + TargetConfig.Tenant、APIToken.Tenants
  validate.go   + 加载时的租户规则
  tenancy.go    新增：Tenants()、MultiTenant()
internal/httpapi/
  auth.go       + Principal.Tenants，以及那个唯一的可达性函数
  server.go     三条会指名或返回目标的路径
internal/core/
  model.go      + DiagnoseRequest.Tenant、RunRecord.Tenant
internal/service/
  service.go    租户随请求写入运行记录
  doctor.go     + 租户检查
pkg/errs/
  registry.go   MAS-1013、MAS-7015
```

## 2. 配置

```yaml
targets:
  - id: payments-redis
    kind: redis
    tenant: payments
  - id: search-kafka
    kind: kafka
    tenant: search

server:
  auth:
    tokens:
      - name: payments-oncall
        token: "${env:PAYMENTS_TOKEN}"
        scopes: [read, diagnose]
        tenants: [payments]
      - name: platform
        token: "${env:PLATFORM_TOKEN}"
        scopes: [read]
        tenants: [payments, search]
```

```go
type TargetConfig struct {
    // …
    Tenant string `yaml:"tenant" json:"tenant,omitempty"`
}

type APIToken struct {
    // …
    Tenants []string `yaml:"tenants" json:"tenants,omitempty"`
}
```

## 3. 租户何时开启

```go
// MultiTenant 报告是否有任何目标写了 tenant。
func (c *Config) MultiTenant() bool

// Tenants 列出已声明的租户，有序。
func (c *Config) Tenants() []string
```

这里没有开关。只要有任何一个目标写下了 tenant，这份配置就是多租户的，
随之而来的加载时规则如下（HLD §2）：

| 条件 | 结果 |
|---|---|
| `MultiTenant()` 且某个目标没有 tenant | `MAS-1013`，并指出是哪个目标 |
| `MultiTenant()` 且某个凭据没有 tenants | `MAS-1013`，并指出是哪个凭据 |
| 某个凭据写了一个没有任何目标声明过的租户 | `MAS-1013`，并同时指出两者 |
| 非 `MultiTenant()` 但某个凭据写了 tenants | `MAS-1013`：这些租户会被静默忽略，而那是一次运维人员以为自己已经收窄了的授权 |

最后一行与其他几行同样重要。
一个因为"没有任何目标带租户"而不起作用的 `tenants:` 列表，
正是"看起来已生效、实际并未生效"的安全控制的典型形态。

## 4. 主体与那个唯一的函数

```go
type Principal struct {
    Name    string
    Scopes  map[Scope]bool
    Tenants map[string]bool   // 为空 ⇒ 不受限，仅在租户关闭时合法
}

// MayReach 报告该主体是否可以作用于某个目标。
//
// 这是租户判定的唯一场所。会自行比较租户的处理函数，
// 就是那种"被复制走时比较逻辑没跟着走"的处理函数 ——
// 而本项目已在其他形态下修过四次同类缺陷。
func (s *Server) MayReach(p Principal, targetID string) bool
```

`MayReach` 从配置中解析该目标的租户，并在主体持有它时返回 true。
租户关闭时，任何主体都能触达一切 —— 这与今天的行为完全一致。

`TestTenancyIsEnforcedInOnePlace` 会解析 `internal/httpapi`，
断言没有任何处理函数直接读取 `Principal.Tenants` 或目标的 `Tenant`：
一切都要经过 `MayReach`。

## 5. 三条路径

| 路径 | 行为 |
|---|---|
| `POST /api/v1/diagnoses` | 不可达 ⇒ `404` 与 `MAS-7404`，与"从未配置过的 id"完全一致 |
| `GET /api/v1/targets` | 过滤为该主体可触达的部分 |
| `GET /api/v1/diagnoses` | 按运行记录中记录的租户过滤 |
| `GET /api/v1/diagnoses/{id}` | 另一个租户的运行 ⇒ `404`，与未知 id 相同 |

404 正是要点（HLD §3）。一个指名了目标的 `403` 会确认它存在 ——
那是邻居的信息而不是调用者的，并且每猜一个 id 就泄露一次。
`TestCrossTenantRefusalRevealsNothing` 会逐字节比较这两种响应。

`MAS-7015` 的存在是为了审计日志 —— 在那里这个区别既真实又安全：
"denied: tenant" 正是一位在排查"列表被过滤"的运维人员需要的信息，
而它绝不会出现在网络响应上。

## 6. 运行上的租户

`core.DiagnoseRequest` 新增 `Tenant string`，由处理函数从配置中设置 ——
绝不取自请求体，理由与 `Principal` 相同。
`core.RunRecord` 同样新增该字段，在准入阶段写入。

是记录而不是推导：在查询时去读目标的租户，回答的是
*那个目标现在归哪个租户*；而配置会变，审计问的却是过去。

## 7. `mas doctor`

一行：租户是否开启、声明了多少个租户，以及每个凭据按名字给出的触达范围 ——
`payments-oncall → payments`。绝不包含 token。

租户关闭时它会明说，因为"没有配置任何租户"正是
一位在排查"列表没有被过滤"的运维人员需要看到的信息。

## 8. 错误码

| 错误码 | 含义 |
|---|---|
| `MAS-1013` | 租户配置不自洽。`Config.Validate` 会做聚合，因此它以 `MAS-1003` 包裹、消息原样保留的形式抵达运维人员 —— 与其他每一个带码的配置问题一致 |
| `MAS-7015` | 该凭据无权代表此目标所属的租户（仅用于审计日志） |

## 9. 测试

| 测试 | 它钉住了什么 |
|---|---|
| `TestTargetsCarryATenant` | FR-001 |
| `TestTenancyOffChangesNothing` | FR-002、NFR-003 |
| `TestPartialTenancyIsRefused` | FR-003 |
| `TestCredentialWithoutTenantsIsRefused` | FR-004 |
| `TestDiagnosingAnotherTenantIsRefused` | FR-005 |
| `TestTargetListingIsTenantScoped` | FR-006 |
| `TestRunAccessIsTenantScoped` | FR-007 |
| `TestRunRecordCarriesTheTenant` | FR-008 |
| `TestCrossTenantRefusalRevealsNothing` | FR-009、CON-003 |
| `TestTenancyIsEnforcedInOnePlace` | FR-010、CON-002 |
| `TestDoctorReportsTenancy` | FR-011 |
| `TestCredentialNamingAnUnknownTenantIsRefused` | FR-012 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版详细设计 | tasks、代码 |
