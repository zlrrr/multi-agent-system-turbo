# Feature Specification: Model Routing and Honest Cost Accounting

> **Feature ID**: `005-model-routing-and-cost` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.3
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

Two M2 items remain, and they are the same problem seen from either end.

**Routing is half-built.** `llm.per_agent` already overrides the *model* and
*temperature* per role, and the agent loop honours it. What it cannot do is
change the **provider**: an operator who wants extraction on a local
OpenAI-compatible endpoint and correlation on Anthropic has no way to say so,
even though the provider interface was built to make exactly that substitutable.
The seam exists; nothing reaches it.

**Cost is plumbed but never measured.** `Usage.CostUSD` runs from the provider
through the run record to the report — and no provider ever sets it, so it is
always zero. Today that is hidden by a `> 0` guard in the renderer, so the report
omits the row rather than printing `$0.0000`. That is honest by accident, and it
is one careless edit away from being dishonest: a report claiming a run cost
nothing is worse than one that says nothing, because an operator will believe it.

The two connect. Feature 003 made topologies comparable and the demo prints what
each one cost — in model calls and tool calls, because those are the only numbers
that are real. Routing is the lever that changes cost; cost is how you tell
whether pulling the lever helped. Shipping either alone leaves the other
unusable.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| Platform engineer | Run extraction on a cheap local model and judgement on a strong hosted one | Cost review |
| SRE | See what a diagnosis cost, and which role spent it | After an expensive run |
| Researcher | Compare topologies on cost as well as conclusion | Choosing a default |
| Operator with no price list | Not be told a run cost `$0.00` when nothing was priced | Any run |

## 3. Scope

### In scope
- Per-role **provider** selection, alongside the existing model and temperature.
- Model pricing as configuration, and cost computed from it.
- An explicit **unpriced** state that is never rendered as zero, anywhere.
- Per-role accounting: calls, tokens, wall time and cost, attributed to the role
  that spent them, in the report and the run record.
- Bilingual documentation, including how to price a model and what happens when
  you do not.

### Out of scope
- Shipping a price list. Prices change, vary by contract and region, and a stale
  hard-coded number that looks authoritative is worse than no number.
- Budget enforcement in currency. The run already has token, step and wall-clock
  budgets; a cost ceiling computed from operator-supplied prices would be a
  safety control resting on unvalidated input.
- Automatic model selection. Choosing a model by predicted difficulty is an
  optimisation this project has no corpus to evaluate (M3).

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | A role MUST be able to name a provider different from the run's default, with its own credentials and endpoint | P0 | `TestPerRoleProviderIsUsed` |
| FR-002 | Every provider a run needs MUST be opened once and reused, and closed when the run ends | P0 | `TestProvidersAreOpenedOnceAndClosed` |
| FR-003 | A per-role provider that cannot be opened MUST fail admission with a code, before any work begins | P0 | `TestUnopenableRoleProviderFailsAdmission` |
| FR-004 | Cost MUST be computed from configured per-model prices | P0 | `TestCostIsComputedFromConfiguredPrices` |
| FR-005 | A model with no configured price MUST make the run's cost **unknown**, never zero | P0 | `TestUnpricedModelMakesCostUnknown` |
| FR-006 | A report MUST NOT display a cost figure for a run whose cost is unknown; it MUST say it is unpriced | P0 | `TestUnpricedRunSaysSoInBothLanguages` |
| FR-007 | Usage MUST be attributed per role: calls, prompt and completion tokens, wall time, and cost | P0 | `TestUsageIsAttributedPerRole` |
| FR-008 | The report and the run record MUST carry the per-role breakdown | P0 | `TestReportCarriesPerRoleBreakdown` |
| FR-009 | Partly priced runs MUST report the priced portion and name what was not priced | P1 | `TestPartiallyPricedRunIsExplicit` |
| FR-010 | `mas doctor` MUST report which models are priced and which are not | P1 | Doctor test |
| FR-011 | `mas models` MUST list the effective routing: which provider and model each role will use | P1 | CLI test |
| FR-012 | Per-role routing MUST be visible in the run record, so a comparison can be reproduced | P1 | `TestRunRecordCarriesRouting` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | Determinism is unaffected: identical input and routing produce an identical report | Existing determinism tests, plus routing |
| NFR-002 | No new module dependency | `go.mod` unchanged |
| NFR-003 | Prices are data, not code: adding one needs no rebuild | Configuration test |
| NFR-004 | Bilingual parity for every operator-facing string | `sddctl verify` |
| NFR-005 | Credentials for a per-role provider are redacted exactly as the default provider's are | Redaction test |
| NFR-006 | Accounting MUST be correct under the topologies that run roles concurrently | `-race`, concurrent topology test |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | An unknown cost is never rendered as a number | Constitution Art. V, honest reporting |
| CON-002 | This project ships no price list | Art. IX: a stale authoritative-looking number is a false claim |
| CON-003 | Routing may not change what a role is permitted to do | Art. IV: the guard is unaffected by which model runs |
| CON-004 | Cost must never gate safety | Out of scope above |

## 7. Acceptance

The feature is done when a role can be routed to a different provider, a priced
run reports its cost per role, an unpriced run says so in both languages instead
of showing zero, `mas models` shows the effective routing, and `make ci` is green.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial specification | plan, HLD, LLD, tasks, code |
