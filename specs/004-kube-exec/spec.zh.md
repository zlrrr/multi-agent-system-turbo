# 特性规格：在 Kubernetes Pod 内执行只读命令

> **特性 ID**：`004-kube-exec` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`spec.md`](./spec.md) · **上游**：[`docs/zh/project-goals.md`](../../docs/zh/project-goals.md) v1.1.2
> **宪法**：`.specify/memory/constitution.zh.md` v1.0.0 · **下游**：`plan.zh.md`

## 1. 问题陈述

知识包声明了一批只读检查命令 —— `redis-cli INFO all`、
`mongosh --eval "db.serverStatus()"`、`kafka-topics.sh --describe` ——
而哪些是安全的，护栏已经能够判定。当中间件以二进制方式跑在主机上时，本地适配器会执行它们。
但在 Kubernetes 上 —— 也就是目标文档点名的主流部署形态 —— 没有任何东西会执行它们：
适配器能列 Pod、读日志、读事件，然后就停在那里。

这个缺口并不只是"少了点锦上添花"。`INFO all` 回答的是任何指标都无法回答的问题 ——
`mem_fragmentation_ratio`、`rdb_last_bgsave_status`、当前真正生效的 `maxmemory-policy` ——
一位在没有这些信息的情况下排查内存故障的运维人员，正是在对"中间件本来会主动告诉他的那些数字"做猜测。

阻碍是**刻意设置**的，这也正是这件事需要一份规格、而不是一个补丁的原因。
`internal/envadapter/kube.Client` 在结构上就不具备执行能力：
只要某个方法名里含有 `Exec`、`Attach` 或 `PortForward`，`TestClientHasNoMutatingMethods`
就会让构建失败。那条测试用"方法名"这个无需任何判断即可检查的代理量，
编码了一条真实的不变量 —— Kubernetes 客户端不能改变集群状态。
本设计要么保住这个代理量，要么用一个同样可被机械检查的东西替换它。
把它弱化成一句注释，等于用一条结构性保证换一个口头承诺。

还有第二个必须被明确拒绝的诱惑。最省事的实现是把 `kubectl` 放进白名单，然后调 `kubectl exec`。
可 `kubectl` 能删掉一整个 namespace；把它放进白名单，等于用一个二进制名把整个 Kubernetes API
塞进了白名单 —— 而这正是"默认拒绝"要堵上的那个洞，
也正是 OceanBase 知识包至今把 `obclient` 挡在门外的同一条理由。

## 2. 用户与场景

| 角色 | 目标 | 触发 |
|---|---|---|
| 在 Kubernetes 上运行 Redis 的 SRE | 像在主机上那样，从故障 Pod 里取到 `INFO all` | 指标与现象对不上的内存故障 |
| 在 Kubernetes 上运行 MongoDB 的 SRE | 从主节点读取 `rs.status()` | 复制滞后，但没有对应的 exporter |
| 平台工程师 | 确保该工具在其集群中只可能执行经过审核的只读命令 | 上线前的安全评审 |
| 受策略约束的运维人员 | 为某个集群彻底关闭容器内执行 | 其策略无论命令是什么都禁止 exec |

## 3. 范围

### 范围内
- 通过 Kubernetes API，在 Pod 内执行知识包声明的检查命令，
  使用与本地适配器**完全相同**的护栏与白名单。
- 基于 `net/http` 与 `crypto/tls` 实现 Kubernetes 的远程命令协议（WebSocket 通道）——
  不引入新依赖，也不外挂任何进程。
- 在护栏中引入一等的 `ExecEffect`：因为"在那个 Pod 里执行这条命令"是**一个**带两重约束的效果，
  而不是两个效果。
- 容器选择、输出采集、退出状态与字节上限。
- 一个只能收紧的配置开关，可为某个环境关闭 exec。
- 双语文档，含"本特性刻意做不到什么"。

### 范围外
- `stdin`、TTY、`attach`、`port-forward`、`cp`。读取状态一个都不需要，
  而每一个都会扩大"被污染的提示词"所能触及的范围。
- SPDY。本项目所面向的每个 Kubernetes 版本都支持 WebSocket 传输，
  两种都实现只会让审计面翻倍，却换不来任何新能力。
