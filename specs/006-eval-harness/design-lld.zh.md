# 详细设计（LLD）：Case 语料库与评测框架

> **特性 ID**：`006-eval-harness` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

| 路径 | 内容 |
|---|---|
| `internal/eval/case.go` | `Case`、校验、`//go:embed cases/*.yaml` |
| `internal/eval/cases/*.yaml` | 内置语料库，每个知识包一个 |
| `internal/eval/stub.go` | 由 case 构建的桩 Prometheus 与 Loki 服务端 |
| `internal/eval/run.go` | `Runner`：case × 拓扑 → `Outcome` |
| `internal/eval/score.go` | `Score`、`Outcome`、`Summary` |
| `internal/eval/render.go` | 双语渲染，含各项限定声明 |
| `internal/cli/commands.go` | `mas eval` |
| `internal/eval/eval_test.go` | 框架自身的测试 |
| `pkg/errs/registry.go` | `MAS-9100`…`MAS-9103` |

## 2. case

```yaml
apiVersion: mas.turbo/v1
kind: DiagnosticCase
metadata:
  id: redis-maxmemory-eviction
  middleware: redis
  version: "7.2.4"
  title:       { en: "…", zh: "…" }
  description: { en: "发生了什么、以及为何这是答案", zh: "…" }

symptom: { en: "p99 latency spike with evictions", zh: "延迟毛刺伴随驱逐" }

telemetry:
  # 以子串匹配展开后的 PromQL，最长匹配优先；这样 case 无需重述知识包的确切查询 ——
  # 否则每一次信号改名都会让每一个 case 失败，而这在诊断意义上毫无理由。
  metrics:
    redis_memory_used_bytes: [940, 970, 990]
    redis_memory_max_bytes:  [1000]
  logs:
    - "OOM command not allowed when used memory > 'maxmemory'"
  withhold: [logs]        # 可选：本 case 对该次运行扣留的源

expect:
  failure_modes:     [memory-pressure]
  not_failure_modes: [replication-broken, persistence-failure]
  gaps:              ["MAS-4101"]     # 扣留某个源时必填
```

`withhold` 正是让 case 能够检验**诚实性**而不只是正确性的那个字段：
它拿走一个源，然后要求本次运行**说出**它缺失。
一个在没有日志的情况下悄悄得出同样结论的系统，即便结论正确，也会在该 case 上失败。

校验会拒绝这样的 case：所指中间件没有知识包；所指故障模式该知识包并未声明；
没有声明任何预期结果；或任何面向运维人员的字符串缺了某一种语言。

## 3. 桩遥测

```go
// stubTelemetry 依据一个 case 提供 Prometheus 与 Loki 的 HTTP API。
//
// 刻意使用真实服务端而非注入桩工具：最可能发生回归的那一层，
// 位于"信号的 PromQL"与"解析出的 series"之间 ——
// 查询构造、护栏裁定、采集器解码、引擎对空值的处理。
// 过去三个特性中发现的每一个缺陷都住在那里。
// 注入工具的框架会把这一切全部跳过（design-hld.zh.md §5）。
func stubTelemetry(c *Case) (*stubs, error)
```

匹配采用最长子串：以 `redis_memory_used_bytes` 为键的 case，
可以回答知识包的 `redis_memory_used_bytes{job="x"} / clamp_min(...)`。
匹配不到任何键的查询返回**空结果**而不是 0 ——
这既是如实的行为，也正是自特性 002 引擎修复之后会产生缺口的那种行为。

## 4. 运行

```go
type Options struct {
    Topology  string
    Language  string
    Provider  config.LLMConfig  // 默认 mock
    Mode      core.Mode
}

func (r *Runner) Run(ctx context.Context, c *Case, o Options) (Outcome, error)
func (r *Runner) Matrix(ctx context.Context, cases []*Case, topologies []string, o Options) (Summary, error)
```

运行器构建一个指向桩服务端的 `config.Config`，构造真实的 `service.Service`，
并调用 `Diagnose`。流水线中没有任何一环被替换。
各 case 之间并发（有上限），因为它们彼此独立，而语料库必须留在 NFR-001 的一分钟之内。

## 5. 打分

