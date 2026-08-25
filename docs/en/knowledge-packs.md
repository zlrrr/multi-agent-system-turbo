# Writing a Knowledge Pack

> **Bilingual pair**: [`docs/zh/knowledge-packs.md`](../zh/knowledge-packs.md)
> **Governs**: `specs/002-middleware-packs/` · **Schema owner**: `internal/knowledge/pack.go`

A knowledge pack is what this system knows about one middleware: the metrics
worth reading, the log lines worth matching, the ways it fails, and the ordered
checks that tell those ways apart. Packs are **versioned YAML data, not code**.
Adding support for a new middleware needs no Go change and no rebuild — a pack
in a configured directory is loaded at startup alongside the built-in ones.

This guide is the contract for writing one. Everything in it is enforced by
`internal/knowledge/conformance_test.go`, so a pack that follows the guide
passes CI and a pack that does not fails it with a message that says why.

---

## 1. Where packs live

| Source | Path | Loaded |
|---|---|---|
| Built in | `internal/knowledge/packs/*.yaml` | Embedded in the binary at build time |
| Local | Any directory listed in `knowledge.pack_dirs` | At startup, after the built-in packs |

A pack's id is `metadata.middleware` + `/` + `metadata.name`. A local pack that
claims a built-in pack's id **replaces** it — that is the supported way to
correct shipped knowledge without forking the binary. What is *not* supported is
two local packs claiming the same id: which one won would depend on directory
order, so the loader reports the collision (`MAS-5002`) instead of resolving it.

`mas doctor` reports every pack that failed to load, with its error code, and
`mas packs` lists what is loaded and where each came from.

---

## 2. Schema

```yaml
apiVersion: mas.turbo/v1        # required, exactly this value
kind: KnowledgePack             # required, exactly this value
metadata:
  middleware: redis             # required; matches the `kind` in a target
  name: redis-core              # required; unique per middleware
  version: 1.0.0                # required; the pack's own version
  versionRange: ">=6.0"         # optional; which middleware versions this suits
```

Every operator-facing string is a **bilingual pair**:

```yaml
description:
  en: "English text."
  zh: "中文文本。"
```

Both halves are required. `Text.Complete()` rejects a pair with either side
blank, so an English-only pack fails to load rather than degrading silently for
Chinese-speaking operators.

### 2.1 `signals` — what to measure

```yaml
signals:
  - id: memory_ratio                                    # referenced as {{signal:memory_ratio}}
    promql: 'redis_memory_used_bytes{{.selector}} / clamp_min(redis_memory_max_bytes{{.selector}}, 1)'
    unit: ratio                                         # free text; shown in reports
    description:
      en: "Used memory against maxmemory."
      zh: "已用内存与 maxmemory 之比。"
```

`{{.selector}}` is substituted with the target's label selector at run time
(`{job="redis",instance="10.0.0.1:9121"}`), so write the metric name and let the
selector land where a PromQL label matcher belongs. Divide with `clamp_min(x, 1)`
rather than bare division: a denominator of zero yields `NaN`, and `NaN`
comparisons are false, which reads as healthy.

### 2.2 `logPatterns` — what a log line means

```yaml
logPatterns:
  - id: oom_command
    regex: '(OOM command not allowed|used memory > .maxmemory.)'
    severity: critical            # info | minor | major | critical
    meaning:
      en: "Redis refused a write because it is at maxmemory."
      zh: "Redis 因达到 maxmemory 而拒绝了一次写入。"
```

