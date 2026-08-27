# 详细设计（LLD）：回归基线与模型维度

> **特性 ID**：`008-regression-baselines` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

```
internal/eval/
  baseline.go     新增：Cell、Baseline、LoadBaseline、Save、逐字节稳定的编码
  compare.go      新增：Compare、Delta、Transition、基线闸门
  run.go          + Options 新增 Models；Matrix 叉乘 case × 拓扑 × 模型
  score.go        + Outcome.Model、Outcome.Class
  render.go       + RenderDelta、抽样限定声明
  baseline.json   面向内置语料库发布的基线
internal/cli/
  commands.go     `mas eval --baseline / --write-baseline / --models`
pkg/errs/
  registry.go     MAS-9105…MAS-9107
```

## 2. 格子及其类别

```go
// Class 是一个结果在基线所记录的词汇中所对应的类别。
type Class string

const (
    ClassHit       Class = "hit"
    ClassMiss      Class = "miss"        // 期望的模式未被得出
    ClassWrong     Class = "wrong"       // 得出了被排除的模式
    ClassGapMissed Class = "gap-missed"  // 期望的缺口未被声明
    ClassError     Class = "error"       // 本次运行失败
)
```

当两者同时成立时，`Wrong` 优先于 `Miss`：
一次既漏掉了答案、又得出了被排除结论的运行，是两者中更严重的那个，
而一个类别必须二选一。相应的 id 会一并记录，因此这个排序不会丢失任何信息。

```go
// Cell 是一组（case、拓扑、模型）的结果，按记录形式呈现。
type Cell struct {
    Case     string   `json:"case"`
    Topology string   `json:"topology"`
    Model    string   `json:"model"`
    Class    Class    `json:"class"`
    Missing  []string `json:"missing,omitempty"`
    False    []string `json:"false_conclusions,omitempty"`
    GapsMissed []string `json:"gaps_missed,omitempty"`
}

func (c Cell) Key() string { return c.Case + "|" + c.Topology + "|" + c.Model }
```

任何地方都没有计数。一个记录总数的基线，
会让"用漏判换错误结论"的改动比较起来像是"没有变化"——
而整个设计正是为了防住这个失败（design-hld.zh.md §2）。

## 3. 基线文件

```go
type Baseline struct {
    Version   int      `json:"version"`   // schema 版本，为 1
    Provider  string   `json:"provider"`
    Recorded  string   `json:"recorded"`  // RFC3339 日期，不含时间
    Corpus    int      `json:"corpus"`    // 记录时的 case 数量
    Cells     []Cell   `json:"cells"`
}
```

使用 `json.MarshalIndent` 编码，cells 按 `Key()` 排序。
这就是 FR-012：这个文件是作为 diff 来评审的，
而一份满是重新排序的 diff，是没有人会看的 diff。
`Recorded` 用日期而不是时间戳，理由相同 ——
内容没变的重写不应该产生任何 diff。

`Save` 只在被要求时写入（FR-002）。从普通的 `mas eval` 到写入之间没有任何路径；
该 flag 是唯一的调用方。

## 4. 比较

```go
type Transition string

const (
    Regressed      Transition = "regressed"
    Improved       Transition = "improved"
    KnownBad       Transition = "known-bad"
    ChangedFailure Transition = "changed-failure"
    New            Transition = "new"
    NotRun         Transition = "not-run"
)

type Change struct {
    Cell       Cell       `json:"cell"`
    Was        Class      `json:"was,omitempty"`
    Transition Transition `json:"transition"`
    Detail     string     `json:"detail,omitempty"`
}

type Delta struct {
    Changes  []Change `json:"changes"`
    Mismatch string   `json:"provider_mismatch,omitempty"`
    Caveats  []string `json:"caveats"`
}
```

`Compare(base Baseline, s Summary) Delta` 同时遍历两个按键索引的集合：

| 原来 | 现在 | 转移 |
|---|---|---|
| hit | hit | 略去 —— 一次没有变化的通过不是新闻 |
| hit | 非 hit | `Regressed` |
| 非 hit | hit | `Improved` |
| 非 hit | 非 hit，id 相同 | `KnownBad` |
| 非 hit | 非 hit，id 不同 | `ChangedFailure` |
| 不存在 | 存在 | `New` |
| 存在 | 不存在 | `NotRun` |

