# Low-Level Design (LLD): A Tenant Dimension on Targets and Credentials

> **Feature ID**: `011-tenant-registry` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

```
internal/config/
  config.go     + TargetConfig.Tenant, APIToken.Tenants
  validate.go   + the load-time tenancy rules
  tenancy.go    new: Tenants(), MultiTenant()
internal/httpapi/
  auth.go       + Principal.Tenants, and the one reachability function
  server.go     the three paths that name or return a target
internal/core/
  model.go      + DiagnoseRequest.Tenant, RunRecord.Tenant
internal/service/
  service.go    the tenant travels onto the run record
  doctor.go     + the tenancy check
pkg/errs/
  registry.go   MAS-1013, MAS-7015
```

## 2. Configuration

```yaml
targets:
  - id: payments-redis
    kind: redis
    tenant: payments
  - id: search-kafka
    kind: kafka
    tenant: search

server:
  auth:
    tokens:
      - name: payments-oncall
        token: "${env:PAYMENTS_TOKEN}"
        scopes: [read, diagnose]
        tenants: [payments]
      - name: platform
        token: "${env:PLATFORM_TOKEN}"
        scopes: [read]
        tenants: [payments, search]
```

```go
type TargetConfig struct {
    // …
    Tenant string `yaml:"tenant" json:"tenant,omitempty"`
}

type APIToken struct {
    // …
    Tenants []string `yaml:"tenants" json:"tenants,omitempty"`
}
```

## 3. When tenancy is on

```go
// MultiTenant reports whether any target names a tenant.
func (c *Config) MultiTenant() bool

// Tenants lists the declared tenants, sorted.
func (c *Config) Tenants() []string
```

There is no flag. A configuration is multi-tenant the moment any target names a
tenant, and the load-time rules follow (HLD §2):

| Condition | Result |
|---|---|
| `MultiTenant()` and a target has no tenant | `MAS-1013` naming the target |
| `MultiTenant()` and a credential has no tenants | `MAS-1013` naming the credential |
| A credential names a tenant no target declares | `MAS-1013` naming both |
| Not `MultiTenant()` and a credential names tenants | `MAS-1013`: the tenants would be silently ignored, which is an authorisation the operator believes they narrowed |

The last row matters as much as the others. A `tenants:` list that has no effect
because no target is tenanted is exactly the shape of a security control that
looks applied and is not.

## 4. The principal and the one function

```go
type Principal struct {
    Name    string
    Scopes  map[Scope]bool
    Tenants map[string]bool   // empty ⇒ unrestricted, only legal when tenancy is off
}

// MayReach reports whether this principal may act on a target.
//
// The only place tenancy is decided. A handler that compared tenants for itself
// would be a handler that gets copied without the comparison, which is the
// defect this project has fixed four times in other guises.
func (s *Server) MayReach(p Principal, targetID string) bool
```

`MayReach` resolves the target's tenant from configuration and returns true when
the principal holds it. With tenancy off, every principal reaches everything,
which is today's behaviour exactly.

`TestTenancyIsEnforcedInOnePlace` parses `internal/httpapi` and asserts that no
handler reads `Principal.Tenants` or a target's `Tenant` directly: everything
goes through `MayReach`.

## 5. The three paths

| Path | Behaviour |
|---|---|
| `POST /api/v1/diagnoses` | Not reachable ⇒ `404` with `MAS-7404`, identical to an id that was never configured |
| `GET /api/v1/targets` | Filtered to what the principal reaches |
| `GET /api/v1/diagnoses` | Filtered by the run's recorded tenant |
| `GET /api/v1/diagnoses/{id}` | Another tenant's run ⇒ `404`, same as an unknown id |

The 404 is the point (HLD §3). A `403` naming the target confirms it exists,
which is the neighbour's information rather than the caller's, and it leaks once
per guessed id. `TestCrossTenantRefusalRevealsNothing` compares the two
responses byte for byte.

`MAS-7015` exists for the audit log, where the distinction is real and safe:
"denied: tenant" is what an operator debugging a filtered listing needs, and it
never reaches the wire.

## 6. The tenant on the run

`core.DiagnoseRequest` gains `Tenant string`, set by the handler from
configuration — never from the body, for the same reason `Principal` is not.
`core.RunRecord` gains it too, written at admission.

Recorded rather than derived: reading the target's tenant at query time answers
*which tenant owns that target now*, and configuration changes while audits ask
about the past.

## 7. `mas doctor`

One line: whether tenancy is on, how many tenants are declared, and each
credential's reach by name — `payments-oncall → payments`. Never a token.

When tenancy is off it says so, because "no tenants configured" is what an
operator debugging an unfiltered listing needs to see.

## 8. Errors

| Code | Meaning |
|---|---|
| `MAS-1013` | The tenancy configuration is inconsistent. `Config.Validate` aggregates, so it reaches an operator inside `MAS-1003` with the message intact, as every other coded configuration problem already does |
| `MAS-7015` | The credential may not act for this target's tenant (audit log only) |

## 9. Tests

| Test | What it pins |
|---|---|
| `TestTargetsCarryATenant` | FR-001 |
| `TestTenancyOffChangesNothing` | FR-002, NFR-003 |
| `TestPartialTenancyIsRefused` | FR-003 |
| `TestCredentialWithoutTenantsIsRefused` | FR-004 |
| `TestDiagnosingAnotherTenantIsRefused` | FR-005 |
| `TestTargetListingIsTenantScoped` | FR-006 |
| `TestRunAccessIsTenantScoped` | FR-007 |
| `TestRunRecordCarriesTheTenant` | FR-008 |
| `TestCrossTenantRefusalRevealsNothing` | FR-009, CON-003 |
| `TestTenancyIsEnforcedInOnePlace` | FR-010, CON-002 |
| `TestDoctorReportsTenancy` | FR-011 |
| `TestCredentialNamingAnUnknownTenantIsRefused` | FR-012 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial low-level design | tasks, code |
