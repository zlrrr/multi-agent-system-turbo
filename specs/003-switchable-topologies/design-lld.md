# Low-Level Design (LLD): Switchable Multi-Agent Topologies

> **Feature ID**: `003-switchable-topologies` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

| Path | Content |
|---|---|
| `internal/core/text.go` | `Text` — the bilingual string, moved down from `internal/knowledge` so two packages can share one |
| `internal/knowledge/pack.go` | `type Text = core.Text` alias; no other change, no YAML change |
| `internal/orchestrator/orchestrator.go` | `Description`; `Register` takes it; `Describe(lang)` |
| `internal/orchestrator/planexecute.go` | `plan-execute` |
| `internal/orchestrator/debate.go` | `debate` |
| `internal/orchestrator/blackboard.go` | `blackboard` |
| `internal/orchestrator/conformance_test.go` | The contract every topology must satisfy |
| `internal/agent/roles.go` | `Strategist`, `Executor`, `Advocate`, `Judge` |
| `internal/agent/prompts.go` | Their instructions |
| `internal/llm/mock/mock.go` | Scripted replies for the four new roles |

## 2. The bilingual description

```go
type Description struct {
    Summary core.Text // the control flow, in one or two sentences
    Cost    core.Text // the cost profile, stated plainly
    Choose  core.Text // when to prefer it
    Avoid   core.Text // when not to
}

func (d Description) In(lang string) string // Summary/Cost/Choose/Avoid, rendered
func Register(name string, d Description, f Factory)
func Descriptions(lang string) map[string]string
func Details() map[string]Description
```

`Avoid` is not decoration. A tool that ships five architectures and recommends
all of them has told the operator nothing; the field is required non-empty by the
conformance contract precisely so that each topology has to admit what it is bad
at (RSK-001).

## 3. New roles

```go
// Objective is one unit of work an adaptive topology decides to do next.
type Objective struct {
    Domain    tool.Domain // which evidence domain answers it
    Statement string      // what to establish, in one sentence
}

// Strategist decides the next objectives from what is known so far, and says
// when nothing further is worth doing. Distinct from Planner, which writes one
// prose plan for the reader: this contract is structured, iterative and
// terminating.
type Strategist struct {
    Round   int      // 0-based; round 0 has learned nothing yet
    Learned []string // what the executed objectives returned
}
func (Strategist) Role() Role { return RoleStrategist }
func (s Strategist) Step(ctx context.Context, st *State) (Outcome, error)
func (s Strategist) Objectives() []Objective // populated by Step
```

`Step` returns `Outcome{Done: true}` when the strategist says stop. The
topology treats `Done` plus an empty objective list as "converged", which is the
early exit that justifies this topology's existence.

```go
// Executor pursues exactly one stated objective with the tools of its domain.
type Executor struct{ Objective Objective }
func (Executor) Role() Role { return RoleExecutor }

// Advocate argues one position against the alternatives, from shared evidence.
// It does not choose its position; that is what makes the argument adversarial
// rather than a second opinion.
type Advocate struct {
    Position    core.Hypothesis
    Alternatives []core.Hypothesis
}
func (Advocate) Role() Role { return RoleAdvocate }

// Judge adjudicates the advocates' arguments against the evidence.
type Judge struct{}
func (Judge) Role() Role { return RoleJudge }
```

`Judge` differs from `Critic` in what it is given: the critic challenges each
hypothesis on its own; the judge is handed competing arguments about the same
evidence and must prefer one. Its reply shape is the critic's, so the report
needs no change.

## 4. `plan-execute`

```
Planner (prose plan, for the reader)
repeat up to maxRounds:
    Strategist(round, learned) → objectives
    if no objectives: break                  ← the early exit
    for each objective: Executor → learned += result
Correlator → Critic → Reporter
```

- `maxRounds` is 3. Beyond that the loop is not adapting, it is wandering, and
  the run's step budget would truncate it anyway — but a topology should bound
  itself rather than rely on being cut off.
- Objectives within a round run sequentially. That is the point: this topology
  trades the supervisor's parallelism for the ability to change its mind, and
  running a round concurrently would only make its rounds coarser.
- Each round's objectives and results are recorded as notes, so the adaptation is
  visible in the report rather than only in the transcript.

## 5. `debate`

```
Planner → Investigators (as supervisor: concurrent, per domain)
Correlator → hypotheses
positions := top N hypotheses by confidence, N = min(3, len)
Advocates (one per position, concurrent) → arguments as notes
SortNotes(deterministic order by position rank)
Judge → status + confidence per hypothesis
Reporter
```

- N is capped at 3. A debate between every hypothesis the correlator produced
  would cost one call per hypothesis for diminishing returns, and the positions
  below third place are rarely live.
- With fewer than two hypotheses there is nothing to debate: the topology records
  a gap saying so and falls back to `Critic`, which is honest about the fact that
  a debate did not happen rather than staging one.

## 6. `blackboard`

The control component is a list of contributors, each with a precondition over
the state. A round runs every eligible contributor once, in the listed order;
the loop stops when a round changes nothing, or at `maxRounds` (4).

| Contributor | Eligible when | Contributes |
|---|---|---|
| `Planner` | No notes yet | The initial plan |
| `Investigator{d}` | Domain `d` has tools **and** has not yet contributed a note | Evidence and a note |
| `Correlator` | ≥1 note exists **and** evidence has changed since the last correlation | Hypotheses |
| `Critic` | ≥1 hypothesis exists that has not been assessed | Status and confidence |
| `Reporter` | ≥1 hypothesis exists **and** no summary yet | Summary and recommendations |

"Changed since" is measured with the digests `State` already computes
(`EvidenceDigest`, `PriorFindingsDigest`), so no new state is needed (plan D-2).

Termination is a property of the predicates, not a counter: every contributor's
precondition is falsified by its own contribution, so a round that runs
contributors always reduces the eligible set unless new evidence appeared — and
new evidence can only come from an investigator, each of which runs at most once.
`maxRounds` is a backstop for a future contributor that breaks that argument, not
the mechanism.

## 7. The conformance contract

`internal/orchestrator/conformance_test.go`, written before the three topologies:

| Test | Property |
|---|---|
| `TestEveryRegisteredTopologyIsGoverned` | Contract table covers every registered name |
| `TestTopologyProducesHypothesisAndSummary` | HLD §2 property 2 |
| `TestTopologyAttributesEveryExchange` | Property 3 — every recorded LLM step names a role |
| `TestTopologiesRespectStepBudget` | Property 4 — a budget of 2 truncates and records it |
| `TestTopologiesDegradeWithoutTools` | Property 5 — empty registry still completes, with gaps |
| `TestTopologiesAreDeterministic` | Property 6 — two runs, identical hypotheses and notes |
| `TestTopologyDescriptionsAreBilingualAndHonest` | Property 7 — all four fields, both languages, `Avoid` non-empty |
| `TestBrokenTopologyFailsTheContract` | The contract itself: a topology that skips the summary must fail |

`TestBrokenTopologyFailsTheContract` is the one that makes the rest credible. It
registers a deliberately defective topology in a sub-test, runs the contract
against it, and asserts the contract *fails* — so a contract that has quietly
stopped checking anything cannot pass unnoticed.

## 8. Errors

No new error codes. `MAS-3001` (unknown topology) already covers selection;
truncation is `MAS-3005` as today; a debate with too few positions records a gap,
not an error, because the run is still valid.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | Initial low-level design | tasks, code |
