# 任务分解：在 Kubernetes Pod 内执行只读命令

> **特性 ID**：`004-kube-exec` · **版本**：1.0.1
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有该测试通过后才可标记为 `done`。

## 阶段 A —— 护栏

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T301 | `ExecEffect`、`Call.Exec`，且仍保持"恰好一个效果" | FR-002、CON-001 | `TestGuardAuthorisesExecAsOneEffect` | — | done |
| T302 | `authorizeExec` 原样复用命令白名单 | FR-003、FR-004、CON-002 | `TestExecRefusesUnlistedBinary`、`TestExecRefusesMutatingCommand` | T301 | done |
| T303 | exec 路径规则，以及使其无法被逃逸的分量校验 | CON-005 | `TestExecPathComponentsCannotEscape` | T301 | done |
| T304 | 错误码 `MAS-4210`…`MAS-4214`，双语，并重新生成文档 | NFR-005 | `mas errcodes` 输出为最新 | T301 | done |
| **G-A** | **闸门 A** | | `go test ./internal/safety/...` 全绿；拒绝行为不触网即可发生 | | done |

## 阶段 B —— 传输

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T310 | 从服务端侧说 `v4.channel.k8s.io` 的测试服务端，且两端都不引入新模块依赖 | NFR-001、NFR-002 | 供以下每个测试使用 | G-A | done |
| T311 | 带 `Sec-WebSocket-Accept` 校验的 RFC 6455 握手 | FR-012 | `TestWebSocketRejectsUnverifiedAccept` | T310 | done |
| T312 | 帧读取器：续帧、ping/pong、关闭、畸形帧 | FR-012 | `TestExecMalformedFrameIsCoded` | T311 | done |
| T313 | 通道解复用，以及从 status 帧取退出状态 | FR-006 | `TestExecCapturesStreamsAndExitStatus`、`TestExecMissingStatusIsCoded` | T312 | done |
| T314 | 字节上限与截断 | FR-007 | `TestExecTruncatesAtCeiling` | T313 | done |
| T315 | 把上下文截止时间作用到连接上 | NFR-006 | `TestExecHonoursTimeout` | T311 | done |
| **G-B** | **闸门 B** | | `go test ./internal/envadapter/kube/...` 在 `-race` 下全绿 | | done |

## 阶段 C —— 工具

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T320 | 只有一个方法、且不接受路径参数的 `ExecClient` | FR-005 | `TestExecClientAddressesOneEndpoint`；`TestClientHasNoMutatingMethods` 未被修改 | G-B | done |
| T321 | 接收实例与知识包命令 id 的 `kube.exec` 工具 | FR-001、FR-008 | `TestExecRunsPackInspectCommand`、`TestExecRefusesPodOutsideTarget` | T320 | done |
| T322 | `exec: false` 移除该工具；只能收紧 | FR-009、CON-003 | `TestExecCanBeDisabledPerEnvironment` | T321 | done |
| T323 | 要求在线模式 | FR-010 | `TestExecRequiresOnlineMode` | T321 | done |
| T324 | 升级失败降级为带码缺口 | FR-012 | `TestExecUpgradeFailureIsCoded` | T321 | done |
| **G-C** | **闸门 C** | | 知识包的 inspect 命令对着测试服务端端到端跑通 | | done |

## 阶段 D —— 呈现与文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T330 | `mas doctor` 报告 exec 是否可用及不可用的原因 | FR-011 | doctor 测试 | G-C | done |
| T331 | 运行记录携带命令、Pod 与退出状态 | NFR-003 | 回放测试 | G-C | done |
| T332 | exec 路径上的参数脱敏 | NFR-004 | 脱敏测试 | G-C | done |
| T333 | 双语文档：用户手册、配置参考、知识包指南 | NFR-005 | `sddctl verify` 对等检查 | G-C | done |
| T334 | 修正手册中被本特性变为不实的那句 RBAC 承诺（"MAS-Turbo 绝不请求 `pods/exec`"） | NFR-005、CON-002 | 手册写明该放宽、其四重边界与关闭方式 | G-C | done |
| T335 | 结构审计：exec 只能经由 `ExecEffect` 触及，且 `kubectl` 始终不入白名单 | NFR-001、NFR-005、CON-002、CON-005 | `TestExecIsReachableOnlyThroughTheGuard`、`TestNoKubectlInTheAllowList` | G-A | done |
| T336 | 每一条已发布的知识包命令都必须能通过容器内替换 | FR-001 | `TestEveryPackInspectCommandSurvivesContainerSubstitution` | T321 | done |
| **G-D** | **闸门 D —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T301–T304 | `go test ./internal/safety/...` |
| G-B | T310–T315 | `go test -race ./internal/envadapter/kube/...` |
| G-C | T320–T324 | `go test ./internal/envadapter/... ./internal/audit/...` |
| G-D | T330–T333 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.1 | 2026-08-25 | 新增 T334–T336：护栏自身的对抗测试否决了最初的路径规则设计；手册中有一句被本特性证伪的 RBAC 承诺；而丢失端口的知识包模板会把一条受审核命令悄悄变成被拒命令 | 已修正手册；新增两条结构审计；逐条校验全部知识包命令在容器内可用 |
| 1.0.0 | 2026-08-25 | 初版任务分解 | 护栏、传输、工具、文档 |
