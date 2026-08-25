# 详细设计（LLD）：在 Kubernetes Pod 内执行只读命令

> **特性 ID**：`004-kube-exec` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

| 路径 | 内容 |
|---|---|
| `internal/safety/guard.go` | `ExecEffect`；`Call.Exec`；`authorizeExec`；exec 路径规则 |
| `internal/envadapter/kube/wsconn.go` | RFC 6455 客户端：握手、帧读取、关闭 |
| `internal/envadapter/kube/exec.go` | `ExecClient`、通道解复用、`ExecResult` |
| `internal/envadapter/kube/exec_test.go` | 一个说真协议的服务端 |
| `internal/envadapter/kube/adapter.go` | `kube.exec` 工具；实例绑定；开关 |
| `internal/config/config.go` | `EnvConfig.Exec *bool` —— 只能关闭 |
| `internal/audit/structural_test.go` | `TestExecClientAddressesOneEndpoint` |
| `pkg/errs/registry.go` | `MAS-4210`…`MAS-4214` |

## 2. 护栏

```go
// ExecEffect 描述"在某个容器内执行一条命令"。它是一个带两重约束的效果，
// 而不是两个效果：若建模成两次 Authorize 调用，组合方式就落到了调用方手里，
// 而漏掉命令检查的调用方照样能编译通过。
type ExecEffect struct {
    Namespace, Pod, Container string
    Binary                    string
    Args                      []string
}

type Call struct {
    …
    Exec *ExecEffect
}
```

`Authorize` 把 `Exec` 计入互斥效果之列（仍然是"恰好一个"），随后：

```go
func (g *Guard) authorizeExec(c Call) error {
    // 1 · 命令必须通过与本地适配器完全相同的白名单 ——
    //     原样复用 authorizeCommand，使两者永远不可能分叉。
    // 2 · 其所隐含的 URL 必须匹配 exec 路径规则，且别无其他。
    // 3 · namespace、Pod 与容器必须符合 DNS-1123 形态：否则一个含
    //     "/" 或 ".." 的路径分量，就能从第 2 步刚刚检查过的规则里逃逸。
}
```

第 3 步是微妙的那一步。路径规则是针对"由这三个字段拼出的 URL"的正则；
若某个字段可以含有斜杠，拼出的 URL 仍会匹配该规则，实际访问的却是别处。
校验各分量，正是让那条规则**名副其实**的东西。

新增路径规则，就排在它的邻居之间：

```go
{"GET", `^/api/v1/namespaces/[^/]+/pods/[^/]+/exec$`, "Kubernetes exec (read-only commands only)"},
```

它是一条 `GET` 规则，因为 WebSocket 升级本身就是 GET —— 护栏中不会引入新动词。

## 3. WebSocket 客户端

```go
// dialWebSocket 在既有 TLS 传输之上完成 RFC 6455 握手，并返回一个帧读取器。
// 它刻意保持最小：本项目只需要在一条短生命周期连接上读取服务端帧，
// 握手之后永远不需要再发送任何东西。
func dialWebSocket(ctx context.Context, hc *http.Client, url string,
    header http.Header, subprotocol string) (*wsConn, error)

func (c *wsConn) ReadMessage() (opcode byte, payload []byte, err error)
func (c *wsConn) Close() error
```

握手：带 `Upgrade: websocket`、`Connection: Upgrade`、
`Sec-WebSocket-Version: 13`、16 字节随机 `Sec-WebSocket-Key` 与子协议的 `GET`。
必须收到 `101`，且 `Sec-WebSocket-Accept` 要与
`base64(sha1(key + RFC6455 GUID))` 校验一致 ——
不校验 accept，会让一个根本没理解升级的代理看起来像是成功了。

连接通过我们自行拨号的 `net.Conn` 获得，读操作把上下文截止时间设置到该
`net.Conn` 上，因此一个卡住的 apiserver 无法比本次运行的超时活得更久（RSK-001）。

帧：服务端到客户端的帧永不掩码；续帧会被重组；`Ping` 以 `Pong` 应答；
`Close` 结束读循环。保留位或意外的 opcode 记为 `MAS-4213`。

## 4. `ExecClient`