```go
type Outcome struct {
    Case, Topology string
    Concluded      []string // 本次运行得出的故障模式
    Missing        []string // 预期但未得出
    False          []string // 得出了、但被该 case 明确排除
    MissingGaps    []string // 未被声明的预期缺口码
    Usage          core.Usage
    Duration       time.Duration
    Err            error
}

func (o Outcome) Hit() bool // Missing、False 与 MissingGaps 全为空
```

四项事实，绝不合并。`Hit()` 是逻辑与而不是分数 ——
正是为了让"用漏判换错误结论"的改动无法看起来像进步；
而这恰是基于 LLM 的系统在被推着"更果断"时会做的那个交换（design-hld.zh.md §3）。

## 6. 渲染

```
语料库：6 个 case × 5 个拓扑 · 确定性 provider

CASE                          拓扑          结果     错误  缺口  调用    成本
redis-maxmemory-eviction      supervisor    命中        0    ok      8  $0.0084
kafka-under-replicated        debate        漏判        1    ok     11  $0.0116

supervisor    6 中命中 5 · 0 个错误结论
debate        6 中命中 4 · 1 个错误结论

本语料库是合成的：它度量的是与其自身标签的一致程度，而不是在真实故障上的准确率。
当前 provider 是 `mock`，它重放的脚本中本就写着答案 —— 这些结果对模型质量不说明任何事情。
```

各项限定由渲染器输出，而不是记在别处的文档里 ——
因为写在手册里的限定不会出现在截图上（计划 D-7）。
第二条仅在使用脚本化 provider 时打印；`--json` 会把两条都作为字段携带，
使集成方无法靠改格式把它们丢掉。

## 7. `mas eval`

```
mas eval                        # 内置语料库，默认拓扑
mas eval --matrix               # 全部拓扑
mas eval --cases ./my-cases     # 运维人员自己的语料库，与内置语料库一同运行
mas eval --topology debate      # 指定单一拓扑
mas eval --json
```

只要有任何 case 漏判或得出被排除的结论，退出码即非零 —— 这正是 CI 闸门之所以成为闸门。

`--cases` 指定的目录是在内置语料库**之外**追加读取的，因此运维人员自己的 case
不会悄悄取代回归基线。无法读取的路径会直接报错，而不是被跳过：
否则，一个写错的路径就会去跑内置语料库并报告成功。

## 8. 错误

| 错误码 | 含义 |
|---|---|
| `MAS-9100` | case 格式错误 |
| `MAS-9101` | case 所指故障模式其知识包并未声明 |
| `MAS-9102` | case 所属中间件没有知识包 |
| `MAS-9103` | 语料库出现回归：至少有一个 case 漏判或得出了被排除的模式 |
| `MAS-9104` | `--cases` 指定的目录无法读取 |

## 9. 测试

| 测试 | 性质 |
|---|---|
| `TestCorpusLoadsFromDirectory` | FR-001 —— case 是数据 |
| `TestCaseSchemaRequiresAnExpectedOutcome` | FR-002 |
| `TestCaseNamingAnUndeclaredModeIsRefused` | §2 —— case 不能断言任何知识包都得不出的东西 |
| `TestHarnessUsesTheRealPipeline` | FR-003 —— 桩服务端确被访问、护栏确有运行 |
| `TestScoringUsesNoTextSimilarity` | FR-004 —— 结构性：打分不读取任何散文字段 |
| `TestFalseConclusionIsScoredSeparately` | FR-005、design-hld.zh.md §3 |
| `TestWithheldSourceMustProduceADeclaredGap` | FR-006 |
| `TestMatrixRunsEveryCaseAgainstEveryTopology` | FR-007 |
| `TestResultsAreDeterministic` | FR-008 |
| `TestMockRunRefusesToClaimModelQuality` | FR-009、CON-001 |
| `TestReportKeepsOutcomesSeparate` | FR-010、CON-002 |
| `TestRenderedResultAlwaysCarriesTheCaveats` | NFR-005，两种语言 |
| `TestEveryPackHasACase` | FR-013 |
| `TestCorpusRunsInsideTheCIBudget` | NFR-001 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | 初版详细设计 | tasks、代码 |
