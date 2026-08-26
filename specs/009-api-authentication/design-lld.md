# Low-Level Design (LLD): Authentication and Authorisation on the HTTP API

> **Feature ID**: `009-api-authentication` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

```
internal/config/
  config.go     + ServerConfig.Auth, ServerConfig.TLS
  validate.go   + scope and token validation at load
internal/httpapi/
  auth.go       new: Authorizer, the route table, the middleware
  admission.go  new: the bind-address rules, run before the listener opens
  server.go     the mux is wrapped; handlers read the principal from context
internal/core/
  model.go      + DiagnoseRequest.Principal, RunRecord.Principal
pkg/errs/
  registry.go   MAS-7010…MAS-7014
```

## 2. Configuration

```yaml
server:
  addr: "0.0.0.0:8080"
  auth:
    tokens:
      - name: dashboard          # the principal, and what an audit line names
        token: "${MAS_DASHBOARD_TOKEN}"
        scopes: [read]
      - name: oncall
        token: "file:/etc/mas/oncall.token"
        scopes: [read, diagnose]
  tls:
    cert_file: /etc/mas/tls.crt
    key_file: /etc/mas/tls.key
    terminated_by_proxy: false
```

```go
type ServerConfig struct {
    Addr         string      `yaml:"addr"`
    ReadTimeout  Duration    `yaml:"read_timeout"`
    WriteTimeout Duration    `yaml:"write_timeout"`
    Auth         ServerAuth  `yaml:"auth"`
    TLS          ServerTLS   `yaml:"tls"`
}

type ServerAuth struct {
    Tokens []APIToken `yaml:"tokens"`
}

type APIToken struct {
    Name   string   `yaml:"name"`
    Token  Secret   `yaml:"token"`
    Scopes []string `yaml:"scopes"`
}

type ServerTLS struct {
    CertFile          string `yaml:"cert_file"`
    KeyFile           string `yaml:"key_file"`
    TerminatedByProxy bool   `yaml:"terminated_by_proxy"`
}
```

`Token` is a `Secret`, so it is already unprintable in logs, `mas config` and
JSON, and it already resolves `${ENV}` and `file:` references (FR-004).

Validation at load (FR-013):

- a token with no `name`, or two tokens with the same name → `MAS-1003`;
- a token with no scopes → `MAS-7013`: a credential that can do nothing is
  almost always a mistake in a list where the others can;
- a scope this build does not recognise → `MAS-7013`. An ignored scope is an
  authorisation the operator believes they granted;
- `cert_file` without `key_file`, or the reverse → `MAS-1003`.

## 3. Admission

```go
// Admit reports why this configuration must not open a listener, or nil.
func Admit(cfg config.ServerConfig) error
```

Run from `Serve` before the listener opens, and separately testable:

```go
switch {
case isLoopback(cfg.Addr):
    return nil                      // the host is the boundary
case len(cfg.Auth.Tokens) == 0:
    return errs.New("MAS-7010", cfg.Addr)
case !cfg.TLS.Enabled() && !cfg.TLS.TerminatedByProxy:
    return errs.New("MAS-7011", cfg.Addr)
}
return nil
```

`isLoopback` resolves the host half of the address: empty host and `0.0.0.0` and
`::` are **not** loopback, `127.0.0.0/8`, `::1` and `localhost` are. A hostname
that does not resolve is treated as non-loopback — the safe direction, and the
one that produces a legible error rather than a silent exposure.

## 4. The authorizer

```go
type Scope string

const (
    ScopeRead     Scope = "read"
    ScopeDiagnose Scope = "diagnose"
)

// Principal is who the request is from.
type Principal struct {
    Name   string
    Scopes map[Scope]bool
}

type Authorizer struct {
    tokens map[string]Principal // digest → principal
    routes map[string]Scope     // route pattern → required scope
    on     bool                 // false when no tokens are configured
}
```

**Lookup is by digest.** Tokens are stored as `sha256` of the secret and the
presented credential is hashed the same way, so `subtle.ConstantTimeCompare`
runs over two fixed-size arrays and length never branches (RSK-4). The map
lookup itself is not constant-time with respect to the digest, which reveals
nothing about the token: a digest is not reversible, and an attacker who has one
already has the token.

