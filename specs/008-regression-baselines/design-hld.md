# High-Level Design (HLD): Regression Baselines and the Model Axis

> **Feature ID**: `008-regression-baselines` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
  corpus ──► Runner.Matrix ──► []Outcome ──┬──► Summary.Regression()   the absolute gate
             (case × topology                │                          (feature 006)
              × model, new)                  │
                                             └──► Compare(baseline) ──► Delta
                                                        ▲                 │
                                          baseline.json │                 ├─ regressed  → fails
                                                        │                 ├─ improved   → shown
                                                   --write-baseline       ├─ known-bad  → shown
                                                   (a person, never       ├─ new        → shown
                                                    a run)                └─ not run    → shown
```

Nothing upstream of `[]Outcome` changes except one loop gaining a dimension.
Everything new is arithmetic over outcomes that already exist, which is NFR-001
and also the reason this feature is small: feature 006 built the hard part when
it decided not to collapse an outcome into a number.

## 2. What a baseline is, and what it refuses to be

The tempting baseline is a scoreboard: hits and misses per topology, compared as
totals. It fails on the first example anyone will hit. A change that turns one
hit into a false conclusion and one miss into a hit leaves every total
unchanged — and those two movements are the exact pair feature 006 refuses to
average, because one leaves an operator where they started and the other sends
them somewhere wrong with confidence.

So a baseline records **cells**, not counts. A cell is one (case, topology,
model), and what is recorded is the class of its outcome plus the ids that made
it that class:

```
kafka-broker-loss-under-replicated / supervisor / mock-1
  → miss, missing=[broker-loss]
```

Comparison is then a per-cell transition, and every transition has a name:

| Was | Is | Called | Gate |
|---|---|---|---|
| hit | hit | — | passes |
| hit | anything else | **regressed** | **fails** |
| not hit | hit | **improved** | passes, and is shown |
| not hit | not hit, same ids | **known-bad** | passes, and is listed every run |
| not hit | not hit, different ids | **changed failure** | passes, and is shown |
| absent | anything | **new** | passes, and is shown |
| anything | absent | **not run** | passes, and is shown |

Nothing is summed, so nothing can cancel. A change that fixes two cells and
breaks one produces two `improved` and one `regressed`, and the person reviewing
it decides — which is what "what moved?" was asking for.

## 3. Known-bad is a first-class state

The row that matters most is the fourth. Once a cell legitimately cannot pass —
a knowledge gap nobody has time to close — the absolute gate leaves exactly one
way to keep CI green: delete the case. That is the worst possible incentive,
because the case is the only record that the gap exists.

Recording the failure in the baseline keeps the gate green *and* the gap
visible. The comparison lists every known-bad cell on every run, not only when
it changes, so it stays in front of the people who could close it (RSK-2). And
a known-bad cell that starts failing *differently* is reported, because the
reason it fails is part of what was recorded — a cell that was missing one
failure mode and is now reaching a wrong one has moved, even though both are
"not a hit".

## 4. The model axis

`Runner.Matrix` crosses cases with topologies. Crossing with models is the same
loop with one more dimension and no new concept — with one thing that has to be
got right: the cell carries the model that produced it.

Without that, per-cell accounting attributes cost to whatever model was
configured last, which is worse than not reporting cost at all: it looks
authoritative and is wrong. So the model travels with the outcome from the job
that ran it, and a test asserts accounting per cell rather than per run.

The comparison across models is the one place this feature knowingly produces a
number an operator will over-read. With a deterministic provider a cell is a
measurement; with a real model it is a **single draw**, and two draws from the
same model can differ. This project does not print confidence intervals it
cannot support, so the answer is disclosure rather than statistics: every
comparison says each cell is one sample, and the statement travels in the JSON
as a field so a formatter cannot drop it.

## 5. What this deliberately does not do

- **No automatic baseline update.** A baseline that updates itself records
  whatever happened and can never fail — a green build that means nothing.
  Writing is a person's act and lands in a reviewable diff.
- **No significance testing.** One sample per cell is what this runs. A
  confidence interval computed from it would be the kind of number that reads as
  rigour and carries none.
- **No shipped baseline for a real provider.** A committed file that changes on
  every run is not a baseline. The repository's own baseline is recorded under
  the deterministic provider, and the shipped corpus is what it covers.
- **No tuning.** The harness measures. Feature 006 refused to let it optimise
  anything, and a baseline makes that refusal more important, not less: a
  baseline is exactly what you would fit to.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial high-level design | LLD, tasks |
