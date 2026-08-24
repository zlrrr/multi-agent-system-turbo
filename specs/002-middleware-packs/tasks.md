# Task Breakdown: Middleware Knowledge Breadth

> **Feature ID**: `002-middleware-packs` · **Version**: 1.0.1
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.

## Phase A — the conformance floor

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T101 | `conformance_test.go`: depth, named modes, bilingual, advice, risk vocabulary, always-on playbook | FR-005, FR-007 | Passes for redis and kafka; fails informatively for a synthetic shallow pack | — | done |
| T102 | Guard pass over every pack's inspect commands | FR-006, CON-002 | `TestPackInspectCommandsPassTheGuard` | T101 | done |
| T103 | Expression compilation and signal-reference resolution | FR-009 | `TestPackExpressionsCompile` | T101 | done |
| T104 | Advisory-phrasing scan, both languages | FR-007, CON-003 | `TestPackRecommendationsAreAdvisory` | T101 | done |
| T105 | Honest-coverage cross-check of indicators against declared signals | NFR-002 | `TestPackCoverageIsHonest` | T101 | done |
| **G-A** | **Gate A** | | `go test ./internal/knowledge/...` green with only redis and kafka present | | done |

## Phase B–E — the packs

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T110 | MongoDB pack: ≥10 signals, ≥6 log patterns, 7 failure modes, ≥3 playbooks, inspect commands | FR-001 | Conformance passes; playbooks produce findings against stub telemetry | G-A | done |
| T120 | Pulsar pack | FR-002 | As above | G-A | done |
| T130 | Milvus pack | FR-003 | As above | G-A | done |
| T140 | OceanBase pack | FR-004 | As above | G-A | done |
| T150 | Integration: each new pack runs end to end through the rules engine | FR-001…004 | `TestNewPacksRunAgainstStubTelemetry` | T110–T140 | done |
| T151 | Regression gates: embedded-pack size stays within budget, and playbook selection stays deterministic with six packs loaded | NFR-003, NFR-004 | `TestEmbeddedPackSizeBudget`; existing determinism tests still pass with all six packs | T110–T140 | done |
| T152 | Rule-engine corrections the integration test exposed: regex literals are not slot references, and an unmeasured metric is not a passed check | NFR-002 | `TestRegexLiteralsAreNotSlotReferences`, `TestIdentifiersIgnoreQuotedText`, `TestEmptyMetricIsNotReportedAsPassed`, `TestDeliberateEmptyReadingStillFires` | T150 | done |
| **G-B** | **Gate B** | | All six packs pass one conformance test, within the size budget, with selection still deterministic | | done |

## Phase F — documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T160 | Bilingual pack-authoring guide: schema, expression environment, conformance rules, metric-source table, the `not X.empty` guidance | FR-008, NFR-001 | `sddctl verify` parity passes; the guide's example pack loads | G-B | done |
| T161 | Confirm no loader change was needed | FR-010 | `git diff --stat internal/knowledge/*.go` shows only the new test | G-B | done |
| T162 | `sdd.sh amend` keeps the reviewer notes it edits around | NFR-001 | `TestAmendPreservesReviewerNotes` | T152 | done |
| T163 | `MAS-5002` made real: overriding a shipped pack stays supported, two local packs claiming one id are reported | FR-008, NFR-002 | `TestOverridingAShippedPackIsAllowed`, `TestTwoLocalPacksCollide` | T160 | done |
| **G-C** | **Gate C — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T101–T105 | `go test ./internal/knowledge/...` |
| G-B | T110–T151 | `go test ./internal/knowledge/... ./internal/rules/... ./internal/service/...` |
| G-C | T160–T161 | `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.1 | 2026-08-24 | T152 and T162 added: the integration test exposed two silent rule-engine defects and one in the SDD tooling; all recorded rather than fixed in passing | `specs/001-mvp-core/design-lld.md` amended to 1.0.2 |
| 1.0.0 | 2026-08-24 | Initial task breakdown | packs, tests, docs |
