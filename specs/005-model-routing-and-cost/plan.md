# Implementation Plan: Model Routing and Honest Cost Accounting

> **Feature ID**: `005-model-routing-and-cost` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

Both halves are small in code and consequential in design, so the plan is mostly
about which shapes to choose.

**Routing** becomes a resolver: given the run's LLM configuration and a role, it
returns the provider *and* model that role uses. Today's `ModelFor` already does
half of this; extending it to return a provider makes the per-role override
uniform instead of "model and temperature are overridable but provider is not",
which is the kind of asymmetry nobody remembers.

**Cost** becomes a type rather than a float. `Usage.CostUSD float64` cannot
express "unknown", and every consumer of a float has to remember the `> 0`
convention. A `Cost` with an explicit `Known` flag makes the unknown case
impossible to render by accident, which is the entire requirement.

## 2. Design decisions

| ID | Decision | Rationale | Reversal condition |
|---|---|---|---|
| D-1 | `Cost{USD float64; Known bool; Unpriced []string}` replaces the bare float | A float has no way to say "not measured", so every renderer must remember a convention. One careless renderer prints `$0.00` for a run that was never priced, and an operator believes it | — |
| D-2 | Prices are configuration, and this project ships none | Prices change, differ by contract and region, and go stale silently. A number that looks authoritative and is wrong is worse than an admitted gap (CON-002) | — |
| D-3 | A partly priced run reports the priced part **and** names the unpriced models | Suppressing the whole figure would waste real information; showing it without the caveat would understate the run. Both halves are needed for the number to mean anything | — |
| D-4 | Routing resolves to a provider *instance*, opened once per run | Opening a provider per call would multiply connections and hide credential errors inside the run rather than at admission | — |
| D-5 | A per-role provider inherits the default's settings unless it overrides them | Otherwise every role that changes one field must restate the endpoint, the timeout and the key — and a forgotten field fails at the worst moment | — |
| D-6 | Per-role usage is accumulated by the counting wrapper, keyed by role | The accounting already exists in one place; keying it is a smaller change than threading totals through every role, and it cannot drift from the calls it counts | — |
| D-7 | `mas models` is a new listing command rather than a flag on `mas config` | Effective routing is derived, not configured: it is the answer to "what will actually happen", which is a different question from "what did I write" | — |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-001 | The `Cost` type change touches report, store and API, and a missed site silently reverts to printing zero | The float is *removed*, not deprecated, so every site fails to compile until it is updated; a test asserts no rendered report contains `$0.00` |
| RSK-002 | An operator prices a model wrongly and trusts the total | The report names the price basis; documentation states that prices are operator-supplied and the figure is only as good as they are |
| RSK-003 | Per-role providers multiply credentials in memory and in logs | Each is redacted by the same redactor at the same boundary; a test asserts a per-role key never appears in a step record |
| RSK-004 | Concurrent topologies mis-attribute usage across roles | Attribution is keyed inside the mutex that already guards the counter, and the concurrency test runs under `-race` |
| RSK-005 | Routing makes runs non-reproducible | The run record carries the effective routing, so a comparison can be repeated exactly (FR-012) |

## 4. Sequencing

| Phase | Content | Gate |
|---|---|---|
| A | `Cost` type; every consumer updated; unpriced never renders as a number | Report tests in both languages; no `$0.00` anywhere |
| B | Pricing configuration; cost computed; partial pricing named | FR-004, FR-005, FR-009 |
| C | Per-role provider routing; opened once; admission failure | FR-001…FR-003, FR-012 |
| D | Per-role accounting; report and run record | FR-007, FR-008, NFR-006 |
| E | `mas models`, doctor, bilingual docs | `make ci`, `sddctl verify` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial plan | HLD, LLD, tasks |
