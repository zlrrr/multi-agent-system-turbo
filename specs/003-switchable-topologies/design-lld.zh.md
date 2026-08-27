# 详细设计（LLD）：可切换的多 Agent 拓扑

> **特性 ID**：`003-switchable-topologies` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

| 路径 | 内容 |
|---|---|
| `internal/core/text.go` | `Text` —— 双语字符串，从 `internal/knowledge` 下沉，使两个包能共用同一个 |
| `internal/knowledge/pack.go` | `type Text = core.Text` 别名；其余不变，YAML 不变 |
| `internal/orchestrator/orchestrator.go` | `Description`；`Register` 接收它；`Describe(lang)` |
| `internal/orchestrator/planexecute.go` | `plan-execute` |
| `internal/orchestrator/debate.go` | `debate` |
| `internal/orchestrator/blackboard.go` | `blackboard` |
| `internal/orchestrator/conformance_test.go` | 所有拓扑都必须满足的契约 |
| `internal/agent/roles.go` | `Strategist`、`Executor`、`Advocate`、`Judge` |
| `internal/agent/prompts.go` | 它们的指令 |
| `internal/llm/mock/mock.go` | 四个新角色的脚本化回复 |

## 2. 双语描述

```go
type Description struct {
    Summary core.Text // 控制流，一两句话说清
    Cost    core.Text // 成本画像，直说
    Choose  core.Text // 何时优先选它
    Avoid   core.Text // 何时不该选它
}

func (d Description) In(lang string) string // 渲染 Summary/Cost/Choose/Avoid
func Register(name string, d Description, f Factory)
func Descriptions(lang string) map[string]string
func Details() map[string]Description
```

`Avoid` 不是装饰。一个发布了五种架构、却把五种都推荐一遍的工具，等于什么都没告诉运维人员；
该字段被一致性契约要求非空，正是为了逼每个拓扑承认自己不擅长什么（RSK-001）。

## 3. 新角色

```go
// Objective 是自适应拓扑决定"下一步要做的那一件事"。
type Objective struct {
    Domain    tool.Domain // 由哪个证据域来回答它
    Statement string      // 要确立什么，一句话
}

// Strategist 依据目前所知决定下一批目标，并在不值得再做时明说停止。
// 与 Planner 不同：Planner 为读者写一份散文式计划；这份契约是结构化、
// 迭代且会终止的。
type Strategist struct {
    Round   int      // 从 0 开始；第 0 轮尚无所得
    Learned []string // 已执行目标返回了什么
}
func (Strategist) Role() Role { return RoleStrategist }
func (s Strategist) Step(ctx context.Context, st *State) (Outcome, error)
func (s Strategist) Objectives() []Objective // 由 Step 填充
```

当 strategist 判定应当停止时，`Step` 返回 `Outcome{Done: true}`。
拓扑把"Done 且目标列表为空"视为**已收敛** —— 这正是本拓扑存在理由所在的提前退出。

```go
// Executor 用其所属域的工具，恰好推进一个既定目标。
type Executor struct{ Objective Objective }
func (Executor) Role() Role { return RoleExecutor }

// Advocate 基于共享证据，为一个立场对抗其余备选立场做论证。
// 它不挑选自己的立场 —— 正是这一点让论证具有对抗性，而不只是"第二种意见"。
type Advocate struct {
    Position    core.Hypothesis
    Alternatives []core.Hypothesis
}
func (Advocate) Role() Role { return RoleAdvocate }

// Judge 对照证据裁决各 advocate 的论证。
type Judge struct{}
func (Judge) Role() Role { return RoleJudge }
```

`Judge` 与 `Critic` 的区别在于**它被给了什么**：critic 逐条独立地挑战假设；
judge 拿到的是围绕同一份证据的相互竞争的论证，必须从中偏向其一。
它的回复结构与 critic 相同，因此报告侧无需改动。

## 4. `plan-execute`

```
Planner（散文式计划，供读者阅读）
最多重复 maxRounds 轮：
    Strategist(round, learned) → objectives
    若无 objectives：break                    ← 提前退出
    对每个 objective：Executor → learned += 结果
Correlator → Critic → Reporter
```

