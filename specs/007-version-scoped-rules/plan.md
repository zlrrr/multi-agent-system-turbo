# Implementation Plan: Version-Scoped Pack Rules

> **Feature ID**: `007-version-scoped-rules` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

The mechanism is small. What makes this feature worth designing rather than
writing is that there are three plausible shapes and two of them rot.

**Shape 1 — filter at every lookup.** Give `Signal(id)`, `MatchingPlaybooks`,
`MatchLogPatterns`, `FailureMode` and `InspectCommands` a version argument and
filter inside each. Rejected: five places must remember, a caller that passes
`""` silently gets unscoped behaviour, and every future consumer inherits the
obligation. This project has found the same class of defect four times — an
authorization or a scoping rule that depended on the caller doing something —
and each time the fix was to remove the choice.

**Shape 2 — resolve into a new type.** `Pack.Resolve(v) *ResolvedPack`, where
only `*ResolvedPack` has the accessor methods, so passing an unresolved pack to
the engine is a compile error. Strongest guarantee, and rejected on cost: every
signature that mentions `*knowledge.Pack` changes, across the service, the rules
engine, the agent prompts and the CLI, for a property one test can assert.

**Shape 3 — resolve into the same type, once, at admission.** `Pack.Resolve(v)`
returns a `*Pack` whose slices are already filtered and whose metadata records
what it was resolved for. Call sites do not change. The service resolves once
and uses the result for the rest of the run. A structural test asserts the
service reaches playbooks only through a resolved pack, and `Resolve` on an
unscoped pack is the identity, so the corpus is the regression proof.

Shape 3 is what we build. It buys most of shape 2's guarantee for none of its
churn, and the guarantee it gives up is recovered by a test rather than by the
type system — which is the trade this codebase already makes for the guard's
"only authorizer" property.

## 2. Design decisions

| ID | Decision | Rationale |
|---|---|---|
| D-1 | One optional `versionRange` field per rule, same syntax as the pack's | A second syntax would be a second parser and a second set of mistakes |
| D-2 | Resolution once per run, at admission, not per lookup | A rule can then be dropped transitively; per-lookup filtering has no view of what depended on it |
| D-3 | Duplicate ids allowed only with non-overlapping ranges | This is what expresses a rename. Without it a rename forces a second pack |
| D-4 | Overlap is checked at load, not at resolution | A pack that is ambiguous for *some* version is broken for everyone, and finding out during an incident is the worst time |
| D-5 | Unknown version drops variants and records a gap | Picking the first variant would answer with a metric name that may not exist, and call it a measurement. Refusing and saying so is the only honest option |
| D-6 | Unknown version **keeps** singly-declared scoped rules | An out-of-range rule reads a metric that does not exist and is already recorded as a gap by the engine. Dropping it too would lose a check we might have been able to make, for no gain |
| D-7 | Dropping is transitive across signal → step → slot → playbook | A step referencing `{{signal:x}}` after `x` was dropped is a runtime error; a step reading a slot no surviving step produces is an undefined-variable error. Both are the same mistake made at load rather than at 3am |
| D-8 | Every drop is a gap, never a silent omission | CON-002. The corpus already proves that a silently-skipped check reads as a passed one |
| D-9 | Only rules whose version boundary is a documented fact are scoped in the shipped packs | A plausible-looking range invented from memory is worse than none: it would silently remove a working check |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-1 | Transitive dropping removes more than intended and a diagnosis quietly loses checks | Every drop is a gap with the rule id; a test asserts a dropped signal's dependents are named |
| RSK-2 | Overlap detection is wrong and a real rename is rejected | Overlap is computed on intervals with a conservative bias: when unsure, report overlap. A false rejection is a load error the author sees immediately; a false acceptance is an ambiguity nobody sees |
| RSK-3 | Resolution is forgotten somewhere and scoping is bypassed | Structural test (FR-012); `resolvedFor` on the resolved pack makes it observable |
| RSK-4 | The shipped packs are scoped from half-remembered facts | D-9: only documented boundaries, and the case corpus covers the one we ship |

## 4. Sequencing

1. `versionRange` on every rule kind, parsed and validated, no behaviour change.
2. Overlap detection and the variant rule, at load.
3. `Resolve`, with transitive dropping and gaps.
4. The service resolves once; structural test.
5. Kafka's ZooKeeper and KRaft boundaries; a corpus case on each side.
6. `mas packs --show --version`; bilingual documentation.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial plan | HLD, LLD, tasks |
