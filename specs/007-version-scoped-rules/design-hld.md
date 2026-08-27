# High-Level Design (HLD): Version-Scoped Pack Rules

> **Feature ID**: `007-version-scoped-rules` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
  config / env adapter          knowledge library                 run
  ────────────────────          ─────────────────                 ───
  target.version  ────────────► Library.For(kind, version)
                                      │   (pack-level range, today)
                                      ▼
                                Pack.Resolve(version)  ──────────► *Pack (resolved)
                                      │                                │
                                      │ gaps: what was skipped         │
                                      ▼                                ▼
                                 report.Gaps                      rules engine
                                                                  agent prompts
                                                                  inspect commands
```

One arrow is new: `Resolve`. Everything downstream of it is unchanged, because
it returns the same type. That is the point — version scoping is a property of
the pack a run holds, not an argument every consumer must remember to pass.

Two things happen at that arrow and nowhere else:

- **Narrowing.** Rules the version excludes leave the pack.
- **Accounting.** Nothing leaves without the report saying so. Not every
  departure deserves the same volume, though, and getting that wrong is how a
  gap list becomes something operators learn to scroll past — see §3.1.

## 2. Why the boundary is a resolution, not a filter

A filter answers "does this rule apply?" one rule at a time. That is enough for
signals and log patterns and not enough for anything with a dependent.

A playbook step reads `{{signal:brokers}}`. Drop the `brokers` signal for this
version and the step no longer expands — it fails at run time, in the middle of
a diagnosis, with a template error. The step must go too. A later step evaluates
`brokers.min < brokers.max`; drop the collect step and that expression reads a
slot nothing produced. It must go too. Remove enough steps and a playbook can no
longer reach any conclusion; it is then a playbook that collects evidence and
decides nothing, which costs queries and returns findings without a verdict.

So the unit of resolution is the pack, not the rule: only something holding the
whole document can follow those edges. The edges are:

```
  signal ──referenced by──► step ──produces slot──► step ──►  ...
                             │
  failureMode ──concluded by─┘
                             │
                             └──► playbook (drops when no conclusion remains)
```

Resolution walks them once, in that order, and every removal names the rule that
caused it.

### 3.1 Two volumes, because they are two different facts

A rule that does not apply to the deployed version is **not** missing evidence.
The check was never possible here; nothing was lost. Itemising each one would
put a dozen entries in the gap list of a correctly-scoped pack, and an operator
who learns that gaps are mostly noise will miss the one that matters. So all of
it — the rules whose range excluded the version, and everything that cascaded
from them — becomes **one** gap per run, naming the version and listing the ids.

A rule we could not *place* is different. When an id has variants and the
target has no version, there was a check we could have run and did not, and the
operator can fix that in one line of configuration. Each of those gets its own
gap, with that line as its remedy. The same goes for a pack whose variants leave
nothing applicable to the deployed version: that is a defect in the pack, and
the person who can fix it needs to see which id.

The rule is: **aggregate what follows from knowing the version, itemise what
follows from not knowing it.**

## 3. Variants, and the one case where we refuse to answer

A rule id may appear more than once when every appearance carries a range and no
two overlap. That is how a rename is expressed:

```yaml
signals:
  - id: controller_count
    versionRange: "<3.3"
    promql: 'sum(kafka_controller_kafkacontroller_activecontrollercount{{.selector}})'
  - id: controller_count
    versionRange: ">=3.3"
    promql: 'sum(kafka_controller_kafkacontroller_activecontrollercount{{.selector}})'
```

Every playbook keeps referencing `controller_count`. One document, one id, two
expressions, and the run picks by version.

The interesting case is a target with **no version configured**. Today an absent
version means "applies" everywhere, which is the right default for a pack-level
range: refusing to help because a version string is missing would be the wrong
trade during an incident. For a *variant* it is not a default at all — there is
no rule to fall back to, only a choice between two, and choosing wrong means
querying a metric name that does not exist and reading its absence as data.

So variants are the one place this design refuses: with no version, the id is
dropped and a gap says so. The remedy in that gap is one line — set
`targets[].version` — and the operator can act on it. A guess cannot be acted
on, because nobody would know it happened.

A singly-declared scoped rule is different, and is **kept** when the version is
unknown. There is nothing to choose between; if it turns out not to apply, its
query returns nothing and the engine already records that as a gap. Dropping it
would trade a check we might have made for no information at all.

## 4. What resolution may never do

Resolution narrows. It has no branch that adds a rule, and in particular no
branch that adds an inspect command, because an inspect command is the one rule
kind that reaches a live system. The safety guard is unaffected either way — it
re-validates every command at call time against an allow-list no pack can widen
— but "the guard would catch it" is not a reason to build something that needs
catching. A test asserts that for every version, the resolved pack's rules are a
subset of the unresolved pack's.

## 5. What this deliberately does not do

- **No version discovery.** The version comes from configuration or the adapter.
  Sniffing it would be another live call and another thing to be wrong about.
- **No scoping by anything else.** Deployment mode, vendor and exporter version
  are all real axes and none of them is the middleware's version. Conflating the
  exporter's naming with the middleware's would be the most tempting and the
  most wrong.
- **No rewrite of the shipped packs.** The mechanism ships with the rules whose
  boundaries are documented facts. A range invented from memory silently removes
  a working check, which is worse than the gap it was meant to fix.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial high-level design | LLD, tasks |
