# Low-Level Design (LLD): Version-Scoped Pack Rules

> **Feature ID**: `007-version-scoped-rules` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

```
internal/knowledge/
  pack.go        + VersionRange on Signal, LogPattern, FailureMode,
                   Playbook, Step, Inspect; validation of ranges and variants
  resolve.go     new: Pack.Resolve, the transitive drop, the gaps
  overlap.go     new: interval form of a versionRange, and overlap detection
internal/service/
  service.go     resolve once at admission; carry the gaps into the report
internal/cli/
  commands.go    `mas packs --show <id> --version <v>`
pkg/errs/
  registry.go    MAS-5016…MAS-5019
```

## 2. The field

One field, on six types, with the same name and the same syntax as
`metadata.versionRange`:

```go
type Signal struct {
    ID           string `yaml:"id" json:"id"`
    VersionRange string `yaml:"versionRange" json:"version_range,omitempty"`
    // …
}
```

`Step` carries it too, so an author can scope a single check inside a playbook
that otherwise applies everywhere. `Inspect` carries it because a command's
flags change between versions more often than anything else in a pack.

Empty means "every version", which is what every existing pack says by saying
nothing. That is NFR-004: `Resolve` on a pack with no ranges returns a pack with
the same rules, so the case corpus is the proof that nothing moved.

## 3. Intervals and overlap

A `versionRange` is a conjunction of comparisons — `">=3.3"`, `">=4.0 <5.0"`.
For overlap detection each is reduced to a half-open interval over version
vectors:

```go
type interval struct {
    lo, hi    []int  // nil means unbounded
    loOpen    bool   // true for ">", false for ">="
    hiOpen    bool   // true for "<", false for "<="
    exact     []int  // set by "==", which pins the interval to a point
    hasHoles  bool   // set by "!=", which we do not model
}
```

`==` pins `lo` and `hi` to the same point. `!=` is **not** modelled: it can only
punch a hole in an interval, so ignoring it can only make two intervals look
*more* overlapping than they are. That bias is deliberate (RSK-2): a false
"overlap" is a load error the author sees on the next `mas packs`, while a false
"disjoint" is an ambiguity that surfaces as the wrong metric name during an
incident.

`overlaps(a, b)` is the usual interval test with the open/closed ends respected.
Two empty ranges overlap — which is why FR-003 requires *every* variant of an id
to carry a range: an unscoped declaration overlaps everything.

## 4. Validation, at load

Added to `Pack.Validate`:

1. Every non-empty `versionRange` parses, or `MAS-5001` with the path
   (`signals[3].versionRange`).
2. Ids may repeat **only** as variants: every declaration carries a non-empty
   range, and no two overlap. Otherwise `MAS-5016`, naming the id and both
   ranges.
3. The existing uniqueness checks become variant-aware rather than being
   removed — a duplicate id with no range is still the mistake it always was.

Validation runs at load, so a pack that is ambiguous for some version fails for
everyone immediately (D-4).

## 5. `Resolve`

```go
// Resolve returns the pack as it applies to one deployed version, and the gaps
// for everything it dropped.
func (p *Pack) Resolve(version string) (*Pack, []core.Gap)
```

A shallow copy with new slices; the receiver is never mutated, because one
`*Pack` in the library is shared by every target of that middleware.

The passes, in order:

**5.1 Variants.** Group each rule kind by id. For a group of one, keep it if its
range applies. For a group of more than one:

- version known → keep the single variant whose range applies; if none does,
  drop the id and record an itemised `MAS-5017`: the pack cannot place this rule
  on a version it claims to cover, and the author needs the id;
- version unknown → drop the id and record an itemised `MAS-5018`, whose remedy
  is "set `targets[].version`" (D-5).

**5.2 Steps that lost a dependency.** For each surviving playbook, walk steps in
order:

- a `collect` whose args reference `{{signal:x}}` for an `x` no longer present →
  drop the step, and remember its `as` slot as unbound;
- a `conclude` naming a failure mode no longer present → drop the step;
- any step whose own range excludes the version → drop the step; if it was a
  `collect`, its slot is unbound too.

**5.3 Steps that read an unbound slot.** Re-walk with the unbound set, dropping
any step whose `evaluate` or `conclude.when` expression references one. Dropping
a step can unbind nothing further — only `collect` binds — so one pass suffices
after 5.2, and the walk is `O(steps × slots)`.

