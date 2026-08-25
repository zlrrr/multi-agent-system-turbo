# High-Level Design: Switchable Multi-Agent Topologies

> **Feature ID**: `003-switchable-topologies` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
service ──► orchestrator.Open(name) ──► Orchestrator.Run(ctx, *agent.State)
                                              │
                      ┌───────────────────────┼───────────────────────┐
                      ▼                       ▼                       ▼
                 roles (agent)          tools (guarded)         State (shared)
```

Nothing above or below the `Orchestrator` interface changes. A topology receives
a fully prepared `State`: the target, the symptom, the deterministic findings
already produced by the rule engine, the tool registry the run is allowed, the
model provider, and the budgets. It returns when it has nothing left to do.

## 2. The comparability contract

Five topologies that cannot be compared are worse than two that can, because the
choice would then be decoration. The contract is therefore a first-class artifact
of this feature, not a test-suite detail. Every registered topology must:

| # | Property | Why it is on this list |
|---|---|---|
| 1 | Be governed — listed in the contract table | A topology that opts out by omission defeats the contract |
| 2 | Produce ≥1 hypothesis and a summary from evidence that supports one | The minimum output a report needs |
| 3 | Attribute every model exchange to a role | Without attribution, cost cannot be assigned and a transcript cannot be read |
| 4 | Respect the step budget, recording truncation | An unbounded topology cannot be compared on cost |
| 5 | Complete with a gap when a domain has no tools | Comparability must survive an offline run |
| 6 | Be deterministic over identical state | Two runs that differ by scheduling are not an experiment |
| 7 | Describe itself in both languages, including when *not* to choose it | An operator picking blind is not choosing |
| 8 | Cause only read-only tool calls | Constitution Art. IV |

Properties 2–6 are checked by running each topology against the same scripted
mock; 1, 7 and 8 are structural.

## 3. The five topologies

| Topology | Control flow | Costs | Choose it when |
|---|---|---|---|
| `single` | One generalist with every tool | Cheapest; no specialisation, no refutation | Establishing a baseline |
| `supervisor` | plan → concurrent domain investigators → correlate → critique → report | Moderate; parallel I/O | The default; broad evidence, one pass |
| `plan-execute` | plan → execute one objective → re-plan on the result → … → correlate → critique → report | Sequential, adaptive; cheap when the first objective settles it, dearer when it does not | The first check may well answer it, or evidence is expensive |
| `debate` | investigate → correlate → advocates argue competing positions concurrently → judge adjudicates → report | Dearest; one advocate call per position | Two causes explain the same evidence and picking wrong is expensive |
| `blackboard` | contributors act when the shared state makes them eligible, in rounds, until a round contributes nothing | Variable; adapts to what evidence exists | Evidence arrives unevenly and a fixed script would waste calls |

The differences are control flow only. Every one of them reads the same
deterministic findings, uses the same guarded tools, and writes through the same
`State`.

## 4. The two genuinely new ideas

**Adaptive re-planning.** `plan-execute` is the only topology whose *next* action
depends on what the previous one returned. That is its whole claim: it can stop
early, and it can change direction. Both must be observable, or the claim is
unfalsifiable — so the plan and each revision are recorded as notes, and the
conformance contract checks that a run whose first objective is conclusive is
shorter than one whose first objective is not.

**Data-driven control.** `blackboard` has no script. A deterministic control
component evaluates each contributor's precondition against the blackboard and
runs the eligible ones. The loop ends when a round changes nothing, which is a
property of the state, not a counter. The eligibility predicates are the design;
they are stated in the LLD and each one is tested.

## 5. What this feature deliberately does not build

- **Scoring.** Comparing costs is fair; declaring a winner without a case corpus
  would be an unsupported claim, and the constitution forbids one (Art. IX).
- **A tournament runner.** Running the same case through five topologies is
  `mas diagnose --topology` in a loop. Wrapping that in a command before there is
  a corpus to score would build the interface before the thing it interfaces to.
- **Per-agent model routing.** Orthogonal, and mixing it in would confound the
  comparison this feature exists to make possible.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | Initial high-level design | LLD, tasks, code |
