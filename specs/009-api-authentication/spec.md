# Feature Specification: Authentication and Authorisation on the HTTP API

> **Feature ID**: `009-api-authentication` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.9
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

`mas serve` binds an unauthenticated API. Anyone who can reach the port can list
every configured target, read every stored diagnosis, and — the part that
matters — `POST /api/v1/diagnoses`, which spends model tokens and reads the
operator's production telemetry.

The read-only guarantee makes this less dangerous than it would otherwise be:
nobody can use the API to break a cluster. It does not make it safe. A stored
diagnosis contains metric values, log lines and cluster topology from a
production system; the target list is a map of that estate; and an unauthorised
caller can run up a model bill for as long as the port is open.

There is a second problem underneath, and it is the one that makes the first
easy to get wrong. The API today has **no notion of who is calling**, so a run
record cannot say who asked for it. When a diagnosis turns out to have been
expensive, or to have read a system it should not have, there is nothing to look
at.

Both are M4's first ranked item, and its exit criterion names the first
explicitly: *API authenticated*.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| Platform engineer | Expose the API to a team without exposing it to the network | First deployment beyond a laptop |
| SRE | Give a dashboard read access without letting it spend model tokens | Wiring a status page |
| Security reviewer | Confirm the service cannot be reached anonymously off-host | Review before rollout |
| On-call engineer | See who asked for an expensive diagnosis | Cost review |
| Developer | Keep running `mas serve` locally with no ceremony | Every day |

## 3. Scope

### In scope
- **Bearer token** authentication, tokens supplied as configuration secrets.
- **Scopes**: `read` and `diagnose`, checked per route, denied by default.
- A **refusal to start** when the configuration would expose an unauthenticated
  or plaintext-credentialled API off-host.
- **TLS** served directly, or an explicit statement that a proxy terminates it.
- The authenticated principal recorded on the run it caused.
- Health endpoints that never require credentials.
- Bilingual errors, documentation and configuration reference.

### Out of scope
- OIDC, JWT verification, or any identity provider integration. Each needs a
  dependency or a partial reimplementation of one, and a partial JWT verifier is
  a vulnerability with a friendly name.
- User management: creating, rotating or revoking tokens at runtime. Tokens are
  configuration, and configuration is where an operator already manages secrets.
- Per-target authorisation. A token that may diagnose may diagnose any
  configured target; splitting that is P3-3's multi-tenant registry, not this.
- Rate limiting. Worth having and a different concern; a budget already bounds
  what one run can spend.
- Authentication on the CLI. It runs as the operator, with the operator's
  configuration and credentials.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | A request without a valid credential MUST be refused with `401` and a code | P0 | `TestAnonymousRequestIsRefused` |
| FR-002 | A valid credential lacking the route's scope MUST be refused with `403` and a code | P0 | `TestScopeIsEnforcedPerRoute` |
| FR-003 | Tokens MUST be compared in constant time | P0 | `TestTokenComparisonIsConstantTime` |
| FR-004 | A token MUST never appear in a log, an error body, or `mas config` output | P0 | `TestCredentialsAreNeverEchoed` |
| FR-005 | `/healthz` and `/readyz` MUST NOT require a credential | P0 | `TestHealthEndpointsStayAnonymous` |
| FR-006 | Every other route MUST pass through the same authorisation choke point | P0 | `TestEveryRouteIsGuarded` |
| FR-007 | Binding a non-loopback address without authentication MUST refuse to start | P0 | `TestUnauthenticatedPublicBindIsRefused` |
| FR-008 | Serving credentials over plaintext off-host MUST refuse to start unless the operator states a proxy terminates TLS | P0 | `TestPlaintextCredentialsOffHostAreRefused` |
| FR-009 | Loopback binds MUST keep working with no configuration | P0 | `TestLoopbackNeedsNoConfiguration` |
| FR-010 | TLS MUST be servable directly, from a certificate and key in configuration | P1 | `TestServesTLSDirectly` |
| FR-011 | The authenticated principal MUST be recorded on the run it caused | P0 | `TestRunRecordCarriesThePrincipal` |
| FR-012 | An authorisation decision MUST be logged with the principal and the outcome, never the credential | P1 | `TestAuthDecisionsAreAudited` |
| FR-013 | A token with no scopes, or an unknown scope, MUST be rejected at load | P0 | `TestScopelessOrUnknownScopeIsRejectedAtLoad` |
| FR-014 | `mas doctor` MUST report the API's exposure and what protects it | P1 | `TestDoctorReportsAPIExposure` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | No new module dependency | `go.mod` unchanged |
| NFR-002 | Every operator-facing string bilingual | `sddctl verify` |
| NFR-003 | Authorisation adds no per-request allocation beyond the lookup | Benchmark-free structural review |
| NFR-004 | The demo and every existing test keep working unchanged | `make demo`, `go test ./...` |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Deny by default: an unlisted route or an unknown scope is refused | Constitution Art. VII |
| CON-002 | One choke point; no handler may authorise for itself | Art. VII.2, guard precedent |
| CON-003 | A configuration that would expose credentials in plaintext off-host is refused, not warned about | §1 |
| CON-004 | Secrets never reach a log or a rendered configuration | Art. VIII |
| CON-005 | Both languages for every message | Art. III |

## 7. Acceptance

The feature is done when an off-host bind refuses to start without
authentication and TLS, a request without a credential is refused, a credential
without the scope is refused, health checks still answer anonymously, the run
record names who asked, no token ever reaches a log, and the loopback developer
workflow is unchanged.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial specification | plan, HLD, LLD, tasks, code |
