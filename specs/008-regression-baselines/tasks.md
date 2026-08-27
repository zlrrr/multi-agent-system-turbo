# Task Breakdown: Regression Baselines and the Model Axis

> **Feature ID**: `008-regression-baselines` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.
Every test named here must exist: `sddctl verify` checks it.

## Phase A — the record

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T701 | `Class`, `Cell`, `Outcome.Class`, with `wrong` outranking `miss` | FR-001, CON-001 | `TestBaselineRecordsEveryCell` | — | done |
| T702 | `Baseline`, `LoadBaseline`, `Save`, key-sorted and byte-stable | FR-001, FR-012 | `TestBaselineIsByteStableAcrossRuns` | T701 | done |
| T703 | Nothing writes a baseline but the flag that asks for one | FR-002, CON-003 | `TestBaselineIsNeverWrittenImplicitly` | T702 | done |
| T704 | Error codes `MAS-9105`…`MAS-9107`, bilingual, docs regenerated | CON-004 | `mas errcodes` output current | T702 | done |
| **G-A** | **Gate A** | | A baseline round-trips unchanged | | done |

## Phase B — comparison

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T710 | `Compare`: the six transitions, nothing netted | FR-003, CON-001 | `TestRegressionsAndImprovementsAreReportedSeparately` | G-A | done |
| T711 | A repeat of the same failure is known-bad, and passes the gate | FR-004 | `TestKnownBadCellDoesNotFailTheGate` | T710 | done |
| T712 | A failure with different ids is a changed failure | FR-005 | `TestChangedFailureIsReported` | T711 | done |
| T713 | A cell absent from the baseline is new, not a regression | FR-006 | `TestNewCellIsNotARegression` | T710 | done |
| T714 | A cell absent from the run is reported as not run | FR-007 | `TestMissingCellIsReported` | T710 | done |
| T715 | A provider mismatch is disclosed wherever the result is shown | FR-008 | `TestProviderMismatchIsDisclosed` | T710 | done |
| **G-B** | **Gate B** | | `go test ./internal/eval/...` | | done |

## Phase C — the model axis

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T720 | `Options.Models`; `Matrix` crosses cases × topologies × models | FR-009 | `TestModelAxisRunsEveryCell` | G-B | done |
| T721 | Per-cell accounting attributed to the model that ran it | FR-010, NFR-001 | `TestPerCellAccountingIsAttributed` | T720 | done |
| **G-C** | **Gate C** | | `go test ./internal/eval/...` | | done |

## Phase D — surface, corpus and CI

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T730 | `RenderDelta` and the one-sample caveat, both languages and JSON | FR-011, CON-002 | `TestComparisonCarriesTheSamplingCaveat` | G-C | done |
| T731 | `mas eval --baseline / --write-baseline / --models` | FR-013 | `TestEvalBaselineCLI` | T730 | done |
| T732 | A committed baseline for the shipped corpus, enforced by CI | FR-014, NFR-004 | `TestShippedBaselineMatchesTheCorpus` | T731 | done |
| T733 | Bilingual documentation: evaluation guide, manual, README | NFR-003, NFR-002 | `sddctl verify` parity; `go.mod` unchanged | T732 | done |
| **G-D** | **Gate D — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T701–T704 | `go test ./internal/eval/...` |
| G-B | T710–T715 | `go test ./internal/eval/...` |
| G-C | T720–T721 | `go test ./internal/eval/...` |
| G-D | T730–T733 | `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial task breakdown | code, baseline, docs |
