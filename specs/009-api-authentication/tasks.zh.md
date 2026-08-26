# 任务拆解：HTTP API 的认证与授权

> **特性 ID**：`009-api-authentication` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有当该测试通过时才标记为 `done`。
此处提到的每一个测试都必须真实存在：`sddctl verify` 会检查这一点。

## 阶段 A —— 配置与准入

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T801 | `ServerAuth`、`APIToken`、`ServerTLS`；token 为 `Secret` | FR-004、CON-004 | `TestCredentialsAreNeverEchoed` | — | done |
| T802 | 加载时的 scope 与 token 校验 | FR-013、CON-001 | `TestScopelessOrUnknownScopeIsRejectedAtLoad` | T801 | done |
| T803 | `Admit`：非回环监听且未配置认证时拒绝启动 | FR-007 | `TestUnauthenticatedPublicBindIsRefused` | T802 | done |
| T804 | `Admit`：主机之外的明文凭据在未声明代理时被拒绝 | FR-008、CON-003 | `TestPlaintextCredentialsOffHostAreRefused` | T803 | done |
| T805 | 回环监听仍然完全不需要任何配置 | FR-009、NFR-004 | `TestLoopbackNeedsNoConfiguration` | T803 | done |
| T806 | 错误码 `MAS-7010`…`MAS-7014`，双语，并重新生成文档 | CON-005 | `mas errcodes` 输出为最新 | T802 | done |
| **G-A** | **闸门 A** | | 危险的配置无法打开监听器 | | done |

## 阶段 B —— 收口点

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T810 | `Authorizer`：按摘要查找，常数时间比较 | FR-003 | `TestTokenComparisonIsConstantTime` | G-A | done |
| T811 | 穷举且默认拒绝的路由表 | FR-006、CON-001、CON-002 | `TestEveryRouteIsGuarded` | T810 | done |
| T812 | 凭据缺失或未知时返回 401，两者共用同一个错误码 | FR-001 | `TestAnonymousRequestIsRefused` | T811 | done |
| T813 | 凭据缺少该路由所需 scope 时返回 403 | FR-002 | `TestScopeIsEnforcedPerRoute` | T812 | done |
| T814 | 健康检查端点无需凭据即可应答 | FR-005 | `TestHealthEndpointsStayAnonymous` | T811 | done |
| T815 | 每一次判定都连同主体记入审计，绝不记录凭据 | FR-012 | `TestAuthDecisionsAreAudited` | T813 | done |
| **G-B** | **闸门 B** | | `go test ./internal/httpapi/...` | | done |

## 阶段 C —— 归属与 TLS

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T820 | 主体抵达运行记录，取自 context 而绝不取自请求体 | FR-011 | `TestRunRecordCarriesThePrincipal` | G-B | done |
| T821 | 依据证书与私钥直接提供 TLS | FR-010 | `TestServesTLSDirectly` | G-B | done |
| **G-C** | **闸门 C** | | `go test ./internal/httpapi/... ./internal/service/...` | | done |

## 阶段 D —— 界面与文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T830 | `mas doctor` 报告 API 的暴露面以及保护它的是什么 | FR-014 | `TestDoctorReportsAPIExposure` | G-C | done |
| T831 | 双语文档：用户手册、配置参考、README | NFR-002、NFR-001、NFR-003 | `sddctl verify` 对等检查；`go.mod` 未变 | T830 | done |
| **G-D** | **闸门 D —— 特性出口** | | `make ci` 全绿；`make demo` 行为不变 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T801–T806 | `go test ./internal/config/... ./internal/httpapi/...` |
| G-B | T810–T815 | `go test ./internal/httpapi/...` |
| G-C | T820–T821 | `go test ./internal/httpapi/... ./internal/service/...` |
| G-D | T830–T831 | `make ci && make demo` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版任务拆解 | 代码、配置、文档 |