"id 相同"比较的是三个 id 列表的有序形式，而不只是类别：
一个原先漏掉某个模式、如今却得出了一个错误结论的格子确实动了，
尽管两者都属于"不是 hit"（design-hld.zh.md §3）。

`KnownBad` 每次运行都会输出，而不只在发生变化时输出。
一个不再可见的缺口，就是一个不再会被修的缺口（RSK-2）。

`Delta.Gate() error` 只在出现 `Regressed` 时失败，
并以 `MAS-9105` 携带数量与前几个键。其余一切都只报告，不阻断。

## 5. Provider 不匹配

基线会记录它被写入时所用的 provider。当本次运行的 provider 与之不同时，
`Delta.Mismatch` 会被设置，渲染器会把它打印在表格上方，并随 JSON 一同携带。

刻意不作为错误：把一次在某个模型下的运行与在另一个模型下记录的基线相比较，
恰恰是模型矩阵存在的意义。真正错误的是**悄悄地**这么做（D-6）。

## 6. 模型维度

```go
type Options struct {
    // …
    Models []string   // 为空表示只用 Options.LLM 中的那一个模型
}
```

`Matrix` 按 case × 拓扑 × 模型构建作业。每个作业复制 `o.LLM` 并设置 `Model`，
因此路由、定价与账本看到的都是真正跑起来的那个模型。
`Outcome.Model` 取自作业本身，而不是共享配置 ——
这个区别很重要：从共享配置读取会把每个格子的成本，
都归到"最后一次恰好被配置"的那个模型头上，看上去很权威，实际上是错的（RSK-4）。

顺序仍然是确定的：先 case，再拓扑，再模型。

## 7. 渲染

`RenderDelta(w, delta, lang)` 按顺序打印：

1. provider 不匹配（若有）；
2. 回归，以及每个格子失去了什么；
3. 改善；
4. 失败方式改变；
5. 已知为坏的格子，以列表形式；
6. 新增与未运行的格子；
7. 限定声明。

此处新增的限定声明是：**每个格子只是一个样本**。
在确定性 provider 下，那是一次测量；在真实模型下，它是一次抽样，
而两次抽样可以不同。它随 `Delta.Caveats` 一同传递，
使 JSON 消费者无法把它格式化掉 —— 与特性 006 的做法完全一致。

## 8. `mas eval`

```
mas eval --baseline internal/eval/baseline.json      # 比较，出现回归时失败
mas eval --write-baseline internal/eval/baseline.json # 记录（人的行为）
mas eval --models mock-1,mock-2 --matrix              # 完整矩阵
```

`--baseline` 会以基线闸门取代绝对闸门。两者都在，但只有一个决定退出码 ——
因为一次同时因两个原因失败的构建，关于这两个原因都教不了你什么。

## 9. 错误码

| 错误码 | 含义 |
|---|---|
| `MAS-9105` | 有格子相对基线发生回归 |
| `MAS-9106` | 基线文件无法读取，或它并不是一份基线 |
| `MAS-9107` | 基线记录的 provider 与本次运行所用的不同 |

`MAS-9107` 是以披露形式携带的警告，绝不是一个会中止运行的错误。

## 10. 测试

| 测试 | 它钉住了什么 |
|---|---|
| `TestBaselineRecordsEveryCell` | FR-001 |
| `TestBaselineIsNeverWrittenImplicitly` | FR-002、CON-003 |
| `TestRegressionsAndImprovementsAreReportedSeparately` | FR-003、CON-001 |
| `TestKnownBadCellDoesNotFailTheGate` | FR-004 |
| `TestChangedFailureIsReported` | FR-005 |
| `TestNewCellIsNotARegression` | FR-006 |
| `TestMissingCellIsReported` | FR-007 |
| `TestProviderMismatchIsDisclosed` | FR-008 |
| `TestModelAxisRunsEveryCell` | FR-009 |
| `TestPerCellAccountingIsAttributed` | FR-010、RSK-4 |
| `TestComparisonCarriesTheSamplingCaveat` | FR-011、CON-002 |
| `TestBaselineIsByteStableAcrossRuns` | FR-012 |
| `TestEvalBaselineCLI` | FR-013 |
| `TestShippedBaselineMatchesTheCorpus` | FR-014 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | 初版详细设计 | tasks、代码 |
