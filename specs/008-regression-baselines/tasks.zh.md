# 任务拆解：回归基线与模型维度

> **特性 ID**：`008-regression-baselines` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有当该测试通过时才标记为 `done`。
此处提到的每一个测试都必须真实存在：`sddctl verify` 会检查这一点。

## 阶段 A —— 记录

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T701 | `Class`、`Cell`、`Outcome.Class`，其中 `wrong` 优先于 `miss` | FR-001、CON-001 | `TestBaselineRecordsEveryCell` | — | done |
| T702 | `Baseline`、`LoadBaseline`、`Save`，按键排序且逐字节稳定 | FR-001、FR-012 | `TestBaselineIsByteStableAcrossRuns` | T701 | done |
| T703 | 除了显式要求写入的那个 flag，没有任何东西会写基线 | FR-002、CON-003 | `TestBaselineIsNeverWrittenImplicitly` | T702 | done |
| T704 | 错误码 `MAS-9105`…`MAS-9107`，双语，并重新生成文档 | CON-004 | `mas errcodes` 输出为最新 | T702 | done |
| **G-A** | **闸门 A** | | 基线可原样往返 | | done |

## 阶段 B —— 比较

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T710 | `Compare`：六种转移，不做任何抵消 | FR-003、CON-001 | `TestRegressionsAndImprovementsAreReportedSeparately` | G-A | done |
| T711 | 同样的失败重复出现即"已知为坏"，且通过闸门 | FR-004 | `TestKnownBadCellDoesNotFailTheGate` | T710 | done |
| T712 | id 不同的失败属于"失败方式改变" | FR-005 | `TestChangedFailureIsReported` | T711 | done |
| T713 | 基线中不存在的格子是新增，而不是回归 | FR-006 | `TestNewCellIsNotARegression` | T710 | done |
| T714 | 本次运行中不存在的格子报告为未运行 | FR-007 | `TestMissingCellIsReported` | T710 | done |
| T715 | provider 不匹配会在展示结果的任何地方被披露 | FR-008 | `TestProviderMismatchIsDisclosed` | T710 | done |
| **G-B** | **闸门 B** | | `go test ./internal/eval/...` | | done |

## 阶段 C —— 模型维度

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T720 | `Options.Models`；`Matrix` 叉乘 case × 拓扑 × 模型 | FR-009 | `TestModelAxisRunsEveryCell` | G-B | done |
| T721 | 逐格记账归属到真正跑它的那个模型 | FR-010、NFR-001 | `TestPerCellAccountingIsAttributed` | T720 | done |
| **G-C** | **闸门 C** | | `go test ./internal/eval/...` | | done |

## 阶段 D —— 界面、语料库与 CI

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T730 | `RenderDelta` 与"一个样本"限定声明，两种语言与 JSON | FR-011、CON-002 | `TestComparisonCarriesTheSamplingCaveat` | G-C | done |
| T731 | `mas eval --baseline / --write-baseline / --models` | FR-013 | `TestEvalBaselineCLI` | T730 | done |
| T732 | 为内置语料库提交一份基线，并由 CI 强制执行 | FR-014、NFR-004 | `TestShippedBaselineMatchesTheCorpus` | T731 | done |
| T733 | 双语文档：评测指南、用户手册、README | NFR-003、NFR-002 | `sddctl verify` 对等检查；`go.mod` 未变 | T732 | done |
| **G-D** | **闸门 D —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T701–T704 | `go test ./internal/eval/...` |
| G-B | T710–T715 | `go test ./internal/eval/...` |
| G-C | T720–T721 | `go test ./internal/eval/...` |
| G-D | T730–T733 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版任务拆解 | 代码、基线、文档 |
