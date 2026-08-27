# Low-Level Design (LLD): Regression Baselines and the Model Axis

> **Feature ID**: `008-regression-baselines` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

```
internal/eval/
  baseline.go     new: Cell, Baseline, LoadBaseline, Save, byte-stable encoding
  compare.go      new: Compare, Delta, Transition, the baseline gate
  run.go          + Models on Options; Matrix crosses cases × topologies × models
  score.go        + Outcome.Model, Outcome.Class
  render.go       + RenderDelta, the sampling caveat
  baseline.json   the shipped baseline for the shipped corpus
internal/cli/
  commands.go     `mas eval --baseline / --write-baseline / --models`
pkg/errs/
  registry.go     MAS-9105…MAS-9107
```

## 2. The cell and its class

```go
// Class is what an outcome amounts to, in the vocabulary a baseline records.
type Class string

const (
    ClassHit       Class = "hit"
    ClassMiss      Class = "miss"        // expected modes not concluded
    ClassWrong     Class = "wrong"       // a ruled-out mode was concluded
    ClassGapMissed Class = "gap-missed"  // an expected gap was not declared
    ClassError     Class = "error"       // the run failed
)
```

`Wrong` outranks `Miss` when both apply: a run that missed the answer *and*
reached a ruled-out one is the more serious of the two, and a class has to pick
one. The ids are recorded alongside, so nothing is lost by the ordering.

```go
// Cell is one (case, topology, model) result, as recorded.
type Cell struct {
    Case     string   `json:"case"`
    Topology string   `json:"topology"`
    Model    string   `json:"model"`
    Class    Class    `json:"class"`
    Missing  []string `json:"missing,omitempty"`
    False    []string `json:"false_conclusions,omitempty"`
    GapsMissed []string `json:"gaps_missed,omitempty"`
}

func (c Cell) Key() string { return c.Case + "|" + c.Topology + "|" + c.Model }
```

No counts anywhere. A baseline that recorded totals would let a change trading a
miss for a false conclusion compare as unchanged, which is the failure this
whole design is arranged against (design-hld.md §2).

## 3. The baseline file

```go
type Baseline struct {
    Version   int      `json:"version"`   // schema version, 1
    Provider  string   `json:"provider"`
    Recorded  string   `json:"recorded"`  // RFC3339 date, not time
    Corpus    int      `json:"corpus"`    // case count when recorded
    Cells     []Cell   `json:"cells"`
}
```

Encoded with `json.MarshalIndent`, cells sorted by `Key()`. That is FR-012: the
file is reviewed as a diff, and a diff full of reordering is one nobody reads.
`Recorded` is a date rather than a timestamp for the same reason — a baseline
rewritten with no change of content should produce no diff at all.

`Save` writes only when asked (FR-002). There is no path from a plain `mas eval`
to a write; the flag is the only caller.

## 4. Comparison

```go
type Transition string

const (
    Regressed      Transition = "regressed"
    Improved       Transition = "improved"
    KnownBad       Transition = "known-bad"
    ChangedFailure Transition = "changed-failure"
    New            Transition = "new"
    NotRun         Transition = "not-run"
)

type Change struct {
    Cell       Cell       `json:"cell"`
    Was        Class      `json:"was,omitempty"`
    Transition Transition `json:"transition"`
    Detail     string     `json:"detail,omitempty"`
}

type Delta struct {
    Changes  []Change `json:"changes"`
    Mismatch string   `json:"provider_mismatch,omitempty"`
    Caveats  []string `json:"caveats"`
}
```

`Compare(base Baseline, s Summary) Delta` walks both keyed sets:

| Was | Is | Transition |
|---|---|---|
| hit | hit | omitted — an unchanged pass is not news |
| hit | not hit | `Regressed` |
| not hit | hit | `Improved` |
| not hit | not hit, same ids | `KnownBad` |
| not hit | not hit, different ids | `ChangedFailure` |
| absent | present | `New` |
| present | absent | `NotRun` |

Same-ids is a sorted comparison of the three id lists, not of the class alone: a
cell that was missing one mode and now reaches a wrong one has moved, even
though both are "not a hit" (design-hld.md §3).

`KnownBad` is emitted every run, not only on change. A gap that stops being
visible is a gap that stops being fixed (RSK-2).

`Delta.Gate() error` fails only on `Regressed`, with `MAS-9105` carrying the
count and the first few keys. Everything else is reported and passes.

## 5. Provider mismatch

The baseline records the provider it was written under. When a run's provider
differs, `Delta.Mismatch` is set and the renderer prints it above the table and
carries it in the JSON.

Not an error, deliberately: comparing a run under one model against a baseline
recorded under another is exactly what a model matrix is for. What would be
wrong is doing it silently (D-6).

## 6. The model axis

```go
type Options struct {
    // …
    Models []string   // empty means the one model in Options.LLM
}
```

`Matrix` builds jobs as case × topology × model. Each job copies `o.LLM` and
sets `Model`, so routing, pricing and the ledger all see the model that actually
ran. `Outcome.Model` is filled from the job, not from the shared config — the
distinction matters because a shared read would attribute every cell's cost to
whichever model was configured last, which looks authoritative and is wrong
(RSK-4).

Ordering stays deterministic: case, then topology, then model.

## 7. Rendering

`RenderDelta(w, delta, lang)` prints, in order:

1. the provider mismatch, if any;
2. regressions, with what each cell lost;
3. improvements;
4. changed failures;
5. known-bad cells, as a list;
6. new and not-run cells;
7. the caveats.

The caveat added here: **each cell is one sample**. Under a deterministic
provider that is a measurement; under a real model it is a draw, and two draws
can differ. It travels in `Delta.Caveats` so a JSON consumer cannot format it
away, exactly as feature 006's do.

## 8. `mas eval`

```
mas eval --baseline internal/eval/baseline.json      # compare, fail on regression
mas eval --write-baseline internal/eval/baseline.json # record (a person's act)
mas eval --models mock-1,mock-2 --matrix              # the full matrix
```

`--baseline` replaces the absolute gate with the baseline gate. Both are
available; only one is the exit status, because a build that fails for two
reasons at once teaches nothing about either.

## 9. Errors

| Code | Meaning |
|---|---|
| `MAS-9105` | Cells regressed against the baseline |
| `MAS-9106` | The baseline file could not be read or is not a baseline |
| `MAS-9107` | The baseline records a different provider than this run used |

`MAS-9107` is a warning carried as a disclosure, never an error that stops a run.

## 10. Tests

| Test | What it pins |
|---|---|
| `TestBaselineRecordsEveryCell` | FR-001 |
| `TestBaselineIsNeverWrittenImplicitly` | FR-002, CON-003 |
| `TestRegressionsAndImprovementsAreReportedSeparately` | FR-003, CON-001 |
| `TestKnownBadCellDoesNotFailTheGate` | FR-004 |
| `TestChangedFailureIsReported` | FR-005 |
| `TestNewCellIsNotARegression` | FR-006 |
| `TestMissingCellIsReported` | FR-007 |
| `TestProviderMismatchIsDisclosed` | FR-008 |
| `TestModelAxisRunsEveryCell` | FR-009 |
| `TestPerCellAccountingIsAttributed` | FR-010, RSK-4 |
| `TestComparisonCarriesTheSamplingCaveat` | FR-011, CON-002 |
| `TestBaselineIsByteStableAcrossRuns` | FR-012 |
| `TestEvalBaselineCLI` | FR-013 |
| `TestShippedBaselineMatchesTheCorpus` | FR-014 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial low-level design | tasks, code |