```go
// ExecClient 在一个容器内执行一条只读命令。
//
// 它恰好只有一个方法，且不接受路径参数：URL 由 namespace、Pod 与容器拼成，
// 不存在任何输入能让调用方指向别的端点。TestExecClientAddressesOneEndpoint
// 断言这一形态 —— 正是它让 kube.Client 得以原样保留自己那条基于方法名的
// 不变量（plan.zh.md §1）。
type ExecClient struct { … }

type ExecRequest struct {
    Namespace, Pod, Container string
    Command                   []string // argv；从不是字符串，因此无物需要 shell
    MaxBytes                  int
}

type ExecResult struct {
    Stdout, Stderr string
    ExitCode       int
    Truncated      bool
}

func (c *ExecClient) Run(ctx context.Context, req ExecRequest) (ExecResult, error)
```

查询参数：argv 每个元素一个 `command=`，外加 `container=`、`stdout=true`、
`stderr=true`、`stdin=false`、`tty=false`。
不带 `stdin` 不是为了省事 —— 那是"一次读取"与"一段会话"的分界（HLD §4）。

解复用：每个二进制帧的第 0 字节是通道号 —— 1 为 stdout、2 为 stderr、3 为 status。
status 帧携带一段 JSON `Status`；`status: "Success"` 表示退出码 0，
否则退出码取自 `details.causes[reason="ExitCode"].message`。
若流结束时始终没有 status 帧，记为 `MAS-4214`：此时命令的结果是未知的，
而把未知报成成功，正是本项目绝不能做的事。

## 5. 工具

```
kube.exec(instance, command_id) → 证据
```

该工具接收的是来自已解析目标的**实例名**，以及来自知识包的**命令 id** ——
从不接收 Pod 名，也从不接收来自模型的 argv。
因此，一个读过恶意日志行的模型，最多只能请求"知识包已声明的命令"、
在"目标已解析到的 Pod"里执行（HLD §4、D-5）。

容器：知识包的命令可以指定一个；否则取该 Pod 的第一个容器。

## 6. 配置

```yaml
envs:
  prod:
    type: kubernetes
    exec: false        # 只能收紧：缺省或 true 表示由护栏决定
```

不存在任何能放宽的键。`exec: false` 会把该工具整个从注册表移除，
因此被关闭的环境即便被提示词要求也无法执行 ——
而 `doctor` 会把它报告为一项策略决定，而不是一项缺失的能力。

## 7. 错误

| 错误码 | 含义 |
|---|---|
| `MAS-4210` | 该环境已按配置关闭 exec |
| `MAS-4211` | 所指定的 Pod 不属于已解析目标的实例 |
| `MAS-4212` | 连接无法升级（RBAC、策略，或 Pod 不存在） |
| `MAS-4213` | 远程命令流格式错误 |
| `MAS-4214` | 命令的退出状态始终未到达 |

拒绝沿用护栏既有的错误码：本特性没有新增任何"以前不存在的被拒方式"。

## 8. 测试

| 测试 | 性质 |
|---|---|
| `TestGuardAuthorisesExecAsOneEffect` | FR-002 —— 两重约束、一次调用 |
| `TestExecRefusesUnlistedBinary`、`TestExecRefusesMutatingCommand` | FR-003、FR-004 —— 在任何连接之前 |
| `TestExecPathComponentsCannotEscape` | §2 第 3 步 —— 分量含斜杠或 `..` 时被拒 |
| `TestClientHasNoMutatingMethods` | FR-005 —— 未被修改，仍然通过 |
| `TestExecClientAddressesOneEndpoint` | 作为替代的那条不变量 |
| `TestExecCapturesStreamsAndExitStatus` | FR-006，对着说真协议的服务端 |
| `TestExecTruncatesAtCeiling` | FR-007 |
| `TestExecRefusesPodOutsideTarget` | FR-008 |
| `TestExecCanBeDisabledPerEnvironment` | FR-009 |
| `TestExecRequiresOnlineMode` | FR-010 |
| `TestExecUpgradeFailureIsCoded`、`TestExecMalformedFrameIsCoded`、`TestExecMissingStatusIsCoded` | FR-012 |
| `TestExecHonoursTimeout` | NFR-006 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | 初版详细设计 | tasks、代码 |
