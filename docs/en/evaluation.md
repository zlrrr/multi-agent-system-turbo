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

One case per knowledge pack, which is the floor a test enforces: a pack with no
case is knowledge nothing checks.

| Case | Middleware | The answer | The plausible wrong answers it rules out |
|---|---|---|---|
| `redis-maxmemory-eviction` | Redis | memory pressure | replication broken, persistence failure |
| `kafka-broker-loss-under-replicated` | Kafka | broker loss, under-replication | offline partitions, controller instability, consumer lag growth |
| `mongodb-replication-lag-write-concern` | MongoDB | replication lag, write-concern stall | primary election, slow queries, lock contention, connection saturation, storage pressure |
| `pulsar-subscription-backlog` | Pulsar | subscription backlog | consumer stall, bookie storage pressure |
| `milvus-query-node-queueing` | Milvus | query-node latency | dependency failure, index build failure |
| `oceanbase-tenant-memory-exhaustion` | OceanBase | tenant memory exhaustion | tenant CPU throttling, major-merge delay, disk pressure, observer unavailable |
| `redis-logs-unavailable` | Redis | memory pressure, **with the log source down** | eviction storm, fragmentation, instance down — and the missing logs must be declared |

Each one is built so the ruled-out modes are the answers the *symptom* invites
and the *evidence* denies. That is the point of the corpus: a diagnosis that
follows the complaint rather than the measurement fails these cases.

## 6. What is deliberately not here

- **No real incident data.** Nothing in the corpus came from a production
  system, so nothing in it can leak one.
- **No prompt tuning against the corpus.** A prompt fitted to these cases would
  make the numbers rise and mean less. The corpus is a regression gate, not a
  training set.
- **No ranking of models or vendors.** The harness runs whatever provider you
  configure; a comparison it produced would be a comparison of your prices, your
  prompts and your packs as much as of any model.
