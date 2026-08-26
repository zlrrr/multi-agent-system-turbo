# 特性规格：HTTP API 的认证与授权

> **特性 ID**：`009-api-authentication` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`spec.md`](./spec.md) · **上游**：[`docs/zh/project-goals.md`](../../docs/zh/project-goals.md) v1.1.9
> **宪法**：`.specify/memory/constitution.zh.md` v1.0.0 · **下游**：`plan.zh.md`

## 1. 问题陈述

`mas serve` 监听的是一个未经认证的 API。任何能访问到该端口的人，
都可以列出全部已配置的目标、读取全部已存储的诊断，
以及 —— 这才是关键 —— 调用 `POST /api/v1/diagnoses`，
而这会花掉模型 token，并读取运维人员的生产遥测数据。

只读保证让这件事没有它本可能的那么危险：没有人能用这个 API 弄坏一个集群。
但这并不等于安全。一份存储下来的诊断中包含来自生产系统的指标数值、
日志行与集群拓扑；目标列表则是这片资产的一张地图；
而一个未经授权的调用方，可以在端口开着的这段时间里一直累积模型账单。

底下还有第二个问题，而且正是它让第一个问题容易被做错。
今天的 API **完全不知道调用者是谁**，因此一条运行记录无法说明是谁请求的它。
当某次诊断事后被发现代价高昂，或读取了它本不该读取的系统时，
没有任何东西可供查看。

这两件事都属于 M4 的第一个排序事项，而它的出口条件明确点名了第一件：**API 已认证**。

## 2. 用户与场景

| 角色 | 目标 | 触发时机 |
|---|---|---|
| 平台工程师 | 把 API 暴露给团队，而不是暴露给整个网络 | 第一次部署到笔记本以外 |
| SRE | 给看板只读权限，但不让它花掉模型 token | 接一个状态页 |
| 安全评审人员 | 确认该服务无法被主机之外匿名访问 | 上线前评审 |
| 值班工程师 | 查明是谁请求了那次昂贵的诊断 | 成本复盘 |
| 开发者 | 在本地继续毫无仪式感地跑 `mas serve` | 每天 |

## 3. 范围

### 范围内
- **Bearer token** 认证，token 以配置密钥的形式提供。
- **Scope**：`read` 与 `diagnose`，按路由校验，默认拒绝。
- 当配置会把"未认证的"或"凭据以明文传输的" API 暴露到主机之外时，**拒绝启动**。
- 直接提供 **TLS**，或由运维人员显式声明由代理终止 TLS。
- 把已认证的主体记录到它所引发的那次运行上。
- 永不需要凭据的健康检查端点。
- 双语错误、文档与配置参考。

### 范围外
- OIDC、JWT 校验，或任何身份提供方集成。每一种都需要引入依赖，
  或者部分地重新实现一个 —— 而一个"部分实现的 JWT 校验器"，
  是一个有着友好名字的漏洞。
- 用户管理：运行时创建、轮换或吊销 token。token 是配置，
  而配置正是运维人员本就在管理密钥的地方。
- 按目标授权。一个可以做诊断的 token 可以诊断任何已配置的目标；
  把这一点拆开属于 P3-3 的多租户注册表，不属于本特性。
- 限流。它值得有，但属于另一个关注点；预算已经限定了一次运行能花多少。
- CLI 的认证。它以运维人员的身份、用运维人员的配置与凭据运行。

## 4. 功能需求

| ID | 需求 | 优先级 | 验收信号 |
|---|---|---|---|
| FR-001 | 不带有效凭据的请求必须以 `401` 与一个错误码被拒绝 | P0 | `TestAnonymousRequestIsRefused` |
| FR-002 | 凭据有效但缺少该路由所需 scope 时，必须以 `403` 与一个错误码被拒绝 | P0 | `TestScopeIsEnforcedPerRoute` |
| FR-003 | token 必须以常数时间比较 | P0 | `TestTokenComparisonIsConstantTime` |
| FR-004 | token 绝不得出现在日志、错误响应体或 `mas config` 的输出中 | P0 | `TestCredentialsAreNeverEchoed` |
| FR-005 | `/healthz` 与 `/readyz` 不得要求凭据 | P0 | `TestHealthEndpointsStayAnonymous` |
| FR-006 | 其余每一个路由都必须经过同一个授权收口点 | P0 | `TestEveryRouteIsGuarded` |
| FR-007 | 在非回环地址上监听而未配置认证时，必须拒绝启动 | P0 | `TestUnauthenticatedPublicBindIsRefused` |
| FR-008 | 在主机之外以明文传输凭据时必须拒绝启动，除非运维人员声明由代理终止 TLS | P0 | `TestPlaintextCredentialsOffHostAreRefused` |
| FR-009 | 回环地址监听必须在零配置下继续可用 | P0 | `TestLoopbackNeedsNoConfiguration` |
| FR-010 | 必须能依据配置中的证书与私钥直接提供 TLS | P1 | `TestServesTLSDirectly` |
| FR-011 | 已认证的主体必须被记录到它所引发的那次运行上 | P0 | `TestRunRecordCarriesThePrincipal` |
| FR-012 | 一次授权判定必须连同主体与结果一并记入日志，且绝不记录凭据本身 | P1 | `TestAuthDecisionsAreAudited` |
| FR-013 | 没有任何 scope 的 token，或含未知 scope 的 token，必须在加载时被拒绝 | P0 | `TestScopelessOrUnknownScopeIsRejectedAtLoad` |
| FR-014 | `mas doctor` 必须报告 API 的暴露面以及保护它的是什么 | P1 | `TestDoctorReportsAPIExposure` |

## 5. 非功能需求

| ID | 需求 | 度量 |
|---|---|---|
| NFR-001 | 不引入新的模块依赖 | `go.mod` 未变 |
| NFR-002 | 每一条面向运维人员的字符串都是双语 | `sddctl verify` |
| NFR-003 | 授权除查表外不引入额外的每请求内存分配 | 无需基准测试的结构性评审 |
| NFR-004 | demo 与现有全部测试保持原样可用 | `make demo`、`go test ./...` |

## 6. 约束

| ID | 约束 | 来源 |
|---|---|---|
| CON-001 | 默认拒绝：未登记的路由或未知的 scope 一律拒绝 | 宪法第七条 |
| CON-002 | 单一收口点；任何处理函数都不得自行授权 | 第七条第 2 款；护栏先例 |
| CON-003 | 会导致凭据在主机之外以明文暴露的配置，是被拒绝而不是被警告 | §1 |
| CON-004 | 密钥绝不进入日志或被渲染出来的配置 | 第八条 |
| CON-005 | 每条消息都要有两种语言 | 第三条 |

## 7. 验收

当满足以下条件时，本特性完成：在主机之外监听而没有认证与 TLS 时拒绝启动；
不带凭据的请求被拒绝；凭据缺少 scope 时被拒绝；健康检查仍可匿名应答；
运行记录写明了是谁请求的；任何 token 都不会进入日志；
且回环地址上的开发工作流丝毫未变。

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版规格 | plan、HLD、LLD、tasks、代码 |
