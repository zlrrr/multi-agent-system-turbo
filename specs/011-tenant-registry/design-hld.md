# High-Level Design (HLD): A Tenant Dimension on Targets and Credentials

> **Feature ID**: `011-tenant-registry` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
  config ──► targets[].tenant ─────────┐
             auth.tokens[].tenants ──┐ │
                                     ▼ ▼
  request ──► authorise (009) ──► Principal{Name, Scopes, Tenants}
                                     │
                                     ▼
                              mayReach(target)  ── one function, every path
                                     │
              ┌──────────────────────┼──────────────────────┐
              ▼                      ▼                      ▼
      POST /diagnoses          GET /targets           GET /diagnoses[/id]
      404 if not reachable     filtered listing       filtered listing
```

Nothing new sits in the request path: the tenant set rides on the principal
feature 009 already resolves, and one function answers the only question there
is. That is deliberate — a second choke point would be a second thing to
remember, and the first handler written after this feature would forget it.

## 2. Tenancy turns itself on

A flag would be the obvious way to enable this, and it has two failure modes and
no upside. Off with tenants configured is a deployment that looks partitioned
and is not. On with no tenants is a configuration error that surfaces as an
empty list nobody can explain.

So there is no flag. **A configuration is multi-tenant the moment any target
names a tenant**, and the rules follow from that at load time:

| Configuration | Result |
|---|---|
| No target names a tenant | Tenancy off; identical to today, and nothing new to configure |
| Some targets name one, some do not | Refused. A target belonging to nobody is one that everyone or no one can reach, and either answer is a guess |
| Every target names one, a credential does not | Refused. An unrestricted credential in a partitioned deployment is a superuser nobody declared |
| Every target and every credential name theirs | Tenancy on |

This is the shape feature 009 used for the bind address, for the same reason:
the requirement follows from what the configuration says rather than from a
separate statement of intent, so it cannot be true and unenforced at once.

The single-team deployment — most of them — never encounters any of this.

## 3. A refusal that does not answer the question

Refusing a cross-tenant target with "you may not access `payments-redis`"
confirms `payments-redis` exists. That is precisely the fact the other tenant
was relying on not sharing, and it leaks from every id an attacker cares to
guess.

So from where the caller stands, another tenant's target **does not exist**:
same status, same code, same body as an id that was never configured. The
distinction is real inside the process and invisible outside it, which is the
only place it can safely live.

The same reasoning applies to runs. A run id belonging to another tenant is not
found, not forbidden.

## 4. The tenant on the run

A run record already says who asked. From here it also says **which tenant it
was for**, recorded at admission rather than derived later.

Deriving it later would mean looking up the target's tenant at read time, which
answers a different question: *which tenant does that target belong to now*.
Configuration changes; audits ask about the past. Writing it down when it
happens is the only version that stays true.

## 5. What this deliberately does not do

- **No per-tenant anything else.** Models, budgets, packs and telemetry sources
  stay global. Each of those is a reasonable request with its own answer, and a
  tenant that silently changed the model would be a surprise nobody asked for.
- **No hierarchies, groups or delegation.** One flat set of names. A tenancy
  model that has hierarchies in its first version is one nobody can reason
  about, and unlike a missing feature, it cannot be removed later.
- **No tenant-scoped storage layout.** Runs stay in one place with the tenant
  recorded on them. Splitting the bucket is a retention decision, and conflating
  it with an access decision makes both harder to change.
- **No tenancy on the CLI.** It runs as the operator, with the operator's own
  configuration file. Every target in that file is already theirs, and a
  partition inside one's own file protects nobody.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial high-level design | LLD, tasks |
