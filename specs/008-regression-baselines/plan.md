# Implementation Plan: Regression Baselines and the Model Axis

> **Feature ID**: `008-regression-baselines` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

The code is arithmetic over outcomes feature 006 already produces. What needs
deciding is what a baseline *is*, because the obvious answer is the one that
rots.

**The obvious answer: record the numbers.** Store hits and misses per topology,
compare totals, fail when totals get worse. Rejected on the first example: a
change that trades a miss for a false conclusion leaves the totals identical.
The corpus was built specifically so those two failures are never summed, and a
baseline that sums them undoes it from the outside.

**What we record instead: the outcome of each cell, as a class.** A cell is one
(case, topology, model). Its class is `hit`, `miss`, `wrong`, `gap-missed` or
`error`, plus the ids that made it so. Comparison is then a per-cell transition,
and the transitions have names an operator recognises: fixed, regressed, changed
failure, new, not run. Nothing is netted because nothing is summed.

That choice makes FR-004 fall out rather than needing machinery: a cell that was
`miss` and is still `miss`, with the same ids, is not a transition. It fails the
absolute gate and passes the baseline gate, which is exactly the behaviour that
stops a known-bad case being deleted to keep CI green.

**The model axis is the same matrix with one more dimension.** `Runner.Matrix`
already crosses cases with topologies; crossing with models is the same loop and
the same `Options`, with the model varied per job. The interesting part is not
the loop — it is that a cell must carry the model that produced it, or the
accounting attributes cost to whatever model happened to be configured last.

## 2. Design decisions

| ID | Decision | Rationale |
|---|---|---|
| D-1 | A baseline records per-cell outcome classes and ids, never totals | Totals can be identical across a trade this project exists to refuse to average |
| D-2 | A cell is (case, topology, model) | Anything coarser cannot answer "which model regressed"; anything finer has no meaning |
| D-3 | Comparison names transitions, not deltas | "Regressed" and "improved" are different facts; a signed number makes them one |
| D-4 | A repeat of the same failure is not a transition | FR-004. Deleting a known-bad case to keep CI green is the incentive this removes |
| D-5 | Baselines are written only by `--write-baseline` | A baseline that writes itself records whatever happened and can never fail (CON-003) |
| D-6 | The baseline carries the provider and model it was recorded under, and a mismatch is disclosed, not refused | Comparing across models is the point of a model matrix; hiding that the comparison crosses one would not be |
| D-7 | JSON, key-sorted, one cell per line's worth of structure | FR-012: a baseline nobody can diff is a baseline nobody reviews |
| D-8 | The shipped baseline is recorded under the deterministic provider | A committed file that changes on every run is not a baseline (§3) |
| D-9 | Every comparison states that each cell is one sample | With a real model a cell is a draw, not a measurement, and the reader has to know which they are looking at |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-1 | The baseline becomes a rubber stamp: regressions get accepted by rewriting it | Writing is explicit, the file is diffable and small, and the transition report names every change so a review has something to look at |
| RSK-2 | A known-bad cell is forgotten because it no longer fails CI | The comparison lists known-bad cells every run, not only when they change |
| RSK-3 | Someone reads a one-sample model comparison as a benchmark | D-9, and the caveat travels in the JSON as a field, as feature 006's do |
| RSK-4 | The model axis silently attributes cost to the wrong model | The cell carries its model; a test asserts accounting per cell rather than per run |

## 4. Sequencing

1. `Cell` and `Baseline`: the record, its stable encoding, load and save.
2. `Compare`: transitions, with regressions and improvements kept apart.
3. The baseline gate, alongside the absolute one.
4. The model axis in `Runner.Matrix`, with per-cell accounting.
5. Rendering: transitions, known-bad cells, and the sampling caveat.
6. `mas eval --baseline / --write-baseline / --models`; the shipped baseline; CI.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial plan | HLD, LLD, tasks |
