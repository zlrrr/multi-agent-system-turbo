# Measuring MAS-Turbo against cases with known causes

> **Bilingual pair**: [`../zh/evaluation.md`](../zh/evaluation.md)
> Applies to MAS-Turbo 0.1.x · See also: [user manual](./user-manual.md) · [knowledge packs](./knowledge-packs.md) · [error codes](./error-codes.md)

---

## 1. What this measures, and what it does not

`mas eval` runs a corpus of **diagnostic cases**. Each case carries synthetic
telemetry and the failure modes a correct diagnosis reaches. The whole pipeline
runs against it — the same entry point `mas diagnose` uses, over real HTTP to
stub metric and log servers — so query construction, the safety guard's verdict
on each call, decoding, the rule engine and the agents are all exercised rather
than mocked away.

**The corpus is synthetic.** It measures agreement with its own labels, not
accuracy on real incidents. A perfect result means the system behaves the way
the corpus authors expected — a weaker claim than "it diagnoses correctly", and
a useful one: it is what catches a knowledge-pack edit that silently stops
concluding anything.

Three things it deliberately does not do:

- **No single score.** Four outcomes are reported side by side and never
  combined. A miss and a false conclusion are different failures — the first
  leaves an operator where they started, the second sends them somewhere wrong
  with confidence — and any weighted sum would let a change that trades the
  first for the second look like an improvement.
- **No text similarity.** Scoring reads failure-mode ids and gap codes only,
  never a summary or a hypothesis statement. A similarity scorer rewards a model
  for restating the prompt and produces a number whose meaning nobody can state.
- **No claim about a model.** With the `mock` provider the transcript already
  contains the answer, so the run says nothing about model quality. Every
  rendering says so, in the table and in the JSON.

## 2. Running it

```bash
mas eval                        # the shipped corpus, the configured topology
mas eval --matrix               # every topology, same cases
mas eval --topology debate      # one named topology
mas eval --cases ./my-cases     # your cases, alongside the shipped ones
mas eval --json                 # machine-readable, caveats carried as fields
mas eval --lang zh              # the table and the caveats in Chinese
```

`--cases` **adds** directories; it never replaces the shipped corpus, so your own
cases cannot quietly remove the regression baseline. A path that cannot be read
is `MAS-9104` rather than a skipped directory — otherwise a mistyped path would
run the shipped corpus and report success.

The exit status is non-zero (`MAS-9103`) when any case missed or reached a
conclusion the case rules out. That is what makes it usable as a CI gate:

```yaml
- name: Run the corpus against every topology
  run: mas eval --matrix
```

`make eval` does the same thing, and `make ci` includes it.

## 3. Reading the result

```
CASE                                   TOPOLOGY    RESULT  FALSE  GAPS  CALLS  COST
kafka-broker-loss-under-replicated     supervisor  hit     0      ok    8      unpriced
mongodb-replication-lag-write-concern  supervisor  miss    0      ok    8      unpriced

supervisor     5/6 hit · 1 miss · 0 false conclusion(s) · 0 gap(s) missed

  mongodb-replication-lag-write-concern / supervisor:
    not concluded: replication-lag
```

| Column | What it means |
|---|---|
| `RESULT` | `hit` — everything expected was concluded, nothing ruled out was, every expected gap was declared. `miss`, `wrong` or `failed` otherwise |
| `FALSE` | How many conclusions the case explicitly rules out were reached anyway |
| `GAPS` | Whether every gap the case expects was actually declared |
| `CALLS` | Model calls, so a topology's cost is visible next to its result |
| `COST` | Money, when you have configured prices. `unpriced` is never `$0.00` |

A failing case is expanded underneath the totals, naming what was missed and
what was concluded that should not have been, so nobody has to re-run to find
out which mode moved.

## 4. Writing a case

A case is a YAML document. Put it in any directory and pass that directory to
`--cases`.

```yaml
apiVersion: mas.turbo/v1
kind: DiagnosticCase
metadata:
  id: redis-maxmemory-eviction        # unique; appears in the table
  middleware: redis                   # must have a knowledge pack
  version: "7.2.4"                    # selects the pack version range
  title:
    en: "Redis at maxmemory, evicting, refusing writes"
    zh: "Redis 触及 maxmemory，正在驱逐并拒绝写入"
  description:
    en: >-
      What the case is testing, and which wrong answers it rules out.
    zh: >-
      本用例检验什么，以及它排除了哪些错误答案。

symptom:                              # this is what selects playbooks
  en: "p99 latency spike with evictions and OOM errors"
  zh: "p99 延迟毛刺，伴随驱逐与 OOM 报错"

telemetry:
  metrics:
    redis_memory_used_bytes: [940, 970, 990]
    redis_memory_max_bytes: [1000, 1000, 1000]
  logs:
    - "OOM command not allowed when used memory > 'maxmemory'."

expect:
  failure_modes: [memory-pressure]
  not_failure_modes: [replication-broken, persistence-failure]
```

