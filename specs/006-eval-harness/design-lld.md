# Low-Level Design (LLD): Case Corpus and Evaluation Harness

> **Feature ID**: `006-eval-harness` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

| Path | Content |
|---|---|
| `internal/eval/case.go` | `Case`, validation, `//go:embed cases/*.yaml` |
| `internal/eval/cases/*.yaml` | The shipped corpus, one per pack |
| `internal/eval/stub.go` | Stub Prometheus and Loki servers built from a case |
| `internal/eval/run.go` | `Runner`: case × topology → `Outcome` |
| `internal/eval/score.go` | `Score`, `Outcome`, `Summary` |
| `internal/eval/render.go` | Bilingual rendering, including the caveats |
| `internal/cli/commands.go` | `mas eval` |
| `internal/eval/eval_test.go` | The harness's own tests |
| `pkg/errs/registry.go` | `MAS-9100`…`MAS-9103` |

## 2. The case

```yaml
apiVersion: mas.turbo/v1
kind: DiagnosticCase
metadata:
  id: redis-maxmemory-eviction
  middleware: redis
  version: "7.2.4"
  title:       { en: "…", zh: "…" }
  description: { en: "What is happening and why it is the answer", zh: "…" }

symptom: { en: "p99 latency spike with evictions", zh: "延迟毛刺伴随驱逐" }

telemetry:
  # Matched against the expanded PromQL by substring, longest match first, so a
  # case does not have to restate a pack's exact query — which would make every
  # signal rename break every case for no diagnostic reason.
  metrics:
    redis_memory_used_bytes: [940, 970, 990]
    redis_memory_max_bytes:  [1000]
  logs:
    - "OOM command not allowed when used memory > 'maxmemory'"
  withhold: [logs]        # optional: the source this case denies the run

expect:
  failure_modes:     [memory-pressure]
  not_failure_modes: [replication-broken, persistence-failure]
  gaps:              ["MAS-4101"]     # required when a source is withheld
```

`withhold` is the field that makes a case able to test honesty rather than only
correctness: it takes a source away and then requires the run to *say* it is
missing. A system that quietly concluded the same thing without the logs would
fail the case even though its conclusion was right.

Validation refuses a case that: names a middleware with no pack; names a failure
mode the pack does not declare; declares no expected outcome; or omits either
language of any operator-facing string.

## 3. Stub telemetry

```go
// stubTelemetry serves the Prometheus and Loki HTTP APIs from a case.
//
// Real servers rather than injected tools, deliberately: the layer most likely
// to regress is between the signal's PromQL and the parsed series — query
// construction, the guard's verdict, the collector's decoding, the engine's
// emptiness handling. Every defect found in the last three features lived
// there. A harness that injected tools would skip all of it (design-hld.md §5).
func stubTelemetry(c *Case) (*stubs, error)
```

Matching is longest-substring: a case keyed `redis_memory_used_bytes` answers
the pack's `redis_memory_used_bytes{job="x"} / clamp_min(...)`. A query matching
nothing returns an **empty result**, not zero — which is the honest behaviour
and, since feature 002's engine fix, the behaviour that produces a gap.

## 4. Running

```go
type Options struct {
    Topology  string
    Language  string
    Provider  config.LLMConfig  // mock by default
    Mode      core.Mode
}

func (r *Runner) Run(ctx context.Context, c *Case, o Options) (Outcome, error)
func (r *Runner) Matrix(ctx context.Context, cases []*Case, topologies []string, o Options) (Summary, error)
```

The runner builds a `config.Config` pointing at the stub servers, constructs a
real `service.Service`, and calls `Diagnose`. Nothing about the pipeline is
substituted. Cases run concurrently, bounded, because they are independent and
the corpus must stay inside NFR-001's minute.

## 5. Scoring

```go
type Outcome struct {
    Case, Topology string
    Concluded      []string // failure modes the run reached
    Missing        []string // expected but not concluded
    False          []string // concluded but ruled out by the case
    MissingGaps    []string // expected gap codes not declared
    Usage          core.Usage
    Duration       time.Duration
    Err            error
}

func (o Outcome) Hit() bool // Missing, False and MissingGaps all empty
```

Four facts, never combined. `Hit()` is a conjunction rather than a score
precisely so that a change trading a miss for a false conclusion cannot look
like an improvement — the trade an LLM system makes when pushed to be more
decisive (design-hld.md §3).

## 6. Rendering

```
Corpus: 6 cases × 5 topologies · deterministic provider

CASE                          TOPOLOGY      RESULT   FALSE  GAPS  CALLS   COST
redis-maxmemory-eviction      supervisor    hit          0     ok      8  $0.0084
kafka-under-replicated        debate        MISS         1     ok     11  $0.0116

supervisor    5/6 hit · 0 false conclusions
debate        4/6 hit · 1 false conclusion

This corpus is synthetic: it measures agreement with its own labels, not
accuracy on real incidents.
The provider is `mock`, which replays a script that already contains the
answer — these results say nothing about a model's quality.
```

The caveats are emitted by the renderer, not documented elsewhere, because a
caveat in the manual is absent from the screenshot (plan D-7). The second is
printed only for a scripted provider, and `--json` carries both as fields so an
integration cannot drop them by formatting.

## 7. `mas eval`

```
mas eval                        # shipped corpus, default topology
mas eval --matrix               # every topology
mas eval --cases ./my-cases     # an operator's own corpus, alongside the shipped one
mas eval --topology debate      # one named topology
mas eval --json
```

Exit code is non-zero when any case misses or reaches a false conclusion, which
is what makes the CI gate a gate.

`--cases` names directories read *in addition to* the shipped corpus, so an
operator's own cases never silently replace the regression baseline. A path that
cannot be read is an error rather than a skipped directory: the alternative is a
mistyped path that runs the shipped corpus and reports success.

## 8. Errors

| Code | Meaning |
|---|---|
| `MAS-9100` | The case is malformed |
| `MAS-9101` | The case names a failure mode its pack does not declare |
| `MAS-9102` | The case's middleware has no knowledge pack |
| `MAS-9103` | The corpus regressed: at least one case missed or concluded a ruled-out mode |
| `MAS-9104` | A `--cases` directory cannot be read |

## 9. Tests

| Test | Property |
|---|---|
| `TestCorpusLoadsFromDirectory` | FR-001 — cases are data |
| `TestCaseSchemaRequiresAnExpectedOutcome` | FR-002 |
| `TestCaseNamingAnUndeclaredModeIsRefused` | §2 — a case cannot assert what no pack can conclude |
| `TestHarnessUsesTheRealPipeline` | FR-003 — the stub servers are hit, the guard runs |
| `TestScoringUsesNoTextSimilarity` | FR-004 — structural: scoring reads no prose field |
| `TestFalseConclusionIsScoredSeparately` | FR-005, design-hld §3 |
| `TestWithheldSourceMustProduceADeclaredGap` | FR-006 |
| `TestMatrixRunsEveryCaseAgainstEveryTopology` | FR-007 |
| `TestResultsAreDeterministic` | FR-008 |
| `TestMockRunRefusesToClaimModelQuality` | FR-009, CON-001 |
| `TestReportKeepsOutcomesSeparate` | FR-010, CON-002 |
| `TestRenderedResultAlwaysCarriesTheCaveats` | NFR-005, both languages |
| `TestEveryPackHasACase` | FR-013 |
| `TestCorpusRunsInsideTheCIBudget` | NFR-001 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial low-level design | tasks, code |