- `maxRounds` 为 3。再多就不是在自适应，而是在游荡；本次运行的步数预算无论如何也会截断它 ——
  但一个拓扑应当自我设限，而不是靠被别人砍掉。
- 同一轮内的目标顺序执行。这正是要点：本拓扑用 supervisor 的并行性换来"改变主意"的能力，
  把一轮并发执行只会让它的轮次变得更粗。
- 每一轮的目标与结果都被记为笔记，因此这种自适应在报告里可见，而不只存在于转录中。

## 5. `debate`

```
Planner → Investigators（同 supervisor：分域并发）
Correlator → 假设
positions := 置信度最高的 N 条假设，N = min(3, 总数)
Advocates（每个立场一个，并发）→ 论证记为笔记
SortNotes（按立场排名确定性排序）
Judge → 逐条假设的状态与置信度
Reporter
```

- N 上限为 3。对 correlator 产出的每一条假设都办一场辩论，会按假设条数线性增加调用，
  收益却递减；排在第三之后的立场很少还有活力。
- 假设少于两条时无可辩论：本拓扑记录一条缺口说明这一点，并退回 `Critic` ——
  如实承认"辩论没有发生"，而不是硬演一场。

## 6. `blackboard`

控制组件是一份贡献者清单，每个贡献者带一个作用于状态的前置条件。
一轮会按清单顺序，把每个具备条件的贡献者各运行一次；
当某一轮什么都没改变、或达到 `maxRounds`（4）时，循环结束。

| 贡献者 | 具备条件当 | 贡献 |
|---|---|---|
| `Planner` | 尚无任何笔记 | 初始计划 |
| `Investigator{d}` | 域 `d` 有工具**且**尚未贡献过笔记 | 证据与笔记 |
| `Correlator` | 已有 ≥1 条笔记**且**证据自上次关联以来发生了变化 | 假设 |
| `Critic` | 存在 ≥1 条尚未被评估的假设 | 状态与置信度 |
| `Reporter` | 存在 ≥1 条假设**且**尚无摘要 | 摘要与建议 |

"自上次以来是否变化"用 `State` 已有的摘要指纹来度量（`EvidenceDigest`、
`PriorFindingsDigest`），因此无需新增状态（计划 D-2）。

终止性是谓词的性质，而不是计数器的性质：每个贡献者的前置条件都会被它自己的贡献所证否，
因此只要没有新证据出现，运行过贡献者的一轮必然缩小"具备条件"的集合 ——
而新证据只能来自 investigator，每个 investigator 至多运行一次。
`maxRounds` 是为将来某个打破该论证的贡献者准备的兜底，而不是终止机制本身。

## 7. 一致性契约

`internal/orchestrator/conformance_test.go`，在三个拓扑之前写就：

| 测试 | 性质 |
|---|---|
| `TestEveryRegisteredTopologyIsGoverned` | 契约表覆盖所有已注册名称 |
| `TestTopologyProducesHypothesisAndSummary` | HLD §2 第 2 条 |
| `TestTopologyAttributesEveryExchange` | 第 3 条 —— 每条被记录的 LLM 步骤都写明角色 |
| `TestTopologiesRespectStepBudget` | 第 4 条 —— 预算为 2 时截断并记录 |
| `TestTopologiesDegradeWithoutTools` | 第 5 条 —— 空注册表下仍能完成，并记录缺口 |
| `TestTopologiesAreDeterministic` | 第 6 条 —— 两次运行，假设与笔记完全一致 |
| `TestTopologyDescriptionsAreBilingualAndHonest` | 第 7 条 —— 四个字段、两种语言，且 `Avoid` 非空 |
| `TestBrokenTopologyFailsTheContract` | 契约自身 —— 一个跳过摘要的拓扑必须被判失败 |

`TestBrokenTopologyFailsTheContract` 是让其余测试可信的那一个。
它在子测试中注册一个刻意做坏的拓扑、对其运行契约，并断言契约确实**失败** ——
这样，一份已经悄悄什么都不检查的契约，就不可能悄无声息地通过。

## 8. 错误

不新增错误码。`MAS-3001`（未知拓扑）已覆盖选择环节；截断沿用今天的 `MAS-3005`；
立场不足的辩论记录为缺口而非错误 —— 因为该次运行依然是有效的。

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | 初版详细设计 | tasks、代码 |
