# Implementation Plan: Read-Only Command Execution Inside Kubernetes Pods

> **Feature ID**: `004-kube-exec` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. The decision that shapes everything else

The Kubernetes client cannot gain an `Exec` method, because
`TestClientHasNoMutatingMethods` would fail — and that test should not be
weakened, because a name-based structural check needs no reviewer judgement and
therefore cannot rot.

The resolution is to keep the invariant by keeping the type. Execution lives in a
**separate type in a separate file**, `ExecClient`, which:

- holds no reference to `Client` and offers exactly one method;
- builds its own URL from a namespace, a pod and a container — it accepts no
  caller-supplied path, so it is structurally unable to address any endpoint but
  the exec subresource;
- carries an argument vector, never a command string, so there is nothing for a
  shell to interpret even if one were reachable.

`Client` keeps its property — "every method issues a GET, and none can express
mutation" — literally unchanged, and `ExecClient` gets a property of its own that
is just as mechanical: *one method, one URL shape, no path parameter*. A new
audit test asserts the second, so the split does not trade one checked invariant
for one unchecked one.

## 2. Design decisions

| ID | Decision | Rationale | Reversal condition |
|---|---|---|---|
| D-1 | Execution in a separate `ExecClient`, not on `Client` | Keeps the existing structural audit passing unmodified and gives exec its own, equally mechanical invariant | — |
| D-2 | WebSocket (`v4.channel.k8s.io`), not SPDY | The apiserver has accepted WebSocket for remote command since long before any version this project targets, the framing is a few hundred lines of RFC 6455, and it is a GET-with-upgrade — so no new HTTP verb enters the guard at all. SPDY would need a second protocol implementation for no extra capability | The target Kubernetes version predates WebSocket exec, which no supported release does |
| D-3 | `ExecEffect` as a first-class effect the guard checks against **both** the command allow-list and the exec path rule | "Run this command in that pod" is one effect with two constraints. Splitting it into two `Authorize` calls would put the composition in the caller's hands, and a caller that forgot one would still compile | — |
| D-4 | `kubectl` stays out of the allow-list, permanently | One binary name would stand in for the whole Kubernetes API, which is the failure deny-by-default exists to prevent — the same argument that keeps `obclient` out of the OceanBase pack | — |
| D-5 | The pod must come from the resolved target's instances | An agent that can name any pod can read any pod. Binding exec to what the target resolved to keeps the blast radius the same as the rest of the run | — |
| D-6 | No stdin, no TTY | Neither is needed to read state, and both turn a one-shot read into an interactive session a prompt could steer | A pack genuinely needs an interactive tool, which none does |
| D-7 | Exec enabled by default, disable-only switch | The local adapter already runs allow-listed commands without an opt-in, and the guard is the control that matters. A switch that can only narrow gives policy-bound operators a lever without creating one that widens | — |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-001 | A hand-written WebSocket client mis-frames and hangs, taking the run's timeout with it | The context deadline is applied to the connection, not only to the read loop; every test runs under a short timeout; a malformed frame is a coded gap |
| RSK-002 | The channel demultiplexer mistakes stderr for stdout, or drops the error channel that carries the exit status | The test server speaks the real protocol and is driven from the server side, so the client is never tested against its own assumptions (NFR-002) |
| RSK-003 | An operator believes exec is safe *because it is exec*, and asks for a wider allow-list | The documentation states plainly that this changes where vetted commands run and never which commands are vetted |
| RSK-004 | Output floods the context window or the report | The guard's byte ceiling applies, truncation is recorded as a gap, and the reader is told what was cut |
| RSK-005 | Someone later adds a second method to `ExecClient` and reopens the surface | The new audit test asserts the one-method shape, so the reopening fails the build |

## 4. Sequencing

| Phase | Content | Gate |
|---|---|---|
| A | `ExecEffect` in the guard; exec path rule; refusal tests | Guard tests green; unlisted and mutating commands refused |
| B | RFC 6455 client and channel demultiplexer, against a real test server | Streams and exit status correct; malformed input coded |
| C | `ExecClient`, the `kube.exec` tool, target binding, the disable switch | FR-001, FR-008, FR-009, FR-010 |
| D | Doctor, run record, bilingual docs | `make ci`, `sddctl verify` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial plan | HLD, LLD, tasks |
