# 任务分解：中间件知识广度

> **特性 ID**：`002-middleware-packs` · **版本**：1.0.1
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现前声明其测试（宪章 VI.1），
且只有当该测试通过时才算 `done`。

## 阶段 A —— 一致性下限

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T101 | `conformance_test.go`：深度、点名失效模式、双语、建议、风险词表、常开剧本 | FR-005、FR-007 | 对 redis 与 kafka 通过；对合成的浅包给出有信息量的失败 | — | done |
| T102 | 对每个包的巡检命令执行守卫校验 | FR-006、CON-002 | `TestPackInspectCommandsPassTheGuard` | T101 | done |
| T103 | 表达式编译与信号引用解析 | FR-009 | `TestPackExpressionsCompile` | T101 | done |
| T104 | 建议措辞扫描，覆盖中英文 | FR-007、CON-003 | `TestPackRecommendationsAreAdvisory` | T101 | done |
| T105 | indicators 与已声明信号的覆盖诚实性交叉核对 | NFR-002 | `TestPackCoverageIsHonest` | T101 | done |
| **G-A** | **闸门 A** | | 仅有 redis 与 kafka 时 `go test ./internal/knowledge/...` 全绿 | | done |

## 阶段 B–E —— 知识包

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T110 | MongoDB 包：≥10 信号、≥6 日志模式、7 个失效模式、≥3 剧本、巡检命令 | FR-001 | 一致性通过；剧本对桩遥测产出结论 | G-A | done |
| T120 | Pulsar 包 | FR-002 | 同上 | G-A | done |
| T130 | Milvus 包 | FR-003 | 同上 | G-A | done |
| T140 | OceanBase 包 | FR-004 | 同上 | G-A | done |
| T150 | 集成：每个新包经规则引擎端到端跑通 | FR-001…004 | `TestNewPacksRunAgainstStubTelemetry` | T110–T140 | done |
| T151 | 回归闸门：内置知识包体积不超预算，且在六个包加载下剧本选择保持确定 | NFR-003、NFR-004 | `TestEmbeddedPackSizeBudget`；六包加载下既有确定性测试仍通过 | T110–T140 | done |
| T152 | 集成测试暴露出的规则引擎纠正：正则字面量不是槽位引用；未被测量的指标不算“检查通过” | NFR-002 | `TestRegexLiteralsAreNotSlotReferences`、`TestIdentifiersIgnoreQuotedText`、`TestEmptyMetricIsNotReportedAsPassed`、`TestDeliberateEmptyReadingStillFires` | T150 | done |
| **G-B** | **闸门 B** | | 全部六个包通过同一个一致性测试，体积在预算内，且选择仍保持确定 | | done |

## 阶段 F —— 文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T160 | 双语知识包编写指南：Schema、表达式环境、一致性规则、指标来源表、`not X.empty` 指导 | FR-008、NFR-001 | `sddctl verify` 对等性通过；指南中的示例包可加载 | G-B | done |
| T161 | 确认未曾需要修改加载器 | FR-010 | `git diff --stat internal/knowledge/*.go` 仅显示新增测试 | G-B | done |
| T162 | `sdd.sh amend` 保留它所编辑文件中的复审注释 | NFR-001 | `TestAmendPreservesReviewerNotes` | T152 | done |
| T163 | 让 `MAS-5002` 名副其实：覆盖内置知识包仍受支持，两个本地知识包声明同一 id 则被报告 | FR-008、NFR-002 | `TestOverridingAShippedPackIsAllowed`、`TestTwoLocalPacksCollide` | T160 | done |
| **G-C** | **闸门 C —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T101–T105 | `go test ./internal/knowledge/...` |
| G-B | T110–T151 | `go test ./internal/knowledge/... ./internal/rules/... ./internal/service/...` |
| G-C | T160–T161 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.1 | 2026-08-24 | 新增 T152 与 T162：集成测试暴露出规则引擎的两处静默缺陷与 SDD 工具链的一处缺陷，均如实记录而非顺手带过 | `specs/001-mvp-core/design-lld.zh.md` 修订至 1.0.2 |
| 1.0.0 | 2026-08-24 | 初版任务分解 | 知识包、测试、文档 |