Patterns are Go [RE2](https://github.com/google/re2/wiki/Syntax) — no
backreferences, no lookaround. An invalid regex fails the pack at load time.

### 2.3 `failureModes` — how it fails, and what to do

```yaml
failureModes:
  - id: memory-pressure
    severity: major
    title:       { en: "…", zh: "…" }
    explanation: { en: "…", zh: "…" }     # why this happens and what it costs
    symptoms: ["memory", "oom", "内存"]    # operator vocabulary, both languages
    indicators: ["memory_ratio high", "oom_command in logs"]
    recommendations:
      - risk: low                          # low | medium | high
        statement: { en: "…", zh: "…" }
        rationale: { en: "…", zh: "…" }    # optional; why this is worth doing
```

Two rules the conformance test enforces:

- **Every indicator must name a declared signal or log-pattern id.** A failure
  mode that lists indicators nothing measures advertises coverage the pack does
  not have (`TestPackCoverageIsHonest`).
- **Every recommendation is advice, never a report of an action.** "Check the
  eviction policy" is valid; "Increased maxmemory" is not. The scan runs against
  both languages (`TestPackRecommendationsAreAdvisory`), because this system is
  read-only and a report that reads like an action log misrepresents it.

### 2.4 `playbooks` — the ordered checks

```yaml
playbooks:
  - id: redis.memory-pressure
    title:       { en: "…", zh: "…" }
    description: { en: "…", zh: "…" }
    matches: ["memory", "oom", "内存"]     # omit to make the playbook always-on
    steps:
      - id: collect-usage
        collect:
          tool: promql.range
          args: { query: "{{signal:memory_ratio}}" }
          as: usage                        # the slot name expressions read
      - id: eval-usage
        evaluate: "not usage.empty and usage.max > 0.9"
        onTrue:
          finding:
            severity: critical
            confidence: 0.9
            statement: { en: "…", zh: "…" }
            detail:    { en: "…", zh: "…" }
        onFalse:
          pass: { en: "…", zh: "…" }       # what this check ruled out
      - id: conclude
        conclude:
          failureMode: memory-pressure
          when: "not usage.empty and usage.max > 0.9"
```

A step declares **exactly one** of `collect`, `evaluate` or `conclude`
(`MAS-5014`). Steps run in file order, and a slot is readable only after the
step that collected it.

**Exactly one playbook per pack must be always-on** (no `matches`). It is the
health check that runs whatever the operator typed, so it should cover
liveness and the failures that make everything downstream unreliable.

### 2.5 `inspect` — read-only commands

```yaml
inspect:
  - id: server-info
    binary: redis-cli
    args: ["-h", "{{.host}}", "-p", "{{.port}}", "INFO", "all"]
    description: { en: "…", zh: "…" }
```

Every inspect command is run through the safety guard **in CI, before it can
ship** (`TestPackInspectCommandsPassTheGuard`). The guard is deny-by-default and
no configuration key can widen it, so a command outside the allow-list fails the
build rather than failing at an operator's site. `{{.host}}` and `{{.port}}` are
filled in by the environment adapter.

If the middleware has no read-only CLI in the allow-list, **ship the pack
without inspect commands** and say so in a comment. Milvus and OceanBase do
exactly that: `obclient` is a full SQL client, and the guard cannot tell a
`SELECT` from a `DELETE` inside a `-e` argument. Metrics and logs still cover
the failure modes; the gap is recorded rather than papered over.

An inspect command runs in one of two places, and you write it once for both:
on a host, the local adapter runs it directly; in Kubernetes, it runs inside the
pod. Write the template as a client on the same machine as the server —
`{{.host}}` becomes the loopback address in a container, and a `{{.port}}` whose
value is unknown there is dropped along with the flag that introduced it, so the
command falls back to the client's own default. Both paths go through the same
allow-list, so a command that runs on a host runs in a pod and one that is
refused on a host is refused in a pod.

### 2.6 `source` — where the code is

```yaml
source:
  repos: ["https://github.com/redis/redis"]
```

Used by the source tools to look up the code behind an error string. Fetching
falls back to the local cache when the network is unavailable.

---

## 3. The expression environment

Expressions are [expr](https://expr-lang.org/) and run in a sandbox that
exposes **only what the playbook collected**: no process environment, no
filesystem, no network.

A metric slot exposes:

| Field | Meaning |
|---|---|
| `empty` | true when the query returned no series |
| `series`, `count` | number of series; number of samples |
| `latest` | the **maximum** across series of each series' last value |
| `latestMin` | the minimum across series of each series' last value |
| `min`, `max`, `avg`, `sum` | across all samples of all series |
| `delta` | last minus first |
| `byLabel` | map from label value to that series' last value |
| `summary` | the human-readable one-line summary |

`latest` is a maximum because a threshold check almost always means "is *any*
instance over the line". Use `latestMin` for "have *all* instances recovered".

A log slot exposes `empty`, `count`, `lines` (a string slice), `text` (the lines
joined) and `summary`.

Helper functions: `contains(haystack, needle)` (case-insensitive),
`matches(s, pattern)`, `countMatching(lines, pattern)`, `lower(s)`,
`ratio(a, b)`, `pct(a, b)`, `isNaN(x)`, `finite(x)`.

> Words inside a quoted regex are data, not slot names. `countMatching(logs.lines,
> 'NotEnoughBookies|Not enough bookies')` reads only the `logs` slot.

---

## 4. The emptiness rule

**Every numeric comparison must mention `.empty` on the slot it compares.**
`TestPackThresholdsGuardAgainstEmpty` fails a pack that does not.

The reason is that a query returning no series produces a slot whose numbers are
all zero, and zero compares as healthy against almost every threshold. A bare
`usage.max > 0.9` on a metric this deployment does not export reports "usage is
within normal bounds" — a statement the system never measured.

Both readings are legitimate, and the choice is yours to make explicitly:

```yaml
evaluate: "not usage.empty and usage.max > 0.9"   # empty ⇒ unknown
evaluate: "up.empty or up.latest < 1"             # empty ⇒ the target is down
```

The engine backs this up. When an expression that reads a metric slot with no
series comes out **false**, the engine records a gap (`MAS-5015`) instead of
taking the false branch: the check did not run, so neither its finding nor its
`pass` text may be reported. The deliberate `up.empty or …` reading comes out
*true* and is unaffected.

Log slots are exempt: a log query returning nothing is a real observation
("nothing was logged in the window"), while a metric query returning nothing
means the signal does not exist here.

---

## 5. Conformance floors

Every shipped pack is measured against a floor declared in
`internal/knowledge/conformance_test.go`. A pack that ships without a floor fails
`TestEveryShippedPackHasAFloor`, so the contract cannot be skipped by omission.

| Requirement | Floor |
|---|---|
| Signals | ≥ 10 |
| Log patterns | ≥ 6 |
| Failure modes | ≥ 6, including the ids the specification names |
| Playbooks | ≥ 3, exactly one of them always-on |
| Every failure mode | ≥ 1 recommendation, each graded `low`/`medium`/`high` |
| Every playbook | collects evidence **and** reaches a finding or conclusion |
| Every conclusion | names a failure mode the pack declares |
| Embedded packs, in total | ≤ 512 KiB |

The floors are not arbitrary: below roughly ten signals a playbook cannot
separate a cause from its effect (it can see that latency rose, but not whether
the disk explains it), and below six failure modes there are not enough
alternatives for a critic to weigh.

---

## 6. Where the signals come from

| Middleware | Exporter / endpoint | Metric prefix |
|---|---|---|
| Redis | `oliver006/redis_exporter` | `redis_` |
| Kafka | JMX exporter with the standard Kafka rules | `kafka_` |
| MongoDB | `percona/mongodb_exporter` | `mongodb_ss_`, `mongodb_mongod_` |
| Pulsar | Broker and BookKeeper native `/metrics` | `pulsar_`, `bookkeeper_`, `bookie_` |
| Milvus | Milvus components' native `/metrics` | `milvus_` |
| OceanBase | `obagent` Prometheus endpoint | `ob_` |

Metric names drift between minor releases of every one of these. That is
expected and handled: a signal whose query returns no series becomes a recorded
gap naming the signal, not a false reading. Prefer the metric family that has
been stable longest, and set `versionRange` when a pack genuinely only suits
some versions.

---

## 7. A complete minimal pack

This pack loads, validates and runs. It is below the conformance floor on
purpose — the floor governs *shipped* packs, not local ones — so use it as a
starting shape rather than a target.

```yaml
apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata:
  middleware: examplewaredb
  name: exampleware-minimal
  version: 1.0.0
  versionRange: ">=1.0"

signals:
  - id: up
    promql: 'up{{.selector}}'
    unit: bool
    description:
      en: "1 when Prometheus can scrape the node, 0 when it cannot."
      zh: "Prometheus 能抓取到该节点时为 1，抓不到为 0。"
  - id: queue_depth
    promql: 'max(exampleware_queue_depth{{.selector}})'
    unit: items
    description:
      en: "Items waiting to be processed."
      zh: "等待处理的条目数。"

logPatterns:
  - id: queue_full
    regex: '(queue is full|QueueFullException)'
    severity: critical
    meaning:
      en: "The queue rejected work because it is full."
      zh: "队列已满，拒绝了新的任务。"

failureModes:
  - id: queue-backlog
    severity: major
    title:
      en: "Work queue backing up"
      zh: "工作队列积压"
    explanation:
      en: "Work arrives faster than it is processed. Once the queue is full the node rejects work outright."
      zh: "任务到达的速度快于处理速度。队列一旦写满，该节点会直接拒绝新任务。"
    symptoms: ["queue", "backlog", "slow", "队列", "积压", "慢"]
    indicators: ["queue_depth rising", "queue_full in logs"]
    recommendations:
      - risk: low
        statement:
          en: "Compare the arrival rate against the processing rate before adding capacity."
          zh: "在扩容之前，先比较任务到达速率与处理速率。"

playbooks:
  - id: examplewaredb.health
    title:
      en: "Node health"
      zh: "节点健康度"
    description:
      en: "Runs on every diagnosis: is the node scraped, and is its queue draining?"
      zh: "每次诊断都会运行：节点是否可被抓取、队列是否在下降？"
    steps:
      - id: collect-up
        collect:
          tool: promql.range
          args: { query: "{{signal:up}}" }
          as: up
      - id: collect-queue
        collect:
          tool: promql.range
          args: { query: "{{signal:queue_depth}}" }
          as: queue
      - id: eval-up
        evaluate: "up.empty or up.latest < 1"
        onTrue:
          finding:
            severity: critical
            confidence: 0.9
            statement:
              en: "The node could not be scraped during the window."
              zh: "在该时间窗内无法抓取到该节点。"
            detail:
              en: "Everything downstream of this is unreliable until it is resolved."
              zh: "在此问题解决前，其下游的一切判断都不可靠。"
        onFalse:
          pass:
            en: "The node was reachable throughout the window."
            zh: "在整个时间窗内该节点保持可达。"
      - id: eval-queue
        evaluate: "not queue.empty and queue.delta > 0 and queue.latest > 1000"
        onTrue:
          finding:
            severity: major
            confidence: 0.8
            statement:
              en: "The work queue grew across the window and is substantial."
              zh: "工作队列在时间窗内持续增长且规模可观。"
            detail:
              en: "While this holds the queue can only grow, whatever its current size."
              zh: "只要这个关系成立，无论当前队列多长，它都只会继续增长。"
        onFalse:
          pass:
            en: "The work queue is stable or draining."
            zh: "工作队列保持稳定或正在下降。"
      - id: conclude-queue
        conclude:
          failureMode: queue-backlog
          when: "not queue.empty and queue.delta > 0 and queue.latest > 1000"

source:
  repos: []
```

---

## 8. Checklist before opening a pull request

1. `go test ./internal/knowledge/...` — schema, conformance, guard, expressions.
2. `go test ./internal/rules/...` — the pack reaches its failure modes against
   stub telemetry, and stays quiet on healthy readings.
3. Every operator-facing string has both languages filled in.
4. Every numeric comparison mentions `.empty` on the slot it compares.
5. Every recommendation reads as advice, in both languages.
6. Exactly one playbook is always-on.
7. Inspect commands pass the guard — or there are none, with a comment saying why.
8. Add the pack's floor to `floors` in `conformance_test.go`, and its row to the
   metric-source table above.
