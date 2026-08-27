# Feature Specification: A Tenant Dimension on Targets and Credentials

> **Feature ID**: `011-tenant-registry` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.2.1
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

Feature 009 gave the API credentials and two scopes, and said plainly what it
was leaving out: *a credential that may diagnose may diagnose any configured
target*. That is fine for one team looking after its own estate and wrong for
every arrangement where the estate is not one team's.

A platform team runs one MAS-Turbo for several product teams. Today each of
those teams either gets a token that can diagnose — and therefore read the
telemetry and stored diagnoses of every other team's middleware — or gets
nothing and runs its own copy, with its own credentials, its own knowledge
packs drifting from everyone else's, and its own bill.

The same gap shows up inside one organisation whenever a boundary exists that
is not the whole deployment: staging and production in one config, a regulated
workload beside an unregulated one, a customer-facing cluster beside an internal
one. In each case what the operator wants to say is "this credential is for
these targets", and there is currently no word for that.

`targets:` is a flat list with no dimension to divide it on, so this is a
registry change before it is an authorisation change — which is why it is its
own feature rather than an amendment to 009.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| Platform engineer | Serve several teams from one deployment without any of them reading another's telemetry | Onboarding a second team |
| Team engineer | See exactly their own targets, and be sure the list is not filtered by mistake | Every day |
| Auditor | Show that a credential could not have read a given tenant's runs | A compliance question |
| Single-team operator | Not have to learn any of this | Every day |

## 3. Scope

### In scope
- A `tenant` on a target, and a `tenants` list on an API credential.
- **Tenancy off by default**: a configuration that names no tenant behaves
  exactly as it does today.
- **Deny by default once on**: if any target declares a tenant, every target
  must, and every credential must declare which tenants it may act for.
- Enforcement on every path that names or returns a target: starting a
  diagnosis, listing targets, listing and reading runs.
- The tenant recorded on the run, beside the principal.
- `mas doctor` reporting the tenancy state and what each credential can reach.
- Bilingual errors and documentation.

### Out of scope
- Per-tenant configuration of anything else: models, budgets, knowledge packs,
  telemetry sources. Each is a real request and each needs its own answer; a
  tenant that silently changed a model would be a surprise nobody asked for.
- Tenant-scoped storage layout. Runs stay in one place with a tenant recorded
  on them; splitting the bucket is a retention decision, not an access one.
- Tenancy on the CLI. It runs as the operator, with the operator's own
  configuration — every target in the file is already theirs.
- Delegation, hierarchies or groups. One flat set of names, because the first
  version of a tenancy model that has hierarchies is a tenancy model nobody can
  reason about.
- Quotas or per-tenant cost limits. Worth having, and a different concern.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | A target MAY declare a `tenant` | P0 | `TestTargetsCarryATenant` |
| FR-002 | A configuration naming no tenant MUST behave exactly as before | P0 | `TestTenancyOffChangesNothing` |
| FR-003 | If any target declares a tenant, every target MUST, or the configuration is refused | P0 | `TestPartialTenancyIsRefused` |
| FR-004 | With tenancy on, a credential declaring no tenants MUST be refused at load | P0 | `TestCredentialWithoutTenantsIsRefused` |
| FR-005 | Starting a diagnosis on a target outside the principal's tenants MUST be refused with a code | P0 | `TestDiagnosingAnotherTenantIsRefused` |
| FR-006 | Listing targets MUST return only the principal's tenants | P0 | `TestTargetListingIsTenantScoped` |
| FR-007 | Listing and reading runs MUST be tenant-scoped | P0 | `TestRunAccessIsTenantScoped` |
| FR-008 | A run MUST record the tenant it was for | P0 | `TestRunRecordCarriesTheTenant` |
| FR-009 | Refusing another tenant's target MUST NOT reveal whether it exists | P0 | `TestCrossTenantRefusalRevealsNothing` |
| FR-010 | Enforcement MUST happen at one choke point, not in each handler | P0 | `TestTenancyIsEnforcedInOnePlace` |
| FR-011 | `mas doctor` MUST report the tenancy state and each credential's reach | P1 | `TestDoctorReportsTenancy` |
| FR-012 | An unknown tenant on a credential MUST be refused at load | P1 | `TestCredentialNamingAnUnknownTenantIsRefused` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | No new module dependency | `go.mod` unchanged |
| NFR-002 | Every operator-facing string bilingual | `sddctl verify` |
| NFR-003 | The single-tenant deployment gains no configuration and no ceremony | `TestTenancyOffChangesNothing`, `make demo` |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Deny by default once tenancy is on | Constitution Art. VII |
| CON-002 | One choke point; no handler filters for itself | Art. VII.2; feature 009 precedent |
| CON-003 | A refusal must not leak the existence of another tenant's target | §1 |
| CON-004 | Both languages for every message | Art. III |

## 7. Acceptance

The feature is done when a target can name a tenant, a credential can name the
tenants it may act for, a request for someone else's target is refused without
revealing whether it exists, listings show only what the caller may see, the run
records which tenant it was for, and a deployment that names no tenant is
untouched.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial specification | plan, HLD, LLD, tasks, code |
