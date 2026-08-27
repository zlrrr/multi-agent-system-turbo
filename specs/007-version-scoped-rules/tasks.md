# Task Breakdown: Version-Scoped Pack Rules

> **Feature ID**: `007-version-scoped-rules` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.
Every test named here must exist: `sddctl verify` checks it.

## Phase A — the field and its validation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T601 | `VersionRange` on Signal, LogPattern, FailureMode, Playbook, Step, Inspect | FR-001 | `TestEveryRuleKindAcceptsAVersionRange` | — | done |
| T602 | Interval form of a range, and overlap detection biased towards reporting overlap | FR-004 | `TestRangeOverlapDetection` | T601 | done |
| T603 | Variants accepted only when every declaration is scoped and none overlap | FR-003, FR-004 | `TestVariantsWithDisjointRangesAreAccepted`, `TestOverlappingVariantsAreRejected` | T602 | done |
| T604 | Error codes `MAS-5016`…`MAS-5019`, bilingual, docs regenerated | CON-004 | `mas errcodes` output current | T603 | done |
| **G-A** | **Gate A** | | A scoped pack loads; an ambiguous one is refused | | done |

## Phase B — resolution

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T610 | `Pack.Resolve`: shallow copy, out-of-range rules dropped | FR-002 | `TestOutOfRangeRulesAreDropped` | G-A | done |
| T611 | An unscoped pack resolves to itself | NFR-004 | `TestUnscopedPackResolvesToItself` | T610 | done |
| T612 | Variant selection by version | FR-005 | `TestVariantMatchingTheVersionIsChosen` | T610 | done |
| T613 | Unknown version drops variants with a gap that names the remedy | FR-006 | `TestUnknownVersionDropsVariantsWithAGap` | T612 | done |
| T614 | Steps follow the signals and failure modes they depend on | FR-007 | `TestStepsFollowTheRulesTheyDependOn` | T610 | done |
| T615 | Steps follow the slots they read, using the engine's identifier scanner | FR-008 | `TestStepsFollowTheSlotsTheyRead` | T614 | done |
| T616 | A playbook with no surviving conclusion is dropped | FR-009 | `TestEmptyPlaybooksAreDropped` | T615 | done |
| T617 | Every drop is a gap with code, detail and impact | FR-010, CON-002 | `TestSkippedRulesAreRecordedAsGaps` | T616 | done |
| T618 | Resolution never widens, over a table of versions | FR-011, CON-001 | `TestResolutionNeverWidens` | T616 | done |
| **G-B** | **Gate B** | | `go test ./internal/knowledge/...` | | done |

## Phase C — the run

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T620 | The service resolves once at admission and carries the gaps | FR-012, NFR-001 | `TestDiagnosisUsesTheResolvedPack` | G-B | done |
| T621 | Kafka's ZooKeeper and KRaft boundaries, from documented facts only | FR-014 | `TestKafkaPackScopesZooKeeperRules` | T620 | done |
| T622 | A corpus case on each side of the Kafka 4.0 boundary | FR-014, NFR-004 | `TestShippedCorpusPasses` | T621 | done |
| **G-C** | **Gate C** | | `make eval` clean; corpus unchanged in behaviour | | done |

## Phase D — surface and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T630 | `mas packs --show --version` previews the resolution | FR-013 | `TestPacksCommandShowsVersionScoping` | G-C | done |
| T631 | Bilingual pack-authoring guidance for ranges and variants | NFR-003, NFR-002 | `sddctl verify` parity; `go.mod` unchanged | T630 | done |
| **G-D** | **Gate D — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T601–T604 | `go test ./internal/knowledge/...` |
| G-B | T610–T618 | `go test ./internal/knowledge/...` |
| G-C | T620–T622 | `make eval` |
| G-D | T630–T631 | `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial task breakdown | code, packs, docs |
