# Task Breakdown: Switchable Multi-Agent Topologies

> **Feature ID**: `003-switchable-topologies` · **Version**: 1.0.1
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.

## Phase A — the comparability contract

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T201 | `core.Text` extracted; `knowledge.Text` becomes an alias | NFR-004 | Existing knowledge tests pass unchanged | — | done |
| T202 | Bilingual `Description` in the registry; `supervisor` and `single` restated with `Cost` and `Avoid` | FR-010, NFR-004 | `TestTopologyDescriptionsAreBilingualAndHonest` | T201 | done |
| T203 | Conformance contract: governed, hypothesis+summary, role attribution, budget, degradation, determinism | FR-001, FR-005, FR-006, FR-007, FR-008, FR-009, NFR-003 | Passes for `supervisor` and `single` before any new topology exists | T202 | done |
| T204 | Broken-topology proof | FR-001 | `TestBrokenTopologyFailsTheContract` | T203 | done |
| T205 | Structural audit: a topology may not reach outside the registry it was handed, nor widen the guard | NFR-001, NFR-005, CON-001 | Audit test extended | T203 | done |
| **G-A** | **Gate A** | | `go test ./internal/orchestrator/... ./internal/audit/...` green with only the two existing topologies | | done |

## Phase B — `plan-execute`

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T210 | `Strategist` and `Executor` roles, prompts, mock replies | FR-002, NFR-002 | Role unit tests | G-A | done |
| T211 | `plan-execute` topology with bounded adaptive rounds | FR-002, CON-003 | `TestPlanExecuteReplansOnFindings`, `TestPlanExecuteStopsWhenConverged` | T210 | done |
| T212 | Conformance contract passes for `plan-execute` | FR-001, FR-005…FR-009 | Contract, per topology | T211 | done |

## Phase C — `debate`

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T220 | `Advocate` and `Judge` roles, prompts, mock replies | FR-003 | Role unit tests | G-A | done |
| T221 | `debate` topology, concurrent advocates, deterministic note order | FR-003, CON-003 | `TestDebateProducesAdjudicatedPositions`, `TestDebateWithoutPositionsFallsBack` | T220 | done |
| T222 | Conformance contract passes for `debate` | FR-001, FR-005…FR-009 | Contract, per topology | T221 | done |

## Phase D — `blackboard`

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T230 | Deterministic eligibility control component | FR-004 | `TestBlackboardSchedulesByEligibility` | G-A | done |
| T231 | `blackboard` topology; termination when a round contributes nothing | FR-004 | `TestBlackboardTerminates`, `TestBlackboardSkipsWhatCannotContribute` | T230 | done |
| T232 | Conformance contract passes for `blackboard` | FR-001, FR-005…FR-009 | Contract, per topology | T231 | done |
| **G-B** | **Gate B** | | All five topologies pass one contract | | done |

## Phase E — surface and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T240 | `mas topologies` renders the operator's language; HTTP endpoint likewise | FR-010 | CLI test asserts all five names and both languages | G-B | done |
| T241 | Run record and report carry topology and its cost | FR-011 | `TestRunRecordCarriesTopologyAccounting` | G-B | done |
| T242 | Unknown topology still fails with `MAS-3001` before any model call | FR-012 | Existing admission test extended to the new names | G-B | done |
| T243 | Bilingual documentation: user manual §8 rewritten for five topologies, with the cost and when-not-to columns | FR-010, NFR-004 | `sddctl verify` parity | G-B | done |
| T244 | Listing commands honour the configured language, not only `--lang` | FR-010 | `TestTopologiesCommandDescribesEveryTopologyBilingually` | G-B | done |
| T245 | CLI wrapping measures terminal columns and breaks CJK, so Chinese wraps at the intended width | FR-010, NFR-004 | `TestWrapMeasuresColumnsNotBytes`, `TestWrapBreaksCJKWithoutSpaces`, `TestWrapKeepsClosingPunctuationOnTheLineItCloses` | T244 | done |
| T246 | Hypothesis citations resolved against what the run collected (found by the contract running every topology with no tools) | NFR-002 | `TestFabricatedCitationsAreDroppedAndRecorded`, `TestRealCitationsSurvive` | T203 | done |
| **G-C** | **Gate C — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T201–T205 | `go test ./internal/orchestrator/... ./internal/audit/...` |
| G-B | T210–T232 | `go test ./internal/orchestrator/... ./internal/agent/...` |
| G-C | T240–T243 | `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.1 | 2026-08-24 | T244–T246 added: the conformance contract and the bilingual surface exposed three defects outside this feature's own code — the configured language was ignored by listing commands, CLI wrapping measured bytes so Chinese wrapped at a third width, and hypothesis citations were reprinted unresolved | `specs/001-mvp-core/design-lld.md` amended to 1.0.3 |
| 1.0.0 | 2026-08-24 | Initial task breakdown | roles, topologies, docs |
