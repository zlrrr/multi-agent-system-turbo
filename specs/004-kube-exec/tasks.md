# Task Breakdown: Read-Only Command Execution Inside Kubernetes Pods

> **Feature ID**: `004-kube-exec` · **Version**: 1.0.1
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.

## Phase A — the guard

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T301 | `ExecEffect`, `Call.Exec`, still exactly-one-effect | FR-002, CON-001 | `TestGuardAuthorisesExecAsOneEffect` | — | done |
| T302 | `authorizeExec` reuses the command allow-list verbatim | FR-003, FR-004, CON-002 | `TestExecRefusesUnlistedBinary`, `TestExecRefusesMutatingCommand` | T301 | done |
| T303 | Exec path rule, and component validation so it cannot be escaped | CON-005 | `TestExecPathComponentsCannotEscape` | T301 | done |
| T304 | Error codes `MAS-4210`…`MAS-4214`, bilingual, docs regenerated | NFR-005 | `mas errcodes` output current | T301 | done |
| **G-A** | **Gate A** | | `go test ./internal/safety/...` green; refusals happen with no network | | done |

## Phase B — the transport

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T310 | Test server speaking `v4.channel.k8s.io` from the server side, with no new module dependency on either side | NFR-001, NFR-002 | Used by every test below | G-A | done |
| T311 | RFC 6455 handshake with verified `Sec-WebSocket-Accept` | FR-012 | `TestWebSocketRejectsUnverifiedAccept` | T310 | done |
| T312 | Frame reader: continuation, ping/pong, close, malformed | FR-012 | `TestExecMalformedFrameIsCoded` | T311 | done |
| T313 | Channel demultiplexer and exit status from the status frame | FR-006 | `TestExecCapturesStreamsAndExitStatus`, `TestExecMissingStatusIsCoded` | T312 | done |
| T314 | Byte ceiling and truncation | FR-007 | `TestExecTruncatesAtCeiling` | T313 | done |
| T315 | Context deadline applied to the connection | NFR-006 | `TestExecHonoursTimeout` | T311 | done |
| **G-B** | **Gate B** | | `go test ./internal/envadapter/kube/...` green under `-race` | | done |

## Phase C — the tool

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T320 | `ExecClient` with one method and no path argument | FR-005 | `TestExecClientAddressesOneEndpoint`; `TestClientHasNoMutatingMethods` unmodified | G-B | done |
| T321 | `kube.exec` tool taking an instance and a pack command id | FR-001, FR-008 | `TestExecRunsPackInspectCommand`, `TestExecRefusesPodOutsideTarget` | T320 | done |
| T322 | `exec: false` removes the tool; narrowing only | FR-009, CON-003 | `TestExecCanBeDisabledPerEnvironment` | T321 | done |
| T323 | Online-mode requirement | FR-010 | `TestExecRequiresOnlineMode` | T321 | done |
| T324 | Upgrade failure degrades to a coded gap | FR-012 | `TestExecUpgradeFailureIsCoded` | T321 | done |
| **G-C** | **Gate C** | | A pack's inspect command runs end to end against the test server | | done |

## Phase D — surface and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T330 | `mas doctor` reports exec availability and why not | FR-011 | Doctor test | G-C | done |
| T331 | Run record carries command, pod and exit status | NFR-003 | Replay test | G-C | done |
| T332 | Argument redaction on the exec path | NFR-004 | Redaction test | G-C | done |
| T333 | Bilingual documentation: manual, configuration reference, knowledge-pack guide | NFR-005 | `sddctl verify` parity | G-C | done |
| T334 | Correct the RBAC promise the manual made ("MAS-Turbo never requests `pods/exec`"), which this feature made false | NFR-005, CON-002 | Manual states the widening, its four bounds, and the opt-out | G-C | done |
| T335 | Structural audits: exec is reachable only through an `ExecEffect`, and `kubectl` stays out of the allow-list | NFR-001, NFR-005, CON-002, CON-005 | `TestExecIsReachableOnlyThroughTheGuard`, `TestNoKubectlInTheAllowList` | G-A | done |
| T336 | Every shipped pack command must survive in-container substitution | FR-001 | `TestEveryPackInspectCommandSurvivesContainerSubstitution` | T321 | done |
| **G-D** | **Gate D — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T301–T304 | `go test ./internal/safety/...` |
| G-B | T310–T315 | `go test -race ./internal/envadapter/kube/...` |
| G-C | T320–T324 | `go test ./internal/envadapter/... ./internal/audit/...` |
| G-D | T330–T333 | `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.1 | 2026-08-25 | T334–T336 added: the guard's own adversarial suite rejected the first path-rule design, the manual carried an RBAC promise this feature falsifies, and a pack template that loses its port silently turns a vetted command into a refused one | Manual corrected; two structural audits added; every pack command checked in-container |
| 1.0.0 | 2026-08-25 | Initial task breakdown | guard, transport, tool, docs |
