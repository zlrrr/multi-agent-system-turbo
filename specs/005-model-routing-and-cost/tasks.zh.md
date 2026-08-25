# 任务分解：模型路由与如实的成本核算

> **特性 ID**：`005-model-routing-and-cost` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有该测试通过后才可标记为 `done`。

## 阶段 A —— 成本变成一个类型

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T401 | 带 `Known`、`Unpriced`、`Add` 与 `String` 的 `core.Cost` | FR-005、CON-001 | `TestCostAddIsUnknownIfEitherIs` | — | done |
| T402 | 移除 `Usage.CostUSD`；更新编译器点名的每一个消费方 | FR-006 | 构建通过；没有任何地方渲染裸 0 | T401 | done |
| T403 | 报告以两种语言渲染成本或写明未定价 | FR-006、NFR-004 | `TestUnpricedRunSaysSoInBothLanguages`、`TestNoRenderedReportContainsBareZeroCost` | T402 | done |
| **G-A** | **闸门 A** | | 未定价的运行会明说；没有任何路径能打印它未曾拿到的 `$0.00` | | done |

## 阶段 B —— 定价

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T410 | `llm.Pricing` 与 `CostOf`；配置项 | FR-004、NFR-002、NFR-003 | `TestZeroPriceIsKnownAbsentPriceIsNot` | G-A | done |
| T411 | 按实际使用的模型逐次交互累加成本 | FR-004 | `TestCostIsComputedFromConfiguredPrices` | T410 | done |
| T412 | 部分定价的运行报告已定价部分并点名其余 | FR-009 | `TestPartiallyPricedRunIsExplicit` | T411 | done |

## 阶段 C —— 路由

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T420 | `llm.Router`、`Route`；具名 provider；继承 | FR-001、NFR-002 | `TestPerRoleProviderIsUsed`、`TestRouteInheritsDefaults` | G-A | done |
| T421 | provider 在准入阶段只打开一次，并随运行关闭 | FR-002、FR-003 | `TestProvidersAreOpenedOnceAndClosed`、`TestUnopenableRoleProviderFailsAdmission` | T420 | done |
| T422 | Agent 循环改用 router | FR-001、NFR-001 | 既有 Agent 测试外加路由 | T420 | done |
| T423 | 按角色 provider 的凭据脱敏方式与默认一致 | NFR-005 | 脱敏测试 | T421 | done |

## 阶段 D —— 归属

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T430 | 以角色为键的 `Counting`；`ByRole()` | FR-007 | `TestUsageIsAttributedPerRole`、`TestAttributionSumsToTheTotal` | T411、T420 | done |
| T431 | 在会并发运行角色的拓扑下保持正确 | NFR-006 | `-race` 下的 `TestAttributionUnderConcurrentTopology` | T430 | done |
| T432 | 报告与运行记录携带明细与路由 | FR-008、FR-012 | `TestReportCarriesPerRoleBreakdown`、`TestRunRecordCarriesRouting` | T430 | done |
| **G-B** | **闸门 B** | | 已定价的运行按角色报告成本；未定价的运行点名缺了什么 | | done |

## 阶段 E —— 呈现与文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T440 | `mas models` 打印生效路由 | FR-011 | `TestModelsCommandShowsEffectiveRouting` | G-B | done |
| T441 | `mas doctor` 报告哪些模型已定价 | FR-010 | doctor 测试 | G-B | done |
| T442 | 双语文档：用户手册、配置参考、README | NFR-004 | `sddctl verify` 对等检查 | G-B | done |
| T443 | 演示在调用数之外同时打印按拓扑的成本 | FR-008 | `make demo` 输出 | G-B | done |
| **G-C** | **闸门 C —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T401–T403 | `go test ./internal/core/... ./internal/report/...` |
| G-B | T410–T432 | `go test -race ./internal/llm/... ./internal/agent/... ./internal/orchestrator/...` |
| G-C | T440–T443 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | 初版任务分解 | 成本类型、定价、路由、归属、文档 |
