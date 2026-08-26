# High-Level Design (HLD): Authentication and Authorisation on the HTTP API

> **Feature ID**: `009-api-authentication` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
                      ┌─ /healthz, /readyz ──────────────────► handler
                      │  (anonymous by design)
  request ──► mux ────┤
                      └─ everything else ──► authorise ──┬──► handler
                                                  │      │     (principal in context)
                                          route→scope    │
                                          token table    └──► 401 / 403 + code
                                                  │
                                            audit log
```

One box is new, and it sits between the router and every handler that is not a
health check. That is the same shape the safety guard has, for the same reason:
a handler that authorises for itself is a handler that will one day be copied
without the check.

A second thing is new and is not on this diagram, because it happens once and
never during a request: **admission**. Before the listener opens, the
configuration is examined against the address it is about to bind, and a
configuration that would expose an unauthenticated or plaintext-credentialled
API off-host stops the process.

## 2. The rule that attaches to the address

The API is open today, so any change breaks something. Requiring credentials
everywhere breaks `mas serve` on a laptop, `make demo`, and the tests. Leaving
credentials optional means the operator who forgets ships an open API.

The way out is to notice that those two cases do not differ by intent — they
differ by **what the socket can reach**:

| Bind address | Authentication | TLS | Result |
|---|---|---|---|
| loopback (`127.0.0.1`, `[::1]`, `localhost`) | not required | not required | starts; nothing changes |
| anything else | **required** | — | refuses without it (`MAS-7010`) |
| anything else | configured | absent, and no proxy declared | refuses (`MAS-7011`) |
| anything else | configured | served, or proxy declared | starts |

Nobody has to set a flag to be safe on a laptop, and nobody can be unsafe off it
by forgetting one. The check runs at admission and stops the process, because a
warning at startup is read once, by a log nobody is watching, while a refusal is
read at the moment someone is looking.

The plaintext row is the one that needs its own justification. A bearer token
over HTTP is a credential on the wire, so shipping bearer auth without TLS would
be building something that looks secure and is not. But requiring the server to
terminate TLS itself is wrong for the deployment most of its users have, where
an ingress already does. The tool cannot see that ingress. So it asks: an
operator who states `terminated_by_proxy` has recorded a fact only they can
know, and typing it is the acknowledgement.

## 3. Scopes, and why exactly two

`read` and `diagnose`, and the split is not arbitrary. `POST /api/v1/diagnoses`
spends model tokens and reads production telemetry; every other route reads
something already computed. A status page that renders the last diagnosis needs
the second and must not have the first — and today the only way to give it
anything is to give it everything.

Finer authorisation — this token may diagnose *these* targets — is deliberately
absent. It needs a tenancy model, which is P3-3's multi-tenant registry. Adding
half of one here would produce a per-target check that looks like tenancy and
isn't, and the first person to rely on it would be wrong about what they had.

Denial is the default in both directions: a route with no scope entry is
refused, and a token whose configuration names a scope this build does not
recognise fails at load rather than being ignored. An ignored scope is an
authorisation the operator believes they have granted.

## 4. Who asked

Authorisation without attribution is half a feature. A run record already says
what was diagnosed, when, by which topology and at what cost; from here it also
says **who asked**, taken from the authenticated principal rather than from
anything the client can set.

That closes the loop the problem statement opened: when a diagnosis turns out to
have been expensive, or to have read a system it should not have, there is
something to look at. It is also why the audit log records the decision and not
just the failures — a report that only shows refusals cannot answer "who ran
this", which is the question actually asked afterwards.

## 5. What this deliberately does not do

- **No identity provider.** OIDC and JWT both need a dependency this project
  does not take, or a partial reimplementation — and a partial JWT verifier is a
  vulnerability with a friendly name. Tokens are configuration, which is where
  an operator already manages secrets.
- **No runtime token management.** Creating and revoking tokens through the API
  would make the API a credential store, which is a different product with
  different obligations.
- **No rate limiting.** Worth having, and a different concern; a run's budget
  already bounds what one caller can spend per diagnosis.
- **No authentication on the CLI.** It runs as the operator, with the operator's
  own configuration and credentials. Adding a login to a local binary would be
  ceremony with no boundary behind it.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial high-level design | LLD, tasks |
