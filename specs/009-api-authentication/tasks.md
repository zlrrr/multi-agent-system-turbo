# Task Breakdown: Authentication and Authorisation on the HTTP API

> **Feature ID**: `009-api-authentication` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.
Every test named here must exist: `sddctl verify` checks it.

## Phase A — configuration and admission

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T801 | `ServerAuth`, `APIToken`, `ServerTLS`; tokens are `Secret` | FR-004, CON-004 | `TestCredentialsAreNeverEchoed` | — | done |
| T802 | Scope and token validation at load | FR-013, CON-001 | `TestScopelessOrUnknownScopeIsRejectedAtLoad` | T801 | done |
| T803 | `Admit`: a non-loopback bind with no authentication refuses to start | FR-007 | `TestUnauthenticatedPublicBindIsRefused` | T802 | done |
| T804 | `Admit`: plaintext credentials off-host refuse unless a proxy is declared | FR-008, CON-003 | `TestPlaintextCredentialsOffHostAreRefused` | T803 | done |
| T805 | A loopback bind still needs no configuration at all | FR-009, NFR-004 | `TestLoopbackNeedsNoConfiguration` | T803 | done |
| T806 | Error codes `MAS-7010`…`MAS-7014`, bilingual, docs regenerated | CON-005 | `mas errcodes` output current | T802 | done |
| **G-A** | **Gate A** | | A dangerous configuration cannot open a listener | | done |

## Phase B — the choke point

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T810 | `Authorizer` with digest lookup and constant-time comparison | FR-003 | `TestTokenComparisonIsConstantTime` | G-A | done |
| T811 | The route table, exhaustive and deny-by-default | FR-006, CON-001, CON-002 | `TestEveryRouteIsGuarded` | T810 | done |
| T812 | 401 for a missing or unknown credential, with one code for both | FR-001 | `TestAnonymousRequestIsRefused` | T811 | done |
| T813 | 403 when the credential lacks the route's scope | FR-002 | `TestScopeIsEnforcedPerRoute` | T812 | done |
| T814 | Health endpoints answer without a credential | FR-005 | `TestHealthEndpointsStayAnonymous` | T811 | done |
| T815 | Every decision audited with the principal, never the credential | FR-012 | `TestAuthDecisionsAreAudited` | T813 | done |
| **G-B** | **Gate B** | | `go test ./internal/httpapi/...` | | done |

## Phase C — attribution and TLS

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T820 | The principal reaches the run record, from context and never the body | FR-011 | `TestRunRecordCarriesThePrincipal` | G-B | done |
| T821 | TLS served directly from a certificate and key | FR-010 | `TestServesTLSDirectly` | G-B | done |
| **G-C** | **Gate C** | | `go test ./internal/httpapi/... ./internal/service/...` | | done |

## Phase D — surface and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T830 | `mas doctor` reports the API's exposure and what protects it | FR-014 | `TestDoctorReportsAPIExposure` | G-C | done |
| T831 | Bilingual documentation: manual, configuration reference, README | NFR-002, NFR-001, NFR-003 | `sddctl verify` parity; `go.mod` unchanged | T830 | done |
| **G-D** | **Gate D — feature exit** | | `make ci` green; `make demo` unchanged | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T801–T806 | `go test ./internal/config/... ./internal/httpapi/...` |
| G-B | T810–T815 | `go test ./internal/httpapi/...` |
| G-C | T820–T821 | `go test ./internal/httpapi/... ./internal/service/...` |
| G-D | T830–T831 | `make ci && make demo` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial task breakdown | code, config, docs |
