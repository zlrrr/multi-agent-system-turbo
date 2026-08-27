# High-Level Design: Case Corpus and Evaluation Harness

> **Feature ID**: `006-eval-harness` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
case (YAML) ──► stub Prometheus + stub Loki (real HTTP)
                          │
                          ▼
              config ──► service.Diagnose ──► report
                          │                     │
              real collectors, guard,           ▼
              rule engine, topology        eval.Score(case, report)
                                                │
                                    Outcome{hit, false, gaps, cost}
                                                │
                                      matrix · render · CI gate
```

The harness sits **outside** the system and drives it through the same door an
operator uses. It adds no interface to the product: `service.Diagnose` is what
`mas diagnose` calls, and the collectors it reaches are the ones a real run
reaches. A harness that injected stub tools would be faster to write and would
measure a system nobody runs.

## 2. What a case asserts

A case is an incident plus the outcome a correct diagnosis reaches. It asserts
only things the pipeline already commits to, in a vocabulary the packs define:

| Field | Meaning | Why it is checkable |
|---|---|---|
| `expect.failure_modes` | Modes a correct diagnosis concludes | Pack-declared ids |
| `expect.not_failure_modes` | Plausible wrong answers it must not conclude | Pack-declared ids |
| `expect.gaps` | Gaps a run must declare, given what the case withholds | Error codes |

There is no reference answer text, and scoring never reads prose. The cost is
that a case cannot measure how well something was explained — which the
documentation states rather than leaving a reader to assume the number covers it.

## 3. Four numbers, never one

Each run of a case produces four facts, kept apart:

| Outcome | Meaning |
|---|---|
| **hit** | Every expected mode was concluded |
| **miss** | An expected mode was not concluded |
| **false conclusion** | A mode the case rules out *was* concluded |
| **gap** | An expected gap was or was not declared |

plus its cost and duration.

They are never combined. A miss and a false conclusion are different failures:
one leaves an operator where they started, the other sends them somewhere wrong
with confidence. A weighted sum would let a change that traded the first for the
second look like an improvement — and that trade is precisely the one an
LLM-based system makes when it is pushed to be more decisive.

## 4. What the numbers do not mean

Three statements are rendered with every result, in the operator's language,
because a caveat that lives in the manual is absent at the moment someone
screenshots the table:

1. **The corpus is synthetic.** It measures agreement with its own labels, not
   accuracy on real incidents. A perfect score means the system behaves as the
   corpus authors expected, which is a weaker claim and a useful one.
2. **A mock-provider run measures the script.** The scripted transcript already
   contains the answer, so agent-phase results say nothing about a model. The
   harness reports the deterministic phase and refuses to present the rest as
   model quality.
3. **Cost is only as good as the prices.** Unpriced models make it unknown, as
   everywhere else.

## 5. Why the real pipeline

The parts most likely to regress are the ones a shortcut would skip: the PromQL
a signal expands to, the guard's verdict on it, the collector's parsing, the
engine's emptiness handling. Every defect this project has found in the last
three features lived in exactly that layer — regex literals read as slot names,
empty series read as healthy, citations that named nothing.

So the harness serves real HTTP from stub servers and lets everything downstream
of the socket be the production path. It costs a few hundred milliseconds per
case and buys a measurement of the system that ships.

## 6. What this does not build

- **Prompt tuning against the corpus.** A corpus used to fit is no longer a
  measure, and the moment it is used that way its numbers stop meaning what the
  caveats say.
- **Real incident data.** Recording production telemetry is a privacy question
  this project has not asked, and answering it silently by shipping a recorder
  would be the wrong way to ask.
- **A leaderboard.** Comparing topologies is the point; ranking them
  permanently on a synthetic corpus of a dozen cases would be an unsupported
  claim wearing a table.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial high-level design | LLD, tasks, code |
