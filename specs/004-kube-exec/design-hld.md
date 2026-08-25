# High-Level Design: Read-Only Command Execution Inside Kubernetes Pods

> **Feature ID**: `004-kube-exec` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
knowledge pack ── inspect command ──► kube.exec tool
                                          │  Plan() → safety.Call{Exec: …}
                                          ▼
                                    tool.Invoker ──► safety.Guard.Authorize
                                          │              ├─ command allow-list
                                          │              └─ exec path rule
                                          ▼
                                    kube.ExecClient ──► GET …/pods/{p}/exec (upgrade)
                                                             │
                                                    v4.channel.k8s.io frames
                                                    ├─ ch 1 stdout
                                                    ├─ ch 2 stderr
                                                    └─ ch 3 status (exit code)
```

Nothing about the guard's position changes: the tool declares its intended effect
before it happens, the invoker is still the only path to a tool, and the guard is
still the only authorizer.

## 2. Two invariants, both mechanical

The existing invariant is preserved verbatim:

> **`Client` cannot express mutation.** Every method issues a GET, and no method
> name may contain `Exec`, `Attach`, `PortForward`, `Create`, `Delete`, …

Execution therefore lives in a second type, which earns a second invariant of the
same kind:

> **`ExecClient` can address exactly one endpoint.** It has one exported method,
> takes no path argument, and builds its URL from a namespace, a pod and a
> container. There is no input by which a caller could point it elsewhere.

Both are checked by name and shape rather than by reading the bodies, which is
what makes them survive maintenance by someone who has not read this document.

## 3. Why the effect is one thing, not two

An exec is an HTTP request *and* a command execution. Modelling it as two
`Authorize` calls would mean the caller composes the two checks, and a caller
that forgot the command check would still compile and still run — the failure
would be invisible until someone read the code.

So the guard gains one new effect:

```
ExecEffect{ Namespace, Pod, Container, Binary, Args }
```

and authorises it by requiring **both** constraints to hold:

1. `Binary` + `Args` pass the same command allow-list the local adapter uses —
   the same rules, the same mutating-verb detection, the same value allow-lists;
2. the URL the effect implies matches the exec path rule and nothing else.

The consequence is worth stating plainly: **this feature adds no command.**
`redis-cli INFO` was already permitted and now has somewhere new to run;
`redis-cli FLUSHALL` was already refused and is still refused, with the identical
error code, before any connection opens.

## 4. What a compromised prompt can reach

The threat that matters is not a malicious operator; it is a model that has read
attacker-controlled log lines and now wants to run something. The reachable set
is bounded by four independent things, and widening any one of them is a
specification change:

| Bound | Set by | Could a prompt widen it? |
|---|---|---|
| Which binaries | The guard's command allow-list | No — deny-by-default, and no config key widens it |
| Which arguments | Mutating-verb detection and per-flag value allow-lists | No |
| Which pods | The instances the target resolved to | No — the tool takes an instance, not a pod name |
| Which endpoint | `ExecClient`'s fixed URL shape | No — there is no path parameter |

`stdin` and TTY are absent for this reason and not for simplicity: both turn a
one-shot read into a session, and a session is steerable.

## 5. Transport

Kubernetes remote command over WebSocket is a `GET` carrying the RFC 6455
upgrade headers and the `v4.channel.k8s.io` subprotocol. Each binary frame's
first byte is a channel number; the rest is payload. Channel 3 delivers a JSON
status at the end, which is where the exit code comes from.

Two consequences shape the design:

- **No new HTTP verb enters the guard.** The exec path rule is a `GET` rule,
  alongside the pod and log rules it sits between.
- **The implementation is small and self-contained**: a handshake, a frame
  reader, and a three-way demultiplexer. It is exercised against a test server
  that speaks the real protocol from the server side, so the client is never
  tested against a mirror of its own assumptions.

## 6. Degradation

Every failure mode becomes a gap with a code, never a failed run:

| Failure | Becomes |
|---|---|
| Command not allow-listed | Refusal before connecting, existing `MAS-8002`/`MAS-8001` |
| Exec disabled for this environment | A gap stating the policy, so the reader knows the check was not merely skipped |
| Upgrade refused (RBAC, no such pod, apiserver policy) | A coded gap naming the reason |
| Non-zero exit | Evidence *plus* a gap: the command ran and disagreed, which is a result |
| Output over the ceiling | Truncated evidence plus a gap saying how much was cut |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial high-level design | LLD, tasks, code |
