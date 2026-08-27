# Implementation Plan: A Tenant Dimension on Targets and Credentials

> **Feature ID**: `011-tenant-registry` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

The mechanism is a string on a target and a list on a credential. What has to be
got right is when the mechanism turns on, and where it is enforced.

**When it turns on.** A flag would be the obvious answer and is the wrong one:
a deployment with tenants and the flag off is silently unprotected, and a
deployment with the flag on and no tenants is a configuration error nobody
finds. So tenancy turns itself on: **the moment any target names a tenant, the
configuration is a multi-tenant one**, and the rules that go with that are
enforced at load — every target must name one, and every credential must say
which tenants it may act for. Nothing to remember, nothing to forget, and the
single-team deployment never learns any of this exists.

That is the same shape feature 009 used for the API's bind address: the
requirement follows from what the configuration actually says, not from a
separate declaration of intent.

**Where it is enforced.** The same place credentials are, and for the same
reason: a handler that filters for itself is a handler that will one day be
copied without the filter. The authorizer already resolves a principal per
request; the tenant set rides on it, and one function answers "may this
principal see this target".

**What a refusal says.** Refusing another tenant's target with "you may not
access target `payments-redis`" confirms that `payments-redis` exists, which is
half of what the other tenant was trying not to tell you. A cross-tenant target
is treated as **not found**, which is what it is from where the caller stands.

## 2. Design decisions

| ID | Decision | Rationale |
|---|---|---|
| D-1 | Tenancy is on when any target names a tenant | A flag can be off with tenants configured, which is silent exposure, or on with none, which is a broken config nobody finds |
| D-2 | Once on, every target must name a tenant | A target belonging to nobody in a multi-tenant deployment is a target everyone or no one can reach, and either answer is a guess |
| D-3 | Once on, every credential must name its tenants | An unrestricted credential in a multi-tenant deployment is a superuser nobody declared |
| D-4 | A cross-tenant target is 404, not 403 | 403 confirms the target exists, which is the neighbour's information, not the caller's |
| D-5 | One flat namespace: no hierarchies, no groups | The first version of a tenancy model with hierarchies is one nobody can reason about, and it cannot be removed later |
| D-6 | Tenancy applies to the API only, never the CLI | The CLI runs as the operator with the operator's own file; every target in it is already theirs |
| D-7 | The tenant is recorded on the run beside the principal | "Which tenant was this for" is the question an audit asks first, and reconstructing it from the target later assumes the config never changed |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-1 | A new endpoint returns targets or runs without filtering | Enforcement lives in one function, and a structural test asserts no handler compares tenants for itself |
| RSK-2 | Turning tenancy on breaks a working single-tenant deployment | It cannot turn on by accident: it requires someone to write a tenant onto a target, and the load-time rules then say exactly what else is now required |
| RSK-3 | A filtered listing looks like an empty estate and someone debugs the wrong thing | `mas doctor` states the tenancy and each credential's reach, so the answer is one command away |
| RSK-4 | A refusal leaks existence through timing or a different status | Cross-tenant and absent produce the same code, the same status and the same body; a test asserts the responses are identical |

## 4. Sequencing

1. `tenant` on targets, `tenants` on credentials, and the load-time rules.
2. The tenant set on the principal, and the one function that answers the question.
3. Enforcement: diagnose, target listing, run listing and reading.
4. The tenant on the run record.
5. `mas doctor`; bilingual documentation.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial plan | HLD, LLD, tasks |
