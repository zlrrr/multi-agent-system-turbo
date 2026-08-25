# Task Breakdown: Model Routing and Honest Cost Accounting

> **Feature ID**: `005-model-routing-and-cost` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.

## Phase A — cost becomes a type

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T401 | `core.Cost` with `Known`, `Unpriced`, `Add` and `String` | FR-005, CON-001 | `TestCostAddIsUnknownIfEitherIs` | — | done |
| T402 | Remove `Usage.CostUSD`; update every consumer the compiler names | FR-006 | Build green; nothing renders a bare zero | T401 | done |
| T403 | Report renders cost or says unpriced, in both languages | FR-006, NFR-004 | `TestUnpricedRunSaysSoInBothLanguages`, `TestNoRenderedReportContainsBareZeroCost` | T402 | done |
| **G-A** | **Gate A** | | An unpriced run says so; no path can print `$0.00` it was not given | | done |

## Phase B — pricing

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T410 | `llm.Pricing` and `CostOf`; configuration keys | FR-004, NFR-002, NFR-003 | `TestZeroPriceIsKnownAbsentPriceIsNot` | G-A | done |
| T411 | Cost accumulated per exchange from the model actually used | FR-004 | `TestCostIsComputedFromConfiguredPrices` | T410 | done |
| T412 | Partly priced runs report the priced part and name the rest | FR-009 | `TestPartiallyPricedRunIsExplicit` | T411 | done |

## Phase C — routing

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T420 | `llm.Router`, `Route`; named providers; inheritance | FR-001, NFR-002 | `TestPerRoleProviderIsUsed`, `TestRouteInheritsDefaults` | G-A | done |
| T421 | Providers opened once at admission and closed with the run | FR-002, FR-003 | `TestProvidersAreOpenedOnceAndClosed`, `TestUnopenableRoleProviderFailsAdmission` | T420 | done |
| T422 | Agent loop uses the router | FR-001, NFR-001 | Existing agent tests plus routing | T420 | done |
| T423 | Per-role provider credentials redacted like the default's | NFR-005 | Redaction test | T421 | done |

## Phase D — attribution

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T430 | `Counting` keyed by role; `ByRole()` | FR-007 | `TestUsageIsAttributedPerRole`, `TestAttributionSumsToTheTotal` | T411, T420 | done |
| T431 | Correct under topologies that run roles concurrently | NFR-006 | `TestAttributionUnderConcurrentTopology` under `-race` | T430 | done |
| T432 | Report and run record carry the breakdown and the routing | FR-008, FR-012 | `TestReportCarriesPerRoleBreakdown`, `TestRunRecordCarriesRouting` | T430 | done |
| **G-B** | **Gate B** | | A priced run reports cost per role; an unpriced one names what is missing | | done |

## Phase E — surface and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T440 | `mas models` prints the effective routing | FR-011 | `TestModelsCommandShowsEffectiveRouting` | G-B | done |
| T441 | `mas doctor` reports which models are priced | FR-010 | Doctor test | G-B | done |
| T442 | Bilingual documentation: manual, configuration reference, README | NFR-004 | `sddctl verify` parity | G-B | done |
| T443 | Demo prints cost per topology alongside calls | FR-008 | `make demo` output | G-B | done |
| **G-C** | **Gate C — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T401–T403 | `go test ./internal/core/... ./internal/report/...` |
| G-B | T410–T432 | `go test -race ./internal/llm/... ./internal/agent/... ./internal/orchestrator/...` |
| G-C | T440–T443 | `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial task breakdown | cost type, pricing, routing, attribution, docs |
