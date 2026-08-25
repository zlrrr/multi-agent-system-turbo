# Feature Specification: Read-Only Command Execution Inside Kubernetes Pods

> **Feature ID**: `004-kube-exec` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.2
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

Knowledge packs declare read-only inspection commands — `redis-cli INFO all`,
`mongosh --eval "db.serverStatus()"`, `kafka-topics.sh --describe` — and the
guard already decides which of those are safe. On a host running middleware as a
binary, the local adapter runs them. In Kubernetes, which the goal names as the
dominant deployment, nothing runs them at all: the adapter can list pods, read
logs and read events, and then stops.

That gap is not cosmetic. `INFO all` answers questions no metric exposes —
`mem_fragmentation_ratio`, `rdb_last_bgsave_status`, the actual `maxmemory-policy`
in force — and an operator diagnosing a memory incident without them is guessing
at the very numbers the middleware would have told them.

The obstacle is deliberate, which is why this needs a specification rather than a
patch. `internal/envadapter/kube.Client` is *structurally* incapable of
execution: `TestClientHasNoMutatingMethods` fails the build if a method's name
contains `Exec`, `Attach` or `PortForward`. That test encodes a real invariant —
the Kubernetes client cannot mutate the cluster — using method names as a proxy
that needs no judgement to check. Any design here must either preserve that
proxy or replace it with something equally checkable. Weakening it into a
comment would trade a structural guarantee for a promise.

There is a second temptation to refuse explicitly. The cheap implementation is to
allow-list `kubectl` and shell out to `kubectl exec`. `kubectl` can delete a
namespace; allow-listing it would put the entire Kubernetes API inside the
allow-list under one binary name, which is exactly the hole deny-by-default
exists to close — the same argument that already keeps `obclient` out of the
OceanBase pack.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| SRE running Redis on Kubernetes | Get `INFO all` from the failing pod, as they would on a host | Memory incident where metrics disagree with symptoms |
| SRE running MongoDB on Kubernetes | Read `rs.status()` from the primary | Replication lag with no exporter for it |
| Platform engineer | Guarantee the tool cannot run anything but vetted read-only commands in their clusters | Security review before rollout |
| Operator under policy | Turn in-container execution off entirely for a cluster | Their policy forbids exec regardless of the command |

## 3. Scope

### In scope
- Executing a knowledge pack's declared inspection commands inside a pod, through
  the Kubernetes API, with the same guard and the same allow-list the local
  adapter uses.
- The Kubernetes remote-command protocol over WebSocket, implemented against
  `net/http` and `crypto/tls` — no new dependency, and no shelling out.
- A first-class `ExecEffect` in the guard, because "run this command in that pod"
  is one effect with two constraints, not two effects.
- Container selection, output capture, exit status, and a byte ceiling.
- A narrowing-only configuration switch that disables exec for an environment.
- Bilingual documentation, including what this deliberately cannot do.

### Out of scope
- `stdin`, TTY, `attach`, `port-forward`, `cp`. None is needed to read state, and
  each widens what a compromised prompt could reach.
- SPDY. The WebSocket transport is supported by every Kubernetes version this
  project targets, and implementing both would double the audit surface for no
  capability.
- Any command not already in the guard's allow-list. This feature changes *where*
  vetted commands can run, never *which* commands are vetted.
- Executing in a pod the target does not resolve to.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | The Kubernetes adapter MUST offer a tool that runs a knowledge pack's inspection command inside a pod of the resolved target | P0 | `TestExecRunsPackInspectCommand` |
| FR-002 | Every exec MUST be authorised by the guard as a single `ExecEffect`, checked against both the command allow-list and the exec path rule | P0 | `TestGuardAuthorisesExecAsOneEffect` |
| FR-003 | A command outside the allow-list MUST be refused before any connection is opened | P0 | `TestExecRefusesUnlistedBinary` |
| FR-004 | A mutating command MUST be refused even though the transport is identical | P0 | `TestExecRefusesMutatingCommand` |
| FR-005 | `kube.Client` MUST remain structurally incapable of execution: the existing name-based audit MUST still pass, unmodified | P0 | `TestClientHasNoMutatingMethods` unchanged |
| FR-006 | Exec MUST capture stdout, stderr and the exit status, and report a non-zero exit as a gap rather than as a failure of the run | P0 | `TestExecCapturesStreamsAndExitStatus` |
| FR-007 | Output MUST be capped by the guard's byte ceiling, and truncation MUST be recorded | P0 | `TestExecTruncatesAtCeiling` |
| FR-008 | The pod and container MUST be chosen from the resolved target's instances; a caller-supplied pod outside them MUST be refused | P0 | `TestExecRefusesPodOutsideTarget` |
| FR-009 | An environment MUST be able to disable exec entirely, and the switch MUST only ever narrow | P1 | `TestExecCanBeDisabledPerEnvironment` |
| FR-010 | Exec MUST be unavailable in offline mode, like every other live tool | P1 | `TestExecRequiresOnlineMode` |
| FR-011 | `mas doctor` MUST report whether exec is available for each Kubernetes environment, and why not when it is not | P1 | Doctor test |
| FR-012 | A failure to upgrade the connection MUST degrade to a coded gap, never a panic or a hang | P0 | `TestExecUpgradeFailureIsCoded` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | No new module dependency | `go.mod` unchanged |
| NFR-002 | The WebSocket implementation MUST be exercised against a server that speaks the real framing, not a mock of our own client | Test server built from `net/http` hijack |
| NFR-003 | Every exec MUST appear in the run record with its command, pod and exit status | Replay test |
| NFR-004 | Secrets MUST NOT reach the transcript: arguments are redacted by the same redactor as everything else | Redaction test |
| NFR-005 | Bilingual parity for every operator-facing string | `sddctl verify` |
| NFR-006 | An exec MUST respect the run's timeout and never outlive it | `TestExecHonoursTimeout` |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Read-only always; the transport does not change what is permitted | Constitution Art. IV |
| CON-002 | `kubectl` MUST NOT be added to the command allow-list | Deny-by-default; one binary must not stand in for a whole API |
| CON-003 | No configuration key may widen the guard, including this feature's switch | Art. IV.2 |
| CON-004 | No shell, ever, on either side of the connection | Art. IV; existing audit |
| CON-005 | The exec path rule MUST match the exec subresource and nothing else | Art. IV.1 |

## 7. Acceptance

The feature is done when a knowledge pack's inspection command runs inside a pod
on a live cluster and its output appears as evidence; an unlisted or mutating
command is refused before a connection opens; `kube.Client` still cannot express
execution; exec can be disabled per environment; and `make ci` is green.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial specification | plan, HLD, LLD, tasks, code |
