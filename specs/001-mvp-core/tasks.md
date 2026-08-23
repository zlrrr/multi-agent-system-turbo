# Task Breakdown: MVP Core

> **Feature ID**: `001-mvp-core` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`[P]` = parallelisable with its neighbours · `status` ∈ `todo | doing | done | blocked`
Every task declares its test **before** implementation (Constitution Art. VI.1).
A task is `done` only when its test passes (Art. VI.2).

## Phase A — Foundation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T001 | Go module, `Makefile`, `.golangci.yml`, `internal/version` | — | `make build` produces `mas`; `mas version` prints build info | — | todo |
| T002 | `pkg/errs`: registry, `Error`, lookup, bilingual definitions | FR-017 | `TestRegistryUnique`, `TestAllCodesRegistered`, `TestBilingualComplete`, `TestCodeOfThroughWrap` | T001 | todo |
| T003 | `internal/core`: domain model + invariants + JSON round-trip | FR-011, FR-012 | `TestReportRoundTrip`, `TestInvariants`, `TestNoUpwardImports` | T002 | todo |
| T004 | `internal/config`: model, load/merge precedence, `Secret`, validation | FR-001, FR-016 | `TestPrecedence`, `TestValidateCodes`, `TestSecretNeverSerialises`, `TestResolveRefs` | T002 | todo |
| T005 | `internal/safety`: `Redactor` | FR-016 | `TestRedactPatterns`, `TestRedactNestedAny` | T004 | todo |
| T006 | `internal/safety`: `Guard` — six checks, deny-by-default | FR-006, CON-001, CON-002 | `TestGuardAdversarial` (≥30 hostile inputs), `TestGuardCannotBeWidened` | T005 | todo |
| T007 | `internal/obs`: slog setup, redacting handler, run context, self-metrics | FR-017, G11.4 | `TestRunIDPropagates`, `TestHandlerRedacts`, `TestPromExposition` | T005 | todo |
| **G-A** | **Gate A** | | `go test ./pkg/... ./internal/errs/... ./internal/core/... ./internal/config/... ./internal/safety/... ./internal/obs/...` green | | todo |

## Phase B — Capability layer

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T010 | `internal/tool`: `Tool`, `Schema`, `Registry`, guarded `Invoker` | FR-006 | `TestInvokerValidatesArgs`, `TestGuardRefusalBecomesGap`, `TestTimeoutBecomesCeilingCode` | T006 | todo |
| T011 | Structural safety tests: `TestNoUnguardedIO`, no `sh -c` in tree | NFR-003 | both tests green | T010 | todo |
| T012 | `collector/promql` client + 3 tools [P] | FR-003 | `TestInstant`, `TestRange`, `TestSeries`, `TestAuthHeaders`, `TestTruncation`, `TestErrorMapping` | T010 | todo |
| T013 | `collector/loki` client + 2 tools [P] | FR-004 | `TestQuery`, `TestLimit`, `TestLabels`, `TestErrorMapping` | T010 | todo |
| T014 | `envadapter/kube` read-only REST client + 5 tools [P] | FR-005 | `TestListPods`, `TestPodLogs`, `TestEvents`, `TestNodes`, `TestAuthModes`, `TestKubeClientHasNoMutatingMethods` | T010 | todo |
| T015 | `envadapter/local` host inspection + 4 tools [P] | FR-021 | `TestProcesses`, `TestPorts`, `TestInspectAllowListed`, `TestInspectRefusesMutating` | T010 | todo |
| T016 | `internal/source` fetch with network→local fallback + search + 2 tools | FR-022, FR-023 | `TestFallbackOnUnreachable`, `TestNoMirrorGap`, `TestCacheHitSkipsNetwork`, `TestSearchFixture` | T010 | todo |
| **G-B** | **Gate B** | | `go test ./internal/tool/... ./internal/collector/... ./internal/envadapter/... ./internal/source/...` green | | todo |

## Phase C — Knowledge & rules

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T020 | `internal/knowledge`: pack types, schema validation, loader, embed | FR-007 | `TestSchemaViolations`, `TestUserDirOverrides`, `TestVersionRange`, `TestBilingualPackFields` | T003 | todo |
| T021 | Redis knowledge pack (signals, log patterns, failure modes, playbooks, inspect) | G2.2 | `TestEmbeddedPacksValid`, `TestRedisPackConformance` | T020 | todo |
| T022 | Kafka knowledge pack | G2.2 | `TestKafkaPackConformance` | T020 | todo |
| T023 | `internal/rules`: playbook engine, sandboxed expressions, findings | FR-008 | `TestPlaybookHappyPath`, `TestMissingEvidenceSkips`, `TestExpressionErrorsCoded`, `TestZeroLLMCalls`, `TestUnder2Seconds` | T020, T010 | todo |
| **G-C** | **Gate C** | | `go test ./internal/knowledge/... ./internal/rules/...` green | | todo |

