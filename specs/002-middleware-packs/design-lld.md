# Low-Level Design (LLD): Middleware Knowledge Breadth

> **Feature ID**: `002-middleware-packs` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md)

## 1. Files added

```
internal/knowledge/packs/mongodb.yaml
internal/knowledge/packs/pulsar.yaml
internal/knowledge/packs/milvus.yaml
internal/knowledge/packs/oceanbase.yaml
internal/knowledge/conformance_test.go
docs/en/knowledge-packs.md
docs/zh/knowledge-packs.md
```

No `.go` file outside the new test is modified. That is FR-010, and it is checked
by review of the diff.

## 2. The conformance contract

`conformance_test.go` is table-driven over every pack the library loads, so a
future pack inherits the floor automatically.

```go
type floor struct {
    middleware   string
    minSignals   int
    minPatterns  int
    minModes     int
    minPlaybooks int
    requiredModes []string // failure modes the specification names
}
```

Checks applied to every pack:

| Check | Rule | Requirement |
|---|---|---|
| Depth | counts meet the floor | FR-005 |
| Named modes | every `requiredModes` entry is present | FR-001…004 |
| Bilingual | every `{en,zh}` pair complete and not identical | CON-001 |
| Advice | every failure mode has ≥1 recommendation | FR-007 |
| Advisory phrasing | no recommendation matches `^(restarted|scaled|applied|deleted|fixed|increased|disabled|enabled) ` or the Chinese equivalents | CON-003 |
| Risk vocabulary | every recommendation's risk is low/medium/high | FR-007 |
| Guard-clean | every `inspect` command passes `safety.Guard.Authorize` | FR-006 |
| Expressions | every `evaluate` and `conclude.when` compiles in the sandbox | FR-009 |
| Signal references | every `{{signal:id}}` resolves | FR-009 |
| Concluded modes | every `conclude.failureMode` is declared | FR-005 |
| Reachability | every playbook has ≥1 collect step and ≥1 conclude or finding | FR-005 |
| Honest coverage | every failure mode's `indicators` mention a declared signal id or log-pattern id | NFR-002 |
| Always-on | exactly one playbook has no `matches`, so something always runs | HLD §4 |

The expression check needs the rules package, which would make
`internal/knowledge` depend on `internal/rules` — the wrong direction. The test
therefore lives in package `knowledge_test` and imports `rules`, which is a test
dependency only and does not affect the production layering.

## 3. Signal sources

Each pack's metric names come from one widely deployed exporter. Recording the
source is what makes NFR-001 checkable by a reader.

| Middleware | Metric source | Prefix |
|---|---|---|
| MongoDB | `percona/mongodb_exporter` | `mongodb_` |
| Pulsar | Pulsar broker's built-in Prometheus endpoint | `pulsar_` |
| Milvus | Milvus built-in metrics | `milvus_` |
| OceanBase | `obagent` / OceanBase exporter | `ob_` |

Where an exporter exposes both a legacy and a current name, the pack uses the
current one and the authoring guide notes the alternative.

## 4. Inspect commands

| Middleware | Command | Guard status |
|---|---|---|
| MongoDB | `mongosh --eval db.serverStatus()` and the other allow-listed evals | Already permitted |
| MongoDB | `mongosh --eval rs.status()` | Already permitted |
| Pulsar | `pulsar-admin brokers healthcheck`, `topics stats`, `namespaces list` | Binary permitted; verbs checked by the conformance test |
| Milvus | none | No allow-listed CLI; diagnosis uses metrics and logs |
| OceanBase | none | Inspection would need a SQL client the guard does not permit |

Milvus and OceanBase shipping without inspect commands is deliberate. The
alternative is asking for a guard allow-list entry, which is a specification
change this feature did not request — and a pack that quietly needs a wider guard
is exactly the failure this design is trying to avoid.

## 5. Playbook expression environment

Unchanged from 001-mvp-core §2.12. Each collected slot exposes:

```
.empty .series .count .latest .last .latestMin .min .max .avg .sum .delta .byLabel .summary
```

with helpers `contains`, `matches`, `countMatching`, `ratio`, `pct`, `finite`.
Log slots expose `.empty .count .lines .text .summary`.

The guidance that matters for pack authors: **always guard a threshold with
`not X.empty`**. A missing collection leaves the slot unset and the step is
skipped, but a slot that collected zero series would otherwise compare as 0 and
read as healthy.

## 6. Test matrix

| Test | Level | Target | Requirement |
|---|---|---|---|
| `TestPackConformance` | unit | every shipped pack | FR-005, FR-001…004 |
| `TestPackInspectCommandsPassTheGuard` | unit | every shipped pack | FR-006 |
| `TestPackExpressionsCompile` | unit | every shipped pack | FR-009 |
| `TestPackRecommendationsAreAdvisory` | unit | every shipped pack | FR-007, CON-003 |
| `TestPackCoverageIsHonest` | unit | every shipped pack | NFR-002 |
| `TestNewPacksRunAgainstStubTelemetry` | integration | the four new packs | FR-001…004 |
| Existing `TestEmbeddedPacksValid`, `TestBilingualPackFields` | unit | every shipped pack | CON-001 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | Initial low-level design | `tasks.md`, packs |
