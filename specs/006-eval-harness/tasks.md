# Task Breakdown: Case Corpus and Evaluation Harness

> **Feature ID**: `006-eval-harness` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.
Every test named here must exist: `sddctl verify` checks it.

## Phase A — the case

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T501 | `Case` schema, bilingual validation, embedded corpus | FR-001, FR-002, CON-004, NFR-003 | `TestCorpusLoadsFromDirectory`, `TestCaseSchemaRequiresAnExpectedOutcome` | — | done |
| T502 | A case may not name a mode its pack does not declare | FR-002 | `TestCaseNamingAnUndeclaredModeIsRefused` | T501 | done |
| T503 | Error codes `MAS-9100`…`MAS-9104`, bilingual, docs regenerated | NFR-003 | `mas errcodes` output current | T501 | done |
| **G-A** | **Gate A** | | A case loads; one with no expected outcome is refused | | done |

## Phase B — the runner

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T510 | Stub Prometheus and Loki servers built from a case, on `net/http/httptest` with no new dependency | FR-003, NFR-002, NFR-004 | Used by every test below | G-A | done |
| T511 | Runner over the real service: no substituted pipeline | FR-003 | `TestHarnessUsesTheRealPipeline` | T510 | done |
| T512 | An unmatched query returns an empty result, not zero | FR-003 | `TestUnmatchedQueryReturnsEmptyNotZero` | T510 | done |
| **G-B** | **Gate B** | | A case reaches a conclusion through the real pipeline | | done |

## Phase C — scoring

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T520 | `Outcome`: concluded, missing, false, missing gaps | FR-004, FR-005 | `TestFalseConclusionIsScoredSeparately` | G-B | done |
| T521 | Scoring reads no prose field | FR-004, CON-002 | `TestScoringUsesNoTextSimilarity` | T520 | done |
| T522 | A withheld source must produce a declared gap | FR-006 | `TestWithheldSourceMustProduceADeclaredGap` | T520 | done |
| T523 | Determinism across repeated runs | FR-008, NFR-001 | `TestResultsAreDeterministic` | T520 | done |

## Phase D — matrix, rendering and the caveats

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T530 | Matrix: every case × every selected topology, bounded concurrency | FR-007 | `TestMatrixRunsEveryCaseAgainstEveryTopology` | T523 | done |
| T531 | Rendering keeps outcomes separate; no collapsed score | FR-010, CON-002 | `TestReportKeepsOutcomesSeparate` | T530 | done |
| T532 | Caveats emitted by the renderer, both languages, JSON too | NFR-005, CON-003, CON-001 | `TestRenderedResultAlwaysCarriesTheCaveats` | T531 | done |
| T533 | A scripted provider refuses to present agent results as model quality | FR-009, CON-001 | `TestMockRunRefusesToClaimModelQuality` | T532 | done |
| T534 | `mas eval`, `--matrix`, `--cases`, `--topology`, `--json`, non-zero exit on regression | FR-011, FR-012 | `TestEvalCommand`, `TestEvalExitsNonZeroOnRegression` | T532 | done |

## Phase E — the corpus and the gate

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T540 | One shipped case per pack | FR-013 | `TestEveryPackHasACase` | T534 | done |
| T541 | The corpus passes, and stays inside the CI budget | FR-012, NFR-001 | `TestCorpusRunsInsideTheCIBudget` | T540 | done |
| T542 | CI runs the corpus and fails on regression | FR-012 | `make ci` includes it | T541 | done |
| T543 | Bilingual documentation: manual, README, case-authoring guidance | NFR-003 | `sddctl verify` parity | T541 | done |
| T544 | Fix what the corpus found: a source that is down is a gap under every topology | FR-006 | `TestDownSourceIsAGapUnderEveryTopology` | T541 | done |
| **G-C** | **Gate C — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T501–T503 | `go test ./internal/eval/...` |
| G-B | T510–T512 | `go test ./internal/eval/...` |
| G-C | T520–T544 | `make ci` |

## What the corpus found

T544 is not a task anyone planned. The corpus ran the same incident through five
topologies with the log source returning 503, and `supervisor` declared the
missing logs while `single` did not: a gap was only recorded when some caller's
query happened to fail, which made "the logs were unavailable" a fact about
control flow rather than about the deployment. Each configured source is now
probed once at admission. Recorded here, and as amendment 1.0.6 in
[`../001-mvp-core/design-lld.md`](../001-mvp-core/design-lld.md), because a
harness whose first real finding went unrecorded would be a harness nobody
trusts the second time.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial task breakdown | case schema, runner, scoring, corpus, docs |
