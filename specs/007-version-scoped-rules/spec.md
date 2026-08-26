# Feature Specification: Version-Scoped Pack Rules

> **Feature ID**: `007-version-scoped-rules` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.6
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

Goal G2.3 says a pack rule may be scoped to a version range. Today only the
**pack** is: `metadata.versionRange` decides whether the whole document applies,
and everything inside it applies unconditionally.

That is the wrong granularity for how middleware actually changes. Kafka 4.0
removed ZooKeeper, so every ZooKeeper log pattern in the pack now fires against
a cluster that has none — and matching nothing is the good case; matching a
line from an unrelated component is the bad one. Kafka 3.3 added KRaft's raft
metrics, which do not exist before it, so a check reading them against 3.2
queries a metric the deployment never exports. Since feature 002's engine fix
that is recorded as a gap rather than read as zero, which is honest and still
wrong: it reports "we could not check this" for a check that was never
applicable.

The only workaround is to fork the whole pack per version range. Six packs
become twelve, every shared signal is duplicated, and the next correction has to
be made twice — which is how knowledge drifts apart.

There is a second problem underneath. Middleware renames things. The same
concept — "is the controller healthy", "how much memory is this tenant using" —
is exported under different metric names across major versions. A pack can only
give a signal one PromQL, so a rename forces a second pack with a different id,
and every playbook that referenced the signal has to be duplicated too.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| Pack author | Scope one check to the versions it applies to, without forking the pack | A version removes or adds a subsystem |
| Pack author | Give one signal two expressions across a rename | A metric is renamed in a major version |
| SRE on an old version | Not be shown gaps for checks that never applied to their deployment | Any diagnosis |
| SRE with no version configured | Be told plainly that version-specific knowledge was skipped, rather than silently losing it | Any diagnosis on a target with no version |

## 3. Scope

### In scope
- An optional `versionRange` on every rule inside a pack: signals, log patterns,
  failure modes, playbooks, playbook steps and inspect commands.
- **Variants**: the same rule id declared more than once with disjoint ranges,
  resolved to the one that applies.
- Resolution **once per run**, against the target's version, producing the pack
  the rest of the run sees.
- **Transitive** dropping: a step that depends on a dropped signal, failure mode
  or slot goes with it, rather than failing at runtime.
- A recorded gap whenever version-specific knowledge is skipped, naming what was
  skipped and why.
- Bilingual documentation and error codes.

### Out of scope
- Version **discovery**. The version comes from configuration or the environment
  adapter, as it does today.
- Ranges over anything but the middleware version — no scoping by deployment
  mode, cloud vendor or exporter version. Each would need its own vocabulary,
  and the exporter's version in particular is not the middleware's.
- Rewriting the shipped packs. This feature ships the mechanism and the rules
  whose version boundaries are documented facts; broad version-splitting of
  existing knowledge is pack work, not code work.
- A resolver that guesses. When the version is unknown and a rule has variants,
  the run says so rather than picking one.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | Every rule in a pack MAY declare a `versionRange`, using the syntax `metadata.versionRange` already uses | P0 | `TestEveryRuleKindAcceptsAVersionRange` |
| FR-002 | Resolution MUST drop rules whose range excludes the target version | P0 | `TestOutOfRangeRulesAreDropped` |
| FR-003 | A rule id MAY be declared more than once when every declaration carries a range and no two ranges overlap | P0 | `TestVariantsWithDisjointRangesAreAccepted` |
| FR-004 | Overlapping ranges on one id MUST be rejected at load, naming both | P0 | `TestOverlappingVariantsAreRejected` |
| FR-005 | Resolution MUST pick the variant whose range applies to the target version | P0 | `TestVariantMatchingTheVersionIsChosen` |
| FR-006 | When the version is unknown, a rule with variants MUST be dropped and a gap recorded, not guessed | P0 | `TestUnknownVersionDropsVariantsWithAGap` |
| FR-007 | A step referencing a dropped signal or failure mode MUST be dropped with it | P0 | `TestStepsFollowTheRulesTheyDependOn` |
| FR-008 | A step whose expression reads a slot no surviving step produces MUST be dropped | P0 | `TestStepsFollowTheSlotsTheyRead` |
| FR-009 | A playbook left with no steps that can reach a conclusion MUST be dropped | P0 | `TestEmptyPlaybooksAreDropped` |
| FR-010 | Every drop MUST produce a gap carrying a code, what was skipped and its effect on the analysis | P0 | `TestSkippedRulesAreRecordedAsGaps` |
| FR-011 | Resolution MUST NOT widen anything: no rule, and no inspect command, may become available to a version its range excludes | P0 | `TestResolutionNeverWidens` |
| FR-012 | A diagnosis MUST use the resolved pack, so version scoping cannot be bypassed by a caller that forgets | P0 | `TestDiagnosisUsesTheResolvedPack` |
| FR-013 | `mas packs --show` MUST show a rule's range, and accept a version to preview the resolution | P1 | `TestPacksCommandShowsVersionScoping` |
| FR-014 | The shipped Kafka pack MUST scope the rules whose version boundaries are documented facts | P1 | `TestKafkaPackScopesZooKeeperRules` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | Resolution happens once per run, not per lookup | Structural test |
| NFR-002 | No new module dependency | `go.mod` unchanged |
| NFR-003 | Every operator-facing string bilingual | `sddctl verify` |
| NFR-004 | An unscoped pack MUST behave exactly as it does today | The corpus, unchanged, still passes |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Resolution may only narrow; the safety guard's allow-list is unaffected either way | Constitution Art. VII |
| CON-002 | Skipped knowledge is a gap, never a silent omission | Art. IX; design-lld.md §2.12 |
| CON-003 | Rules stay data: adding a range needs no recompilation | G2.1 |
| CON-004 | Both languages for every message | Art. III |

## 7. Acceptance

The feature is done when a pack can scope any rule to a version range, a rename
can be expressed as two variants of one id, a diagnosis on a target with a
version sees exactly the applicable rules, a diagnosis on a target without one
is told what it lost, no resolution ever widens what may run, and the corpus is
unchanged and green.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial specification | plan, HLD, LLD, tasks, code |
