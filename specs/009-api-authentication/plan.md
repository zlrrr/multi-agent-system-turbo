# Implementation Plan: Authentication and Authorisation on the HTTP API

> **Feature ID**: `009-api-authentication` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

Two decisions carry this feature. The mechanism is the easy part.

**What to do about the existing open default.** The API is unauthenticated
today, so any answer breaks something: requiring credentials breaks `mas serve`
on a laptop and `make demo`; leaving it optional means an operator who forgets
ships an open API and finds out from someone else.

The resolution comes from what actually differs between those two cases — not
whether a flag was set, but **what the socket can reach**. A loopback bind is
already protected by the host; a `0.0.0.0` bind is not protected by anything.
So the requirement attaches to the address:

- loopback → nothing required, nothing changes, the demo and every existing test
  keep working;
- anything else → authentication required, and the process refuses to start
  without it.

Nobody has to remember a flag, and nobody's laptop breaks. The one thing an
operator must do is exactly the thing that has become dangerous.

**What a token on a plaintext connection is worth.** Bearer auth over HTTP is a
credential on the wire, and shipping it would be building something that looks
secure and is not. But requiring the server to terminate TLS itself would be
wrong for the common Kubernetes deployment, where an ingress already does.

So plaintext off-host is refused *unless the operator states that a proxy
terminates TLS* — a key they have to type, whose meaning is documented, and
which the tool cannot verify. That is honest: it records a fact only the
operator knows, rather than guessing or pretending the question does not exist.

**The mechanism**, once those are settled, is small and follows the guard's
precedent: one middleware, deny by default, a table of route→scope, tokens
compared with `crypto/subtle`, and a structural test that no handler can
authorise for itself.

## 2. Design decisions

| ID | Decision | Rationale |
|---|---|---|
| D-1 | The requirement attaches to the bind address, not to a flag | The laptop case and the exposed case differ in what the socket reaches; making the operator declare it again is ceremony that gets skipped |
| D-2 | Refuse to start rather than warn | A warning at startup is read once, by a log nobody is watching. The failure has to be at the moment someone is looking |
| D-3 | Bearer tokens from configuration secrets, nothing else | OIDC and JWT need a dependency or a partial reimplementation, and a partial JWT verifier is a vulnerability with a friendly name |
| D-4 | Two scopes: `read` and `diagnose` | `POST /diagnoses` spends money and reads production; a dashboard needs neither. Any finer split is per-target, which is P3-3 |
| D-5 | Plaintext off-host allowed only behind an explicit `terminated_by_proxy` | The tool cannot see the proxy. An operator statement is the only honest input, and typing it is the acknowledgement |
| D-6 | `/healthz` and `/readyz` stay anonymous; `/metrics` does not | A liveness probe that needs a credential is a liveness probe that fails during a credential problem. Metrics carry target names and run counts |
| D-7 | One middleware, deny by default, with a route table | Same shape as the safety guard, for the same reason: a handler that authorises for itself is a handler that will one day forget |
| D-8 | The principal is recorded on the run | "Who asked for this" is the other half of authorisation being worth anything |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-1 | A new route is added and nobody wires its scope | Deny by default: an unlisted route is refused, and a structural test asserts every registered route has an entry |
| RSK-2 | A token reaches a log through an error path | `Secret` already refuses to print; a test asserts the token appears in no log, no error body and no rendered config |
| RSK-3 | The address rule is defeated by a proxy binding loopback | Out of scope by construction: a proxy in front of loopback is the operator's deployment, and `terminated_by_proxy` is where they say so |
| RSK-4 | Constant-time comparison is undone by an early length check | `subtle.ConstantTimeCompare` over fixed-size digests of both sides, so length never branches |

## 4. Sequencing

1. Configuration: tokens, scopes, TLS, validation at load.
2. The startup rules: which binds refuse, and why.
3. The middleware: authenticate, authorise, audit.
4. The principal on the run record.
5. `mas doctor` reports exposure; bilingual documentation.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial plan | HLD, LLD, tasks |
