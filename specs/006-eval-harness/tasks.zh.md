# 任务分解：Case 语料库与评测框架

> **特性 ID**：`006-eval-harness` · **版本**：1.0.0
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有该测试通过后才可标记为 `done`。
此处写下的每一个测试都必须真实存在：`sddctl verify` 会检查这一点。

## 阶段 A —— case

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T501 | `Case` schema、双语校验、内置语料库 | FR-001、FR-002、CON-004、NFR-003 | `TestCorpusLoadsFromDirectory`、`TestCaseSchemaRequiresAnExpectedOutcome` | — | done |
| T502 | case 不得指向其知识包并未声明的故障模式 | FR-002 | `TestCaseNamingAnUndeclaredModeIsRefused` | T501 | done |
| T503 | 错误码 `MAS-9100`…`MAS-9104`，双语，并重新生成文档 | NFR-003 | `mas errcodes` 输出为最新 | T501 | done |
| **G-A** | **闸门 A** | | case 能加载；无预期结果的 case 被拒 | | done |

## 阶段 B —— 运行器

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T510 | 由 case 构建的桩 Prometheus 与 Loki 服务端，基于 `net/http/httptest`，不引入新依赖 | FR-003、NFR-002、NFR-004 | 供以下每个测试使用 | G-A | done |
| T511 | 跑在真实 service 之上的运行器：流水线不做任何替换 | FR-003 | `TestHarnessUsesTheRealPipeline` | T510 | done |
| T512 | 未匹配到的查询返回空结果而不是 0 | FR-003 | `TestUnmatchedQueryReturnsEmptyNotZero` | T510 | done |
| **G-B** | **闸门 B** | | case 经真实流水线抵达结论 | | done |

## 阶段 C —— 打分

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T520 | `Outcome`：已得出、漏判、错误结论、缺失缺口 | FR-004、FR-005 | `TestFalseConclusionIsScoredSeparately` | G-B | done |
| T521 | 打分不读取任何散文字段 | FR-004、CON-002 | `TestScoringUsesNoTextSimilarity` | T520 | done |
| T522 | 被扣留的源必须产生一条被声明的缺口 | FR-006 | `TestWithheldSourceMustProduceADeclaredGap` | T520 | done |
| T523 | 重复运行的确定性 | FR-008、NFR-001 | `TestResultsAreDeterministic` | T520 | done |

## 阶段 D —— 矩阵、渲染与限定声明

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T530 | 矩阵：每个 case × 每个所选拓扑，并发有上限 | FR-007 | `TestMatrixRunsEveryCaseAgainstEveryTopology` | T523 | done |
| T531 | 渲染保持各项结果分离；不给出压缩分数 | FR-010、CON-002 | `TestReportKeepsOutcomesSeparate` | T530 | done |
| T532 | 限定声明由渲染器输出，两种语言，JSON 亦然 | NFR-005、CON-003、CON-001 | `TestRenderedResultAlwaysCarriesTheCaveats` | T531 | done |
| T533 | 脚本化 provider 拒绝把 Agent 结果当作模型质量呈现 | FR-009、CON-001 | `TestMockRunRefusesToClaimModelQuality` | T532 | done |
| T534 | `mas eval`、`--matrix`、`--cases`、`--topology`、`--json`，回归时退出码非零 | FR-011、FR-012 | `TestEvalCommand`、`TestEvalExitsNonZeroOnRegression` | T532 | done |

## 阶段 E —— 语料库与闸门

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T540 | 每个知识包一个内置 case | FR-013 | `TestEveryPackHasACase` | T534 | done |
| T541 | 语料库全部通过，且留在 CI 预算之内 | FR-012、NFR-001 | `TestCorpusRunsInsideTheCIBudget` | T540 | done |
| T542 | CI 运行语料库，并在回归时失败 | FR-012 | `make ci` 已包含 | T541 | done |
| T543 | 双语文档：用户手册、README、case 编写指引 | NFR-003 | `sddctl verify` 对等检查 | T541 | done |
| T544 | 修复语料库发现的问题：数据源不可用在任何拓扑下都必须是缺口 | FR-006 | `TestDownSourceIsAGapUnderEveryTopology` | T541 | done |
| **G-C** | **闸门 C —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T501–T503 | `go test ./internal/eval/...` |
| G-B | T510–T512 | `go test ./internal/eval/...` |
| G-C | T520–T544 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | 初版任务分解 | case schema、运行器、打分、语料库、文档 |
