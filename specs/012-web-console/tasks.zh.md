# 任务拆解：只读 Web 控制台

> **特性 ID**：`012-web-console` · **版本**：1.0.0
> **双语对照**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.1

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明它的测试（宪章第六条
第 1 款），且只有该测试通过后才算 `done`。这里列出的每个测试都必须真实存在：`sddctl verify`
会检查。

## 阶段 A —— 提供服务

| ID | 任务 | 满足 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| TA01 | `ServerConfig.UI`、`UIConfig.On()`，默认开启 | FR-013 | `TestConsoleCanBeDisabled` | — | done |
| TA02 | 内嵌资源、白名单、`handleConsole`、`/ui` → `/ui/` | FR-001、FR-007、NFR-004 | `TestConsoleIsServed`、`TestConsoleServesOnlyItsOwnAssets` | TA01 | done |
| TA03 | `Routes()` 报告 `routes()` 实际注册的内容；`TestEveryRouteIsGuarded` 读取包内自己的匿名集合 | CON-002 | `TestEveryRouteIsGuarded` | TA02 | done |
| TA04 | 控制台路径匿名；所有数据通路仍然受守卫 | FR-002、FR-003、NFR-003 | `TestConsoleShellIsAnonymousAndDataIsNot`、`TestConsoleServesNoEstateData` | TA03 | done |
| TA05 | CSP 及其余响应头 | FR-006 | `TestConsoleSendsAContentSecurityPolicy` | TA02 | done |
| TA06 | `MAS-7016`，双语；重新生成错误码文档 | FR-013、CON-004 | `mas errcodes` 输出为最新 | TA01 | done |
| **G-A** | **闸口 A** | | 控制台已提供、守卫正确，并且可以关闭 | | done |

## 阶段 B —— 客户端

| ID | 任务 | 满足 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| TB01 | 双语字符串表与 `/ui/strings.json` | FR-008、NFR-002 | `TestConsoleStringsAreBilingual` | G-A | done |
| TB02 | `app.js`：state、`t`、`api`、`el`、哈希路由、五个视图 | FR-001、FR-009、NFR-005 | `TestConsoleStringsAreAllUsed`；资源合计 1500 行以内 | TB01 | done |
| TB03 | 资源中任何位置都没有 HTML 注入点 | FR-005、CON-003 | `TestConsoleNeverUsesAnHTMLSink` | TB02 | done |
| TB04 | 凭据：`sessionStorage`、仅走请求头、401 时清除 | FR-011 | `TestConsoleKeepsTheCredentialOutOfURLsAndCookies` | TB02 | done |
| TB05 | 只读：不发 POST，不引用 `diagnose` scope | FR-004、CON-001 | `TestConsoleNeverStartsADiagnosis` | TB02 | done |
| TB06 | 失败渲染错误码、消息与处置建议 | FR-010 | `TestConsoleRendersTheErrorCode` | TB02 | done |
| TB07 | 缺口、"仅供参考"、未定价成本与截断都渲染出来，不折叠隐藏 | FR-012、CON-005 | `TestConsoleSurfacesGapsAndAdvisoryStatus` | TB02 | done |
| **G-B** | **闸口 B** | | `go test ./internal/httpapi/...` | | done |

## 阶段 C —— 集成与文档

| ID | 任务 | 满足 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| TC01 | API 索引上的 `language` | FR-014 | `TestIndexReportsTheLanguage` | G-B | done |
| TC02 | `mas doctor` 报告控制台状态 | FR-015 | `TestDoctorReportsTheConsole` | TC01 | done |
| TC03 | 目标文档中解除 `NG-6`；P3-4 记为已交付；M4 退出条件达成 | — | `sddctl verify` cascade | TC02 | done |
| TC04 | 双语文档：用户手册、配置参考、README | NFR-002、NFR-001 | `sddctl verify` parity；`go.mod` 不变 | TC03 | done |
| **G-C** | **闸口 C —— 特性出口** | | `make ci` 通过；`make demo` 不变 | | done |

## 检查点闸口

| 闸口 | 任务 | 验证命令 |
|---|---|---|
| G-A | TA01–TA06 | `go test ./internal/config/... ./internal/httpapi/...` |
| G-B | TB01–TB07 | `go test ./internal/httpapi/...` |
| G-C | TC01–TC04 | `make ci && make demo` |

## 变更记录

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-27 | 初始任务拆解 | 代码、配置、文档 |
