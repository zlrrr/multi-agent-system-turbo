# Implementation Plan: Case Corpus and Evaluation Harness

> **Feature ID**: `006-eval-harness` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. The decision that shapes everything else

**What counts as correct must be checkable without judgement.**

The tempting design scores the report's prose against a reference answer. It is
tempting because it feels like it measures understanding, and it is wrong for
three reasons that compound: it rewards a model that restates the prompt; it
makes every new case require a reference writer; and it produces a number whose
meaning nobody can state precisely, which is the property this project's
constitution most consistently refuses.

So the corpus labels facts the system already commits to in a controlled
vocabulary:

- **which failure mode was concluded** — the packs' failure-mode ids;
- **which modes must not be concluded** — the plausible wrong answers;
- **which gaps must be declared** — when the case withholds a source.

All three are ids, all three are already produced by the pipeline, and a scorer
over them needs no judgement and no reference text. The cost is that the corpus
cannot measure how *well* something was explained. That is a real limitation and
the documentation says so rather than pretending the number covers it.

## 2. Design decisions

| ID | Decision | Rationale | Reversal condition |
|---|---|---|---|
| D-1 | Score only ids the pipeline already emits | A scorer needing judgement cannot be run in CI, and one built on similarity measures the wrong thing | A conclusion type appears that has no id |
| D-2 | The harness stands up stub Prometheus and Loki **HTTP servers** rather than injecting stub tools | Injecting tools would skip the collector clients, the guard and the query construction — the parts most likely to regress. A harness that skips the real path measures a system nobody runs | — |
| D-3 | No single collapsed score | Correctness, false conclusions, cost and latency trade against each other, and a weighted sum makes that trade silently on the reader's behalf | — |
| D-4 | False conclusions counted separately from misses | "Said nothing" and "said the wrong thing confidently" are different failures with different costs, and averaging them together hides which one a change caused | — |
| D-5 | A mock-provider run reports the deterministic phase and refuses to present the agent phase as model quality | The mock replays a script that already contains the answer. Reporting that as a model score would be the most flattering possible lie | — |
| D-6 | Cases ship embedded, and extra directories are loadable | Same rule as knowledge packs: an operator's own corpus is the point, and a shipped one they cannot extend is a demo | — |
| D-7 | The synthetic-corpus caveat is emitted by the renderer, not left to documentation | A caveat in the manual is not present at the moment someone screenshots the table | — |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-001 | The corpus becomes a target and the packs are tuned to it | Stated in the spec as out of scope, and in the docs: a corpus used to fit is no longer a measure. Cases assert *conclusions*, not thresholds, so a pack fitted to a case would still have to be right about the mechanism |
| RSK-002 | A synthetic corpus is read as an accuracy figure | The caveat is rendered with every result, in both languages, and the word "accuracy" is not used |
| RSK-003 | Stub servers drift from what real Prometheus and Loki return | They serve the same JSON shapes the collector tests already assert against, and those tests stay the source of truth for the shapes |
| RSK-004 | The corpus slows CI until someone deletes it | NFR-001 caps the whole corpus at 60s with the deterministic provider, and the runner is concurrent across cases |
| RSK-005 | A case passes for the wrong reason — the mode concluded by luck | Each case declares modes it must *not* conclude, so a topology that concludes everything fails rather than scores well |

## 4. Sequencing

| Phase | Content | Gate |
|---|---|---|
| A | Case schema, corpus loader, bilingual validation | A case loads; a case with no expected outcome is refused |
| B | Stub telemetry servers; the runner over the real service | FR-003 — a case reaches a conclusion through the real pipeline |
| C | Scoring: hits, false conclusions, gaps; determinism | FR-004, FR-005, FR-006, FR-008 |
| D | Matrix, rendering, the caveat, `mas eval` | FR-007, FR-010, FR-011, NFR-005 |
| E | The shipped corpus, one case per pack; CI gate; docs | FR-012, FR-013, `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial plan | HLD, LLD, tasks |