Field by field:

- **`metadata.title`, `metadata.description`, `symptom`** must be present in
  **both** languages. The loader rejects a case that translates half of itself.
- **`symptom`** is not decoration: it is what selects which playbooks run, the
  same way it does in a real diagnosis. A symptom that names no playbook's match
  terms runs only the always-on ones.
- **`telemetry.metrics`** keys are matched as **substrings of the expanded
  PromQL**, longest key first — so a case does not have to restate a pack's exact
  expression, and renaming a signal does not break every case for no diagnostic
  reason. The values are the series the query returns, in order.
- A query matching **no** key returns an **empty** result, not a zero. Empty
  means "this deployment does not export that", which the rule engine records as
  a gap; zero would be a measurement, and a false one.
- **`expect.failure_modes`** are ids the pack declares. A case naming a mode no
  pack declares is `MAS-9101` at load, because it could never pass.
- **`expect.not_failure_modes`** are the plausible wrong answers. They are the
  half of a case that catches a system becoming more confident rather than more
  correct, so give a case at least one.

Run `mas packs --show redis` to see which failure-mode ids a pack declares.

### Testing honesty, not only correctness

A case can take a source away:

```yaml
telemetry:
  withhold: [logs]                    # "logs" or "metrics"
  metrics:
    redis_memory_used_bytes: [940, 970, 990]
    redis_memory_max_bytes: [1000, 1000, 1000]
expect:
  failure_modes: [memory-pressure]
  gaps: ["MAS-4102"]                  # the log source answered with an HTTP error
```

The withheld source is served by a handler that **fails**, so the run
experiences the absence rather than being handed empty data. `expect.gaps` then
requires the run to have *said* the evidence was missing. Without that, a system
that reached the right conclusion without the evidence would pass on correctness
while having got there by luck.

Withholding a source without expecting a gap is rejected at load: it would test
nothing beyond the run having less evidence.

The gap code is the one the collector raises for that kind of failure —
`MAS-4102` for a log source answering with an HTTP status, `MAS-4101` for one
that cannot be reached at all, `MAS-4002` and `MAS-4001` for the metric source's
equivalents. `mas errcodes --filter MAS-41` lists the log-source codes, `--filter MAS-40` the metric ones.

This is also the case that found a real defect. A configured source is now
probed once at admission, so a source that is down is a gap whether or not this
run's control flow happened to query it. Before that, `supervisor` reported the
missing logs and `single` did not — the difference was which agent ran, not what
the deployment could tell us.

## 5. What is in the shipped corpus

21 cases, at least three for every pack. The floor is enforced by a test,
because one case per pack proves the machinery and not the knowledge: a pack
with a single case can lose every other failure mode it declares without
anything going red.

| Pack | Cases | The answers they reach |
|---|---|---|
| Redis | 5 | memory pressure at maxmemory, persistence failure behind MISCONF, connection saturation at the maxclients ceiling, memory pressure with the log source down, and one healthy instance |
| Kafka | 3 | broker loss with under-replication, consumer lag growth against a flat produce rate, produce latency from slow log flushes |
| MongoDB | 4 | replication lag with a write-concern stall, connection saturation, collection scans, and a write-concern stall reached from the log alone with the metric source down |
| Pulsar | 3 | subscription backlog with consumers attached, publish latency at the broker, not enough bookies for the write quorum |
| Milvus | 3 | query-node queueing rather than execution, object storage and etcd failing, nodes OOM-killed at the memory ceiling |
| OceanBase | 3 | one tenant at its own memory ceiling, redo-log replication delay, response time up with throughput flat |

`mas eval` prints them all by id; the YAML in `internal/eval/cases/` is the
authority on any one of them.

Every case is built so the modes it rules out are the answers the *symptom*
invites and the *evidence* denies — "consumers are falling behind" when a broker
was lost, "the disk is filling" when the connection pool is exhausted, "writes
are slow" when the bookies are fine. A diagnosis that follows the complaint
rather than the measurement fails these cases.

Two of them are not about a fault at all:

- **`redis-healthy-baseline`** describes an instance where nothing is wrong, and
  a symptom that still says "latency spike" — because that is what an operator
  types when the application is slow and Redis is the first suspect. It rules
  out every mode the pack declares, so a system that invents a fault to have
  something to say fails it. No correct-answer case can catch that: each of them
  has an answer to find.
- **`redis-logs-unavailable`** and **`mongodb-metrics-unavailable`** take a
  source away and require the run both to reach what the remaining evidence
  supports and to declare what it could not see.

### Does it catch anything?

Two mutations, run against the shipped packs:

| Mutation | Result |
|---|---|
| Redis eviction rule widened from `evicted.avg > 0` to `>= 0` | 3 cases turn `WRONG`, including the healthy one; exit 1 |
| Kafka under-replication threshold raised from `> 0` to `> 100000` | 1 case turns `MISS`; exit 1 |

A widened rule and a narrowed one fail in different columns, which is the whole
reason the columns are kept apart.

## 5a. Baselines: what moved, not just whether it is green

`mas eval` on its own answers one question — is everything still green? After a
change the question is different: **what moved?** A pack edit that fixes two
cases and breaks one shows up as "the corpus regressed", with no way to see the
trade. And once a case legitimately cannot pass, the only way to keep CI green
is to delete it — which deletes the only record that the gap exists.

A baseline records one outcome per **cell**: (case, topology, model).

```bash
mas eval --matrix --write-baseline internal/eval/baseline.json   # record
mas eval --matrix --baseline internal/eval/baseline.json         # compare
```

Recording is a person's act. Nothing else writes a baseline, because one that
updates itself records whatever happened and can never fail.

### What a comparison says

| Was | Is | Called | Gate |
|---|---|---|---|
| hit | hit | *(not reported — an unchanged pass is not news)* | passes |
| hit | anything else | **regressed** | **fails** |
| not hit | hit | **improved** | passes, and is shown |
| not hit | not hit, same ids | **known-bad** | passes, and is listed every run |
| not hit | not hit, different ids | **changed failure** | passes, and is shown |
| absent | anything | **new** | passes, and is shown |
| anything | absent | **not run** | passes, and is shown |

Regressions and improvements are reported side by side and **never netted**. A
change that fixes two cells and breaks one is two improvements and one
regression; the person reviewing it decides. Summing them would let one hide
the other, which is the same collapse this harness refuses everywhere else.

### Known-bad is the point

The row that matters most is the fourth. A cell that fails exactly as it was
recorded — same class, same failure-mode ids — is not a transition. It keeps CI
green *and* appears in the comparison on every run, so the gap stays in front of
whoever could close it. That is what makes it unnecessary to delete a case to
get a green build.

A known-bad cell that starts failing **differently** is reported, because the
reason it fails is part of what was recorded: a cell that was missing one mode
and now reaches a wrong one has moved, even though both are "not a hit".

### The model axis

```bash
mas eval --matrix --models claude-haiku-4-5,claude-sonnet-5
```

Every named model runs across every case and every topology, and each cell
carries the model that produced it — so cost and calls are attributed to the
model that spent them rather than to whichever was configured last.

**Each cell is one sample.** Under the deterministic provider that is a
measurement; under a real model it is a single draw, and two draws can differ.
The comparison says what changed, not whether the change is significant: one run
cannot support that claim, so none is made. The statement travels in the JSON as
a field, like every other caveat here.

Comparing a run under one provider against a baseline recorded under another is
allowed — it is what a model matrix is for — and disclosed every time
(`MAS-9107`), because doing it silently is the part that would be wrong.

### The repository's own baseline

`internal/eval/baseline.json` covers the shipped corpus across all five
topologies under the deterministic provider, and `make ci` compares against it.
`make eval-baseline` re-records it; review the diff before committing, because
that diff is the only thing standing between a recorded regression and a green
build.

---

## 6. What is deliberately not here

- **No real incident data.** Nothing in the corpus came from a production
  system, so nothing in it can leak one.
- **No prompt tuning against the corpus.** A prompt fitted to these cases would
  make the numbers rise and mean less. The corpus is a regression gate, not a
  training set.
- **No ranking of models or vendors.** The harness runs whatever provider you
  configure; a comparison it produced would be a comparison of your prices, your
  prompts and your packs as much as of any model.
- **No significance testing.** One sample per cell is what this runs. A
  confidence interval computed from it would read as rigour and carry none.
- **No automatic baseline updates.** A baseline that updates itself records
  whatever happened, and a build that can never fail teaches nothing.