## Phase D — Reasoning layer

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T030 | `internal/llm`: types, `Provider`, registry, budget accounting | FR-010, FR-019 | `TestRegistryOpen`, `TestUnknownProviderCoded` | T004 | todo |
| T031 | `llm/mock` scripted deterministic provider | Art. VI.3, NFR-010 | `TestMockDeterminism`, `TestMockToolSequence` | T030 | todo |
| T032 | `llm/anthropic` [P] | FR-010 | `TestAnthropicToolRoundTrip`, `TestAnthropicErrorMapping`, `TestAPIKeyRedactedInErrors` | T030 | todo |
| T033 | `llm/openai` (OpenAI-compatible) [P] | FR-010 | `TestOpenAIToolRoundTrip`, `TestBaseURLOverride`, `TestOpenAIErrorMapping` | T030 | todo |
| T034 | `internal/agent`: `State`, budgets, `toolLoop`, prompt templates | FR-009, FR-019 | `TestBudgetEnforced`, `TestInvalidToolCallRepairThenGap` | T031, T010 | todo |
| T035 | Roles: planner, investigator, correlator, critic, reporter | G7.1 | one behavioural test per role against a scripted mock | T034 | todo |
| T036 | `internal/orchestrator`: interface, registry, `single` | FR-009 | `TestSingleProducesReport`, `TestRegistryRejectsDuplicate` | T035 | todo |
| T037 | `orchestrator/supervisor` with concurrent investigators | FR-009 | `TestSupervisorProducesReport`, `-race` clean | T036 | todo |
| **G-D** | **Gate D** | | `go test -race ./internal/llm/... ./internal/agent/... ./internal/orchestrator/...` green | | todo |

## Phase E — Output & persistence

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T040 | `internal/report`: Markdown (en/zh) + JSON renderers | FR-011 | golden-file tests for all four outputs | T003 | todo |
| T041 | `internal/store`: `RunStore`, `fs`, `memory` | FR-012 | `TestFSRoundTrip`, `TestAppendOnly`, `TestCorruptDetected`, `TestList` | T003 | todo |
| T042 | `internal/service`: admission, two-phase pipeline, short-circuit, degradation, accounting | FR-001, FR-002, FR-008, FR-013, FR-019 | `TestAdmissionCodes`, `TestShortCircuit`, `TestAllSourcesDownStillCompletes`, `TestEndToEndUnder5s`, `TestDeterminism` | T023, T037, T041 | todo |
| T043 | Replay | FR-012 | `TestReplayWithoutNetwork` | T042 | todo |
| **G-E** | **Gate E** | | `go test ./internal/report/... ./internal/store/... ./internal/service/...` green | | todo |

## Phase F — Surfaces

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T050 | `internal/cli`: all subcommands, global flags, output formats | FR-014 | smoke test per subcommand | T042 | todo |
| T051 | `mas doctor` checks across config, telemetry, env, LLM, packs, source | FR-018 | `TestDoctorAgainstStubs` | T050 | todo |
| T052 | `internal/httpapi`: endpoints, health, `/metrics`, error mapping | FR-015 | one test per endpoint incl. 4xx paths | T042 | todo |
| **G-F** | **Gate F** | | `go test ./internal/cli/... ./internal/httpapi/...` green | | todo |

## Phase G — Delivery

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T060 | `cmd/sddctl`: bilingual parity, traceability staleness, requirement coverage | NFR-009, G13 | `TestParityDetectsMissingZH`, `TestStalenessDetected`, `TestCoverageGap`; `make sdd-verify` green | T001 | todo |
| T061 | Multi-stage `Dockerfile`, non-root, `docker-compose` example | FR-020, NFR-005 | image builds; `docker run … version` and `… diagnose` succeed | T050 | todo |
| T062 | `.github/workflows/ci.yml`: fmt, vet, lint, test `-race`, build matrix, sdd-verify | Art. VIII.2 | workflow green | T060 | todo |
| T063 | `.github/workflows/release.yml`: tag → binaries + checksums + image | FR-020, G12.2 | dry-run on a tag produces artifacts | T062 | todo |
| T064 | Bilingual user manual, configuration reference, error-code reference, README, quickstart | G12.3, NFR-009 | `sddctl verify` parity green; manual walkthrough reproduces the demo | T050 | todo |
| T065 | Example configs + demo fixtures (`examples/`) so a fresh user gets a report in one command | G12.1 | `make demo` produces a report | T064 | todo |
| **G-G** | **Gate G — M1 exit** | | `make ci` green; image runs; release artifacts produced | | todo |

## Checkpoint gates

| Gate | Tasks that must be `done` | Verification command |
|---|---|---|
| G-A | T001–T007 | `make test-foundation` |
| G-B | T010–T016 | `make test-capability` |
| G-C | T020–T023 | `make test-knowledge` |
| G-D | T030–T037 | `make test-reasoning` |
| G-E | T040–T043 | `make test-output` |
| G-F | T050–T052 | `make test-surfaces` |
| G-G | T060–T065 | `make ci && make docker && make demo` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-23 | Initial task breakdown from LLD v1.0.0 | code |