Slot references are found with the rule engine's own identifier scanner, not a
substring search: `identifiers()` already skips quoted string literals, which is
the fix feature 002 made after regex literals were read as slot names. Using
anything else here would reintroduce that defect in a new place.

**5.4 Playbooks with nothing left to conclude.** A playbook with no surviving
`conclude` step cannot reach a failure mode; it would spend queries and return
findings without a verdict. Drop it, and name it in the aggregate below along
with the rule whose removal started the cascade.

**5.5 Accounting, at two volumes** (HLD §3.1). Everything dropped because the
*known* version excluded it — the rules themselves and everything that cascaded
from them — is aggregated into exactly one gap:

```go
core.Gap{
    Intent: "version scoping for kafka/kafka-core",
    Reason: core.GapNotApplicable,
    Code:   "MAS-5019",
    Detail: "7 rule(s) do not apply to version 4.0.1: logPattern zk_session_expired, playbook kafka.zookeeper-health, …",
    Impact: "these checks do not exist for this version and were not run; nothing was lost",
}
```

`core.GapNotApplicable` is a new reason, and its `Impact` is the reason it
exists. `GapUnavailable` says evidence could not be obtained, which is both a
different claim and a more alarming one. Here the evidence does not exist for
this version, and nothing is wrong — the aggregate is a note, not a warning.

The two itemised codes stay itemised because each names something a person can
act on: `MAS-5018` a missing `targets[].version`, `MAS-5017` a pack that cannot
place one of its own rules.

The resolved pack records what it was resolved for:

```go
out.Metadata.ResolvedFor = version   // "" when it was never resolved
```

## 6. Where the service resolves

In `Diagnose`, immediately after `s.library.For(...)`:

```go
pack, packErr := s.library.For(target.Kind, target.Version)
if packErr == nil {
    var scopeGaps []core.Gap
    pack, scopeGaps = pack.Resolve(target.Version)
    prepGaps = append(prepGaps, scopeGaps...)
}
```

`target.Version` is the version after the environment adapter has had its say,
so a version discovered from a running cluster is used in preference to none.
From here the rules engine, the prompts and the inspect registration all take
the resolved pack, because they take the same variable they always did.

FR-012 is asserted structurally: a test parses `internal/service/service.go` and
requires that the value flowing into `rules.New` came from a `Resolve` call.

## 7. `mas packs --show`

```
mas packs --show kafka                 # every rule, with its range
mas packs --show kafka --version 4.0.1 # what a diagnosis would use
```

Without `--version` the summary gains a range column, so an author can see the
scoping they wrote. With it, the pack is resolved first and the skipped rules
are listed underneath with their gaps — the same sentences a report would carry,
which is what makes this a preview rather than a second implementation.

## 8. Errors

| Code | Meaning |
|---|---|
| `MAS-5016` | Two declarations of one rule id have overlapping version ranges |
| `MAS-5017` | A rule's variants leave no declaration applicable to this version |
| `MAS-5018` | A rule has version-specific variants and the target's version is unknown |
| `MAS-5019` | Rules do not apply to the deployed version and were skipped (one aggregated gap per pack) |

## 9. Tests

| Test | What it pins |
|---|---|
| `TestEveryRuleKindAcceptsAVersionRange` | The field exists and parses on all six kinds |
| `TestOutOfRangeRulesAreDropped` | FR-002 |
| `TestVariantsWithDisjointRangesAreAccepted` | FR-003 |
| `TestOverlappingVariantsAreRejected` | FR-004, including two unscoped declarations |
| `TestVariantMatchingTheVersionIsChosen` | FR-005 |
| `TestUnknownVersionDropsVariantsWithAGap` | FR-006, and that the gap names the remedy |
| `TestStepsFollowTheRulesTheyDependOn` | FR-007 |
| `TestStepsFollowTheSlotsTheyRead` | FR-008, including a slot named inside a regex literal |
| `TestEmptyPlaybooksAreDropped` | FR-009 |
| `TestSkippedRulesAreRecordedAsGaps` | FR-010 |
| `TestResolutionNeverWidens` | FR-011, over a table of versions |
| `TestDiagnosisUsesTheResolvedPack` | FR-012, structurally |
| `TestPacksCommandShowsVersionScoping` | FR-013 |
| `TestKafkaPackScopesZooKeeperRules` | FR-014 |
| `TestUnscopedPackResolvesToItself` | NFR-004 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial low-level design | tasks, code |
