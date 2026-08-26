# Feature Specification: Regression Baselines and the Model Axis

> **Feature ID**: `008-regression-baselines` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.7
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

Feature 006 gave the project a corpus and a gate. The gate is absolute: `mas
eval` fails when any case misses or reaches a ruled-out conclusion. That is the
right bar for a corpus everything currently passes, and it answers only one
question — "is everything still green?"

It cannot answer the question anyone actually asks after a change: **what
moved?** A pack edit that fixes two cases and breaks one shows up as "the corpus
regressed", with no way to see the trade. A prompt change that costs a hit under
`debate` and gains one under `single` shows up as red, and the person who made
it has to run the matrix twice and diff two terminals by eye. Worst of all, once
a case legitimately cannot pass — a knowledge gap nobody has time to close — the
only way to keep CI green is to delete the case, which is exactly the wrong
incentive.

The second half is the model axis. G7.3 asks for regression scoring across a
**model/topology** matrix, and today only the topology axis exists: `--matrix`
varies the topology and holds the model fixed. An operator choosing between a
cheap model and a strong one on their own cases has to script the sweep
themselves, and loses the shared accounting — cost per cell, calls per cell —
that makes the comparison worth anything.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| Pack author | See what a knowledge edit fixed and what it broke, separately | Any pack change |
| Maintainer | Fail CI on a cell that got worse, not on a cell that was already known-bad | Every build |
| Platform engineer | Compare two models on the same cases, with cost beside the result | Choosing a default model |
| Researcher | Have a recorded, diffable artefact of how the system performed | Reporting a result |

## 3. Scope

### In scope
- A **baseline**: a recorded outcome per (case, topology, model) cell.
- Comparison of a run against a baseline, reporting **regressions and
  improvements separately**.
- A gate that fails on regression relative to the baseline, so a known-bad cell
  stays visible instead of being deleted.
- Writing and updating a baseline as an explicit, reviewable act.
- The **model axis**: `--models` runs each named model across the same cases and
  topologies, with per-cell accounting.
- A statement, wherever a comparison is shown, of what a single sample per cell
  can and cannot support.

### Out of scope
- Statistical significance. One sample per cell is what this runs; claiming a
  confidence interval from it would be the kind of number this project refuses
  to print.
- Automatic baseline updates. A baseline that updates itself records whatever
  happened and can never fail.
- Shipping a baseline for a non-deterministic provider. The repository's
  baseline is recorded under the deterministic provider, because a committed
  file that changes on every run is not a baseline.
- Tuning anything against the baseline. The harness measures; it does not
  optimise (feature 006, CON-005).

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | A baseline MUST record one outcome per (case, topology, model) cell, plus the provider it was recorded under | P0 | `TestBaselineRecordsEveryCell` |
| FR-002 | A baseline MUST be written only when explicitly asked for | P0 | `TestBaselineIsNeverWrittenImplicitly` |
| FR-003 | Comparison MUST report regressions and improvements separately, never netted | P0 | `TestRegressionsAndImprovementsAreReportedSeparately` |
| FR-004 | A cell that was failing and still fails the same way MUST NOT fail the gate | P0 | `TestKnownBadCellDoesNotFailTheGate` |
| FR-005 | A cell that was failing and now fails *differently* MUST be reported | P1 | `TestChangedFailureIsReported` |
| FR-006 | A cell present in the run but not the baseline MUST be reported as new, not as a regression | P0 | `TestNewCellIsNotARegression` |
| FR-007 | A cell present in the baseline but not the run MUST be reported as not run | P0 | `TestMissingCellIsReported` |
| FR-008 | Comparison against a baseline recorded under a different provider or model MUST say so wherever the result is shown | P0 | `TestProviderMismatchIsDisclosed` |
| FR-009 | `--models` MUST run every named model across every case and topology | P0 | `TestModelAxisRunsEveryCell` |
| FR-010 | Per-cell accounting — calls, tokens, cost — MUST be attributed to the model that spent it | P0 | `TestPerCellAccountingIsAttributed` |
| FR-011 | The comparison MUST state what one sample per cell supports | P0 | `TestComparisonCarriesTheSamplingCaveat` |
| FR-012 | The baseline file MUST be stable across runs so a diff shows only real change | P0 | `TestBaselineIsByteStableAcrossRuns` |
| FR-013 | `mas eval --baseline`, `--write-baseline`, `--models` on the CLI, in both languages | P1 | `TestEvalBaselineCLI` |
| FR-014 | The repository MUST ship a baseline for its own corpus, and CI MUST compare against it | P1 | `TestShippedBaselineMatchesTheCorpus` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | Comparison adds no run time: it is arithmetic over outcomes already produced | Timed test |
| NFR-002 | No new module dependency | `go.mod` unchanged |
| NFR-003 | Every operator-facing string bilingual | `sddctl verify` |
| NFR-004 | The shipped baseline stays inside the CI budget feature 006 set | `TestCorpusRunsInsideTheCIBudget` |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | No collapsed score, in the baseline or the comparison | Feature 006 CON-002 |
| CON-002 | A measurement is never presented as more than it is | Constitution Art. IX |
| CON-003 | A baseline is written by a person, never by a run | §3, FR-002 |
| CON-004 | Both languages for every message | Art. III |

## 7. Acceptance

The feature is done when a run can be compared against a recorded baseline,
regressions and improvements are reported side by side and never netted, a
known-bad cell keeps CI green while staying visible, a model axis runs with
per-cell cost, every comparison states what one sample supports, and the
repository's own baseline is committed and enforced by CI.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial specification | plan, HLD, LLD, tasks, code |
