# 详细设计（LLD）：模型路由与如实的成本核算

> **特性 ID**：`005-model-routing-and-cost` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

## 1. 文件

| 路径 | 内容 |
|---|---|
| `internal/core/model.go` | `Cost`；以 `Usage.Cost` 取代 `Usage.CostUSD`；`RoleUsage` |
| `internal/llm/llm.go` | `Router`、`Route`；以角色为键的 `Counting` |
| `internal/llm/pricing.go` | `Pricing`，由 token 计算成本 |
| `internal/config/config.go` | `LLMConfig.Providers`、`LLMConfig.Pricing`；`AgentModel.Provider` |
| `internal/agent/loop.go` | 使用 router；在每次交互上记录角色 |
| `internal/report/report.go` | 渲染成本或写明未定价；按角色的表格 |
| `internal/cli/commands.go` | `mas models` |
| `internal/service/{service,doctor}.go` | 在准入阶段打开各路由；doctor 报告定价情况 |

## 2. 成本

```go
// Cost 是一次运行花了多少，或者一句"没人知道"的如实陈述。
//
// 它是类型而不是浮点数，因为浮点数没有任何取值能表示"未被测量"：
// 0 是一个真实的成本 —— mock provider 确实不花钱 ——
// 因此渲染器在没有约定的情况下无法区分"免费"与"未定价"，
// 而约定正是维护者会不经意间改掉的东西。
type Cost struct {
    USD      float64  `json:"usd"`
    Known    bool     `json:"known"`
    Unpriced []string `json:"unpriced,omitempty"` // 没有配置价格的模型
}

func (c Cost) Add(o Cost) Cost   // 两者都已知才算已知；Unpriced 取并集并排序
func (c Cost) String() string    // "$0.0412"，或 "unpriced (claude-opus-5)"
```

`Usage.CostUSD` 是被**移除**，而不是被弃用。这样每一个消费方都会编译失败直到被更新 ——
这是唯一可靠的办法，能确保没有任何一处仍在打印一个它从未拿到过的 0（计划 RSK-001）。

`Add` 是微妙的那一个：一次运行为两个模型定了价、第三个没有，那么它**不是**已知。
把已定价部分的和当作总额来报告会低估这次运行，
因此 `Known` 是逻辑与，未定价的名字则随数字一起传递。

## 3. 定价

```yaml
llm:
  pricing:
    claude-opus-5:  { input_per_mtok: 5.00, output_per_mtok: 25.00 }
    qwen2.5:14b:    { input_per_mtok: 0,    output_per_mtok: 0 }   # 自建
```

```go
type Pricing map[string]ModelPrice
type ModelPrice struct{ InputPerMTok, OutputPerMTok float64 }

// CostOf 为一次交互计价。表中不存在的模型会得到一个点名它的未知成本 ——
// 而绝不是 0，因为 0 读起来就是"免费"。
func (p Pricing) CostOf(model string, in, out int) core.Cost
```

价格恰好为 0 是合法的，且仍然是 `Known`：自建模型在边际上确实免费，
而写下 `0` 的运维人员是有意这么说的。**缺失**才是未知的那种情况。
这一区分正是该类型存在的全部意义。

## 4. 路由

```go
// Route 是某个角色的工作被送往何处。
type Route struct {
    Name        string // 具名 provider，或 "default"
    Provider    Provider
    Model       string
    Temperature float64
}

// Router 把角色解析为路由，并持有它所打开的那些 provider。
type Router struct{ … }

func NewRouter(cfg config.LLMConfig) (*Router, error) // 每个不同 provider 只打开一次
func (r *Router) For(role string) Route
func (r *Router) Routes() map[string]Route            // 生效路由，供 `mas models` 使用
func (r *Router) Close() error
```

解析顺序：角色的 `per_agent` 条目指定了 provider → 采用该具名 provider 的配置，
再叠加该角色自己的模型/温度覆盖；否则采用默认 provider 加该角色的覆盖。
具名 provider 会继承默认配置中它没有设置的每一个字段（`api_key`、`timeout`、`max_tokens`），
因为一个只改了某个字段的角色不应悄无声息地丢掉其余字段（HLD §3）。

`NewRouter` 在构造时就打开各 provider，因此错误的凭据是一次准入失败
（`MAS-2001`/`MAS-2002`），而不是运行到一半才发现的一条缺口。

## 5. 归属

```go
type RoleUsage struct {
    Role     string `json:"role"`
    Provider string `json:"provider"`
    Model    string `json:"model"`
    Calls    int    `json:"calls"`
    PromptTokens, CompletionTokens int `json:"prompt_tokens","completion_tokens"`
    WallMillis int64 `json:"wall_millis"`
    Cost     Cost   `json:"cost"`
}
```

`Counting.Complete` 从请求中取出角色，并在本就保护总量的那把锁内累加进一个 map，
因此明细不可能与它所构成的总和相矛盾。`Totals()` 签名不变；新增 `ByRole()`。

排序按成本降序、再按调用数降序、再按角色名 —— 全序且确定，
因此同一案例的两次运行会产出同一张表（NFR-001）。

## 6. 报告

```
| 成本 | $0.0412 |                        ← 已知
| 成本 | 未定价（claude-opus-5） |          ← 未知，并给出原因
| 成本 | $0.0031 · 1 个模型未定价 |          ← 部分定价
```

当角色多于一个时，再加一张按角色的表。渲染器中没有任何分支能为未知成本输出一个裸数字 ——
因为 `Cost.String()` 是唯一路径，而它不存在这种输出。

## 7. `mas models`

打印生效路由 —— 角色、provider、模型、温度、是否已定价 ——
它回答的是"实际会发生什么"，这与 `mas config` 的"我写了什么"是两个问题。

## 8. 错误

不新增错误码。无法打开的按角色 provider 沿用既有的 provider 错误码
（`MAS-2001`、`MAS-2002`、`MAS-2005`）；未定价的模型根本不是错误，而是一个被写明的未知。

## 9. 测试

| 测试 | 性质 |
|---|---|
| `TestCostAddIsUnknownIfEitherIs` | §2 —— 那个逻辑与，低估正是藏在这里 |
| `TestZeroPriceIsKnownAbsentPriceIsNot` | §3 —— 该类型存在的那个区分 |
| `TestUnpricedRunSaysSoInBothLanguages` | FR-006、CON-001 |
| `TestNoRenderedReportContainsBareZeroCost` | RSK-001，覆盖所有报告路径 |
| `TestPerRoleProviderIsUsed`、`TestRouteInheritsDefaults` | FR-001、§4 |
| `TestProvidersAreOpenedOnceAndClosed` | FR-002 |
| `TestUnopenableRoleProviderFailsAdmission` | FR-003 |
| `TestUsageIsAttributedPerRole` | FR-007 |
| `TestAttributionSumsToTheTotal` | §5 —— 明细不可能漂移 |
| `TestAttributionUnderConcurrentTopology` | NFR-006、`-race` |
| `TestReportCarriesPerRoleBreakdown`、`TestRunRecordCarriesRouting` | FR-008、FR-012 |
| `TestModelsCommandShowsEffectiveRouting` | FR-011 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | 初版详细设计 | tasks、代码 |
