# 任务分解：可切换的多 Agent 拓扑

> **特性 ID**：`003-switchable-topologies` · **版本**：1.0.1
> **双语对应文件**：[`tasks.md`](./tasks.md) · **上游**：[`design-lld.zh.md`](./design-lld.zh.md) v1.0.0

## 图例
`status` ∈ `todo | doing | done | blocked`。每个任务在实现之前先声明其测试
（宪法第六条第 1 款），且只有该测试通过后才可标记为 `done`。

## 阶段 A —— 可比性契约

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T201 | 抽出 `core.Text`；`knowledge.Text` 改为其别名 | NFR-004 | 既有 knowledge 测试原样通过 | — | done |
| T202 | 注册表中的双语 `Description`；用 `Cost` 与 `Avoid` 重述 `supervisor` 与 `single` | FR-010、NFR-004 | `TestTopologyDescriptionsAreBilingualAndHonest` | T201 | done |
| T203 | 一致性契约：被收录、假设+摘要、角色归属、预算、降级、确定性 | FR-001、FR-005、FR-006、FR-007、FR-008、FR-009、NFR-003 | 在任何新拓扑存在之前，对 `supervisor` 与 `single` 通过 | T202 | done |
| T204 | 坏拓扑验证 | FR-001 | `TestBrokenTopologyFailsTheContract` | T203 | done |
| T205 | 结构审计：拓扑不得触及所授注册表之外，也不得放宽护栏 | NFR-001、NFR-005、CON-001 | 扩展审计测试 | T203 | done |
| **G-A** | **闸门 A** | | 仅存两个既有拓扑时 `go test ./internal/orchestrator/... ./internal/audit/...` 全绿 | | done |

## 阶段 B —— `plan-execute`

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T210 | `Strategist` 与 `Executor` 角色、提示词、mock 回复 | FR-002、NFR-002 | 角色单元测试 | G-A | done |
| T211 | 带有界自适应轮次的 `plan-execute` 拓扑 | FR-002、CON-003 | `TestPlanExecuteReplansOnFindings`、`TestPlanExecuteStopsWhenConverged` | T210 | done |
| T212 | `plan-execute` 通过一致性契约 | FR-001、FR-005…FR-009 | 契约，逐拓扑执行 | T211 | done |

## 阶段 C —— `debate`

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T220 | `Advocate` 与 `Judge` 角色、提示词、mock 回复 | FR-003 | 角色单元测试 | G-A | done |
| T221 | `debate` 拓扑，advocate 并发，笔记顺序确定 | FR-003、CON-003 | `TestDebateProducesAdjudicatedPositions`、`TestDebateWithoutPositionsFallsBack` | T220 | done |
| T222 | `debate` 通过一致性契约 | FR-001、FR-005…FR-009 | 契约，逐拓扑执行 | T221 | done |

## 阶段 D —— `blackboard`

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T230 | 确定性的"具备条件"控制组件 | FR-004 | `TestBlackboardSchedulesByEligibility` | G-A | done |
| T231 | `blackboard` 拓扑；某轮毫无贡献即终止 | FR-004 | `TestBlackboardTerminates`、`TestBlackboardSkipsWhatCannotContribute` | T230 | done |
| T232 | `blackboard` 通过一致性契约 | FR-001、FR-005…FR-009 | 契约，逐拓扑执行 | T231 | done |
| **G-B** | **闸门 B** | | 五个拓扑全部通过同一份契约 | | done |

## 阶段 E —— 呈现与文档

| ID | 任务 | 满足需求 | 测试 / 检查点 | 依赖 | 状态 |
|---|---|---|---|---|---|
| T240 | `mas topologies` 以运维人员的语言渲染；HTTP 端点同理 | FR-010 | CLI 测试断言五个名称与两种语言 | G-B | done |
| T241 | 运行记录与报告携带拓扑及其成本 | FR-011 | `TestRunRecordCarriesTopologyAccounting` | G-B | done |
| T242 | 未知拓扑仍在任何模型调用之前以 `MAS-3001` 失败 | FR-012 | 扩展既有准入测试至新名称 | G-B | done |
| T243 | 双语文档：用户手册 §8 按五个拓扑重写，含成本与"何时不该选"两列 | FR-010、NFR-004 | `sddctl verify` 对等检查 | G-B | done |
| T244 | 列表类命令遵循已配置语言，而不只是 `--lang` | FR-010 | `TestTopologiesCommandDescribesEveryTopologyBilingually` | G-B | done |
| T245 | CLI 折行按终端列宽度量并可在 CJK 处断行，使中文按预期宽度折行 | FR-010、NFR-004 | `TestWrapMeasuresColumnsNotBytes`、`TestWrapBreaksCJKWithoutSpaces`、`TestWrapKeepsClosingPunctuationOnTheLineItCloses` | T244 | done |
| T246 | 假设引用需与本次运行的采集结果比对解析（由契约"让每个拓扑在无工具下跑一遍"发现） | NFR-002 | `TestFabricatedCitationsAreDroppedAndRecorded`、`TestRealCitationsSurvive` | T203 | done |
| **G-C** | **闸门 C —— 特性出口** | | `make ci` 全绿 | | done |

## 检查点闸门

| 闸门 | 任务 | 验证命令 |
|---|---|---|
| G-A | T201–T205 | `go test ./internal/orchestrator/... ./internal/audit/...` |
| G-B | T210–T232 | `go test ./internal/orchestrator/... ./internal/agent/...` |
| G-C | T240–T243 | `make ci` |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.1 | 2026-08-24 | 新增 T244–T246：一致性契约与双语呈现暴露出三处本特性代码之外的缺陷 —— 列表类命令忽略了已配置语言、CLI 折行按字节度量导致中文只折到三分之一宽度、假设引用未经解析就被原样印出 | `specs/001-mvp-core/design-lld.zh.md` 修订至 1.0.3 |
| 1.0.0 | 2026-08-24 | 初版任务分解 | 角色、拓扑、文档 |
