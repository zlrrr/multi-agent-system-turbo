# Task Breakdown: A Tenant Dimension on Targets and Credentials

> **Feature ID**: `011-tenant-registry` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.
Every test named here must exist: `sddctl verify` checks it.

## Phase A — the registry

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| TA01 | `TargetConfig.Tenant`, `APIToken.Tenants`, `MultiTenant()`, `Tenants()` | FR-001 | `TestTargetsCarryATenant` | — | done |
| TA02 | Tenancy off leaves every existing behaviour untouched | FR-002, NFR-003 | `TestTenancyOffChangesNothing` | TA01 | done |
| TA03 | Partial tenancy is refused at load | FR-003 | `TestPartialTenancyIsRefused` | TA01 | done |
| TA04 | A credential without tenants, or with an unknown one, is refused | FR-004, FR-012, CON-001 | `TestCredentialWithoutTenantsIsRefused`, `TestCredentialNamingAnUnknownTenantIsRefused` | TA03 | done |
| TA05 | Error codes `MAS-1013`, `MAS-7015`, bilingual, docs regenerated | CON-004 | `mas errcodes` output current | TA03 | done |
| **G-A** | **Gate A** | | An inconsistent tenancy cannot load | | done |

## Phase B — enforcement

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| TB01 | `Principal.Tenants` and `MayReach`, the one place tenancy is decided | FR-010, CON-002 | `TestTenancyIsEnforcedInOnePlace` | G-A | todo |
| TB02 | Diagnosing another tenant's target is refused | FR-005 | `TestDiagnosingAnotherTenantIsRefused` | TB01 | todo |
| TB03 | The refusal is indistinguishable from an unknown target | FR-009, CON-003 | `TestCrossTenantRefusalRevealsNothing` | TB02 | todo |
| TB04 | Target listing is tenant-scoped | FR-006 | `TestTargetListingIsTenantScoped` | TB01 | todo |
| TB05 | Run listing and reading are tenant-scoped | FR-007 | `TestRunAccessIsTenantScoped` | TB01 | todo |
| **G-B** | **Gate B** | | `go test ./internal/httpapi/...` | | todo |

## Phase C — attribution and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| TC01 | The tenant is recorded on the run at admission | FR-008 | `TestRunRecordCarriesTheTenant` | G-B | todo |
| TC02 | `mas doctor` reports tenancy and each credential's reach | FR-011 | `TestDoctorReportsTenancy` | TC01 | todo |
| TC03 | Bilingual documentation: configuration reference, manual, README | NFR-002, NFR-001 | `sddctl verify` parity; `go.mod` unchanged | TC02 | todo |
| **G-C** | **Gate C — feature exit** | | `make ci` green; `make demo` unchanged | | todo |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | TA01–TA05 | `go test ./internal/config/...` |
| G-B | TB01–TB05 | `go test ./internal/httpapi/...` |
| G-C | TC01–TC03 | `make ci && make demo` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial task breakdown | code, config, docs |