**The route table is exhaustive and deny-by-default.**

| Route | Scope |
|---|---|
| `POST /api/v1/diagnoses` | `diagnose` |
| `GET /api/v1/diagnoses`, `/api/v1/diagnoses/…` | `read` |
| `GET /api/v1/targets`, `/topologies`, `/packs` | `read` |
| `GET /metrics` | `read` |
| `/healthz`, `/readyz` | *anonymous by design* |
| anything else | **refused** |

A route with no entry is refused rather than allowed, so adding a handler
without wiring its scope fails closed (RSK-1). `TestEveryRouteIsGuarded` walks
the registered patterns and asserts each is either in the table or in the
anonymous set.

**The middleware** wraps the whole mux:

```go
func (a *Authorizer) Wrap(next http.Handler) http.Handler
```

1. anonymous route → straight through;
2. authorisation off (no tokens, loopback) → through, with `Principal{Name: "anonymous"}`;
3. no or malformed `Authorization: Bearer …` → `401`, `MAS-7012`;
4. unknown token → `401`, `MAS-7012`. Deliberately the same code and body as (3):
   distinguishing "no token" from "wrong token" tells an attacker which half to
   work on;
5. known token without the scope → `403`, `MAS-7014`;
6. otherwise → `next`, with the principal in the request context.

Every decision is logged at info with the principal name, the route and the
outcome — never the credential, and never the `Authorization` header (FR-012).
Refusals *and* grants: a log that shows only refusals cannot answer "who ran
this", which is the question actually asked afterwards.

## 5. The principal on the run

`core.DiagnoseRequest` gains `Principal string`, set by the handler from the
context and **never** from the request body — a client-supplied principal is an
attribution anyone can forge. `core.RunRecord` gains the same field, and the
service copies it from the request.

The CLI leaves it empty, which renders as "local" wherever a record is shown: a
run from the CLI was made by whoever could run the binary, and inventing a name
for that would be a fact the system does not have.

## 6. TLS

`Serve` uses `ListenAndServeTLS` when `cert_file` and `key_file` are set, and
`ListenAndServe` otherwise — which admission has already established is either
loopback or behind a declared proxy. `MinVersion: tls.VersionTLS12`.

## 7. `mas doctor`

A new section: the bind address, whether it is loopback, how many tokens are
configured and their scopes by name, whether TLS is served or declared
terminated. No token, no digest, no length.

## 8. Errors

| Code | Meaning |
|---|---|
| `MAS-7010` | The API would bind %s with no authentication configured |
| `MAS-7011` | The API would serve credentials over plaintext on %s |
| `MAS-7012` | The request carried no usable credential |
| `MAS-7013` | An API token declares no scopes, or a scope this build does not know |
| `MAS-7014` | The credential lacks the scope this route requires |

## 9. Tests

| Test | What it pins |
|---|---|
| `TestAnonymousRequestIsRefused` | FR-001 |
| `TestScopeIsEnforcedPerRoute` | FR-002 |
| `TestTokenComparisonIsConstantTime` | FR-003, structurally |
| `TestCredentialsAreNeverEchoed` | FR-004, CON-004 |
| `TestHealthEndpointsStayAnonymous` | FR-005 |
| `TestEveryRouteIsGuarded` | FR-006, CON-001, CON-002 |
| `TestUnauthenticatedPublicBindIsRefused` | FR-007 |
| `TestPlaintextCredentialsOffHostAreRefused` | FR-008, CON-003 |
| `TestLoopbackNeedsNoConfiguration` | FR-009, NFR-004 |
| `TestServesTLSDirectly` | FR-010 |
| `TestRunRecordCarriesThePrincipal` | FR-011 |
| `TestAuthDecisionsAreAudited` | FR-012 |
| `TestScopelessOrUnknownScopeIsRejectedAtLoad` | FR-013 |
| `TestDoctorReportsAPIExposure` | FR-014 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial low-level design | tasks, code |
