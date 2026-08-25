# Low-Level Design (LLD): Model Routing and Honest Cost Accounting

> **Feature ID**: `005-model-routing-and-cost` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

| Path | Content |
|---|---|
| `internal/core/model.go` | `Cost`; `Usage.Cost` replaces `Usage.CostUSD`; `RoleUsage` |
| `internal/llm/llm.go` | `Router`, `Route`; `Counting` keyed by role |
| `internal/llm/pricing.go` | `Pricing`, cost from tokens |
| `internal/config/config.go` | `LLMConfig.Providers`, `LLMConfig.Pricing`; `AgentModel.Provider` |
| `internal/agent/loop.go` | Uses the router; records the role on every exchange |
| `internal/report/report.go` | Renders cost or says it is unpriced; per-role table |
| `internal/cli/commands.go` | `mas models` |
| `internal/service/{service,doctor}.go` | Opens routes at admission; doctor reports pricing |

## 2. Cost

```go
// Cost is what a run spent, or an honest statement that nobody knows.
//
// It is a type rather than a float because a float has no value meaning "not
// measured": zero is a real cost — the mock provider genuinely costs nothing —
// so a renderer cannot tell "free" from "unpriced" without a convention, and a
// convention is what a maintainer edits away without noticing.
type Cost struct {
    USD      float64  `json:"usd"`
    Known    bool     `json:"known"`
    Unpriced []string `json:"unpriced,omitempty"` // models with no configured price
}

func (c Cost) Add(o Cost) Cost   // Known only if both are; Unpriced unions, sorted
func (c Cost) String() string    // "$0.0412", or "unpriced (claude-opus-5)"
```

`Usage.CostUSD` is **removed**, not deprecated. Every consumer then fails to
compile until it is updated, which is the only reliable way to be sure none of
them still prints a zero it was never given (plan RSK-001).

`Add` is the subtle one: a run that priced two models and not a third is *not*
known. Reporting the sum of the priced part as though it were the total would
understate the run, so `Known` is a conjunction and the unpriced names travel
with the figure.

## 3. Pricing

```yaml
llm:
  pricing:
    claude-opus-5:  { input_per_mtok: 5.00, output_per_mtok: 25.00 }
    qwen2.5:14b:    { input_per_mtok: 0,    output_per_mtok: 0 }   # self-hosted
```

```go
type Pricing map[string]ModelPrice
type ModelPrice struct{ InputPerMTok, OutputPerMTok float64 }

// CostOf prices one exchange. A model absent from the table yields an unknown
// cost naming it — never zero, which would read as free.
func (p Pricing) CostOf(model string, in, out int) core.Cost
```

A price of exactly zero is legitimate and stays `Known`: a self-hosted model
really is free at the margin, and an operator who wrote `0` said so deliberately.
Absence is the unknown case. That distinction is the whole point of the type.

## 4. Routing

```go
// Route is where one role's work goes.
type Route struct {
    Name        string // the named provider, or "default"
    Provider    Provider
    Model       string
    Temperature float64
}

// Router resolves a role to its route and owns the providers it opened.
type Router struct{ … }

func NewRouter(cfg config.LLMConfig) (*Router, error) // opens every distinct provider once
func (r *Router) For(role string) Route
func (r *Router) Routes() map[string]Route            // effective routing, for `mas models`
func (r *Router) Close() error
```

Resolution, in order: the role's `per_agent` entry names a provider → that named
provider's settings, with the role's own model/temperature overrides on top;
otherwise the default provider with the role's overrides. A named provider
inherits every field of the default it does not set (`api_key`, `timeout`,
`max_tokens`), because a role that changes one field must not silently lose the
others (HLD §3).

`NewRouter` opens providers at construction, so a bad credential is an admission
failure (`MAS-2001`/`MAS-2002`) rather than a gap discovered mid-run.

## 5. Attribution

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

`Counting.Complete` takes the role from the request and accumulates into a map
under the mutex that already guards the total, so the breakdown cannot disagree
with the sum it forms. `Totals()` keeps its signature; `ByRole()` is new.

Ordering is by descending cost, then descending calls, then role name — total
and deterministic, so two runs of the same case produce the same table
(NFR-001).

## 6. Report

```
| Cost | $0.0412 |                       ← Known
| Cost | not priced (claude-opus-5) |     ← Unknown, with the reason
| Cost | $0.0031 · 1 model not priced |   ← partly priced
```

Plus a per-role table when there is more than one role. The renderer has no
branch that can emit a bare number for an unknown cost, because `Cost.String()`
is the only path and it has no such output.

## 7. `mas models`

Prints the effective routing — role, provider, model, temperature, priced or not
— which is the answer to "what will actually happen", a different question from
`mas config`'s "what did I write".

## 8. Errors

No new codes. A per-role provider that cannot be opened fails with the provider
codes that already exist (`MAS-2001`, `MAS-2002`, `MAS-2005`); an unpriced model
is not an error at all, it is a stated unknown.

## 9. Tests

| Test | Property |
|---|---|
| `TestCostAddIsUnknownIfEitherIs` | §2 — the conjunction, which is where an understatement would hide |
| `TestZeroPriceIsKnownAbsentPriceIsNot` | §3 — the distinction the type exists for |
| `TestUnpricedRunSaysSoInBothLanguages` | FR-006, CON-001 |
| `TestNoRenderedReportContainsBareZeroCost` | RSK-001, across every report path |
| `TestPerRoleProviderIsUsed`, `TestRouteInheritsDefaults` | FR-001, §4 |
| `TestProvidersAreOpenedOnceAndClosed` | FR-002 |
| `TestUnopenableRoleProviderFailsAdmission` | FR-003 |
| `TestUsageIsAttributedPerRole` | FR-007 |
| `TestAttributionSumsToTheTotal` | §5 — the breakdown cannot drift |
| `TestAttributionUnderConcurrentTopology` | NFR-006, `-race` |
| `TestReportCarriesPerRoleBreakdown`, `TestRunRecordCarriesRouting` | FR-008, FR-012 |
| `TestModelsCommandShowsEffectiveRouting` | FR-011 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial low-level design | tasks, code |