- 任何尚未进入护栏白名单的命令。本特性改变的是受审核命令**能在哪里**执行，
  绝不改变**哪些**命令通过了审核。
- 在诊断目标并未解析到的 Pod 中执行。

## 4. 功能需求

| ID | 需求 | 优先级 | 验收信号 |
|---|---|---|---|
| FR-001 | Kubernetes 适配器必须提供一个工具，在已解析目标的 Pod 内执行知识包的检查命令 | P0 | `TestExecRunsPackInspectCommand` |
| FR-002 | 每一次 exec 都必须作为单个 `ExecEffect` 由护栏授权，并同时对照命令白名单与 exec 路径规则 | P0 | `TestGuardAuthorisesExecAsOneEffect` |
| FR-003 | 白名单之外的命令必须在建立任何连接之前就被拒绝 | P0 | `TestExecRefusesUnlistedBinary` |
| FR-004 | 即便传输通道完全相同，具有变更性的命令也必须被拒绝 | P0 | `TestExecRefusesMutatingCommand` |
| FR-005 | `kube.Client` 必须在结构上仍然不具备执行能力：既有的基于方法名的审计必须原样通过 | P0 | `TestClientHasNoMutatingMethods` 未被修改 |
| FR-006 | exec 必须采集 stdout、stderr 与退出状态，并把非零退出记为缺口而非本次运行的失败 | P0 | `TestExecCapturesStreamsAndExitStatus` |
| FR-007 | 输出必须受护栏字节上限约束，且截断必须被记录 | P0 | `TestExecTruncatesAtCeiling` |
| FR-008 | Pod 与容器必须从已解析目标的实例中选取；调用方指定的、不属于该集合的 Pod 必须被拒绝 | P0 | `TestExecRefusesPodOutsideTarget` |
| FR-009 | 环境必须能够彻底关闭 exec，且该开关只能收紧 | P1 | `TestExecCanBeDisabledPerEnvironment` |
| FR-010 | 与其他所有实时工具一样，exec 在离线模式下必须不可用 | P1 | `TestExecRequiresOnlineMode` |
| FR-011 | `mas doctor` 必须报告每个 Kubernetes 环境是否可用 exec，不可用时说明原因 | P1 | doctor 测试 |
| FR-012 | 连接升级失败必须降级为带码的缺口，绝不能 panic 或挂起 | P0 | `TestExecUpgradeFailureIsCoded` |

## 5. 非功能需求

| ID | 需求 | 度量 |
|---|---|---|
| NFR-001 | 不引入新的模块依赖 | `go.mod` 无变化 |
| NFR-002 | WebSocket 实现必须对着一个真正说该帧格式的服务端受测，而不是对着我们自己客户端的 mock | 用 `net/http` 劫持连接搭建的测试服务端 |
| NFR-003 | 每一次 exec 都必须带着命令、Pod 与退出状态出现在运行记录中 | 回放测试 |
| NFR-004 | 密钥不得进入转录：参数由与其他一切相同的脱敏器处理 | 脱敏测试 |
| NFR-005 | 每一个面向运维人员的字符串都双语对等 | `sddctl verify` |
| NFR-006 | 一次 exec 必须遵守本次运行的超时，且绝不超时后仍存活 | `TestExecHonoursTimeout` |

## 6. 约束

| ID | 约束 | 来源 |
|---|---|---|
| CON-001 | 始终只读；传输方式的变化不改变"什么被允许" | 宪法第四条 |
| CON-002 | 不得把 `kubectl` 加入命令白名单 | 默认拒绝；一个二进制名不得代表整个 API |
| CON-003 | 任何配置项都不得放宽护栏，包括本特性的开关 | 第四条第 2 款 |
| CON-004 | 连接两端都永远不使用 shell | 第四条；既有审计 |
| CON-005 | exec 路径规则必须只匹配 exec 子资源，别无其他 | 第四条第 1 款 |

## 7. 验收

当知识包的检查命令能在真实集群的 Pod 内执行、其输出作为证据出现；
白名单之外或具有变更性的命令在连接建立之前即被拒绝；
`kube.Client` 依然无法表达"执行"这件事；exec 可按环境关闭；
且 `make ci` 全绿时，本特性完成。

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | 初版规格 | plan、HLD、LLD、tasks、代码 |
