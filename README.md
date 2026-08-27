# MAS-Turbo

**A read-only diagnostic multi-agent system for open-source middleware.**

[中文说明](./README.zh.md) · [User manual](./docs/en/user-manual.md) · [Configuration](./docs/en/configuration.md) · [Knowledge packs](./docs/en/knowledge-packs.md) · [Evaluation](./docs/en/evaluation.md) · [Error codes](./docs/en/error-codes.md)

---

When a Redis cluster starts evicting keys or a Kafka consumer group falls behind,
the first thirty minutes go into *assembling context*: opening Grafana, guessing
the right PromQL, tailing pods, remembering which `INFO` field matters. The
analysis — the part that needs expertise — starts only after that, usually under
pressure, and usually not by the person who knows the system best.

MAS-Turbo does the assembly, applies middleware-specific expert knowledge, shows
its evidence, and tells you what it could not check.

**It performs no action against the systems it inspects.** No restart, no
configuration change, no `FLUSHALL`. A safety guard sits between every capability
and the outside world, refuses anything not on a read-only allow-list, and has no
setting that disables it.

## Try it in one command

```bash
git clone https://github.com/zlrrr/multi-agent-system-turbo
cd multi-agent-system-turbo
make demo
```

No credentials, no cluster, no model API. Local stubs serve a coherent Redis
memory-pressure scenario, and you get three reports: the deterministic
short-circuit path, the full multi-agent investigation, and the same
investigation in Chinese.

## What a report looks like

```markdown
## Summary

Redis is at its configured memory ceiling. Eviction began before latency rose and
the log shows write refusals in the same window, so memory pressure is the cause
rather than a consequence. The container was not OOM-killed: the limit being hit
is Redis's own maxmemory.

## Hypotheses

### 1. Redis reached maxmemory; eviction could not free space fast enough.
- **status**: supported by the evidence   - **confidence**: 85%
- **Reasoning**: Three independent sources agree, and eviction preceding latency
  rules out latency as the cause.
- **Supporting**: ev-1, ev-2

### 2. A single slow command blocked the event loop.
- **status**: refuted by the evidence     - **confidence**: 5%
- **Reasoning**: Contradicted by the CPU and fork evidence collected in this run.

## Gaps in the evidence
- **kube.nodes()** — refused by the safety guard (`MAS-4201`)
  - Effect on this analysis: node-level memory pressure could not be ruled out
```

## How it works

Two phases, and the order is the design.

**Deterministic first.** Playbooks from the knowledge pack run with *no model in
the loop*: collect → evaluate → conclude. If a rule settles the question with
enough confidence, the run stops there — routine incidents cost nothing and
return in under two seconds.

**Agents only where rules are inconclusive.** A planner decides what is still
unsettled; specialised investigators — one per evidence domain, concurrent —
gather targeted evidence; a correlator ranks hypotheses; a critic challenges each
against the evidence; a reporter writes it up. The critic matters: an explanation
that has never been challenged is just the first one that came to mind.

```
request ─▶ admission ─▶ ┌─ deterministic playbooks ─┐─▶ report
                        │   (zero model calls)      │
                        └─ agents, if inconclusive ─┘
                              every tool call ─▶ safety guard ─▶ read-only allow-list
```

## What makes it trustworthy

| Property | How it is guaranteed |
|---|---|
| Cannot mutate a target | One choke point every effect passes through; deny-by-default; no setting disables it. An adversarial suite tries FLUSHALL in every casing, argument injection, `pods/exec`, and pack-supplied commands — none arrive |
| A missing measurement is never a healthy one | A check whose input failed to collect is *skipped* with a recorded gap, never evaluated as passing |
| A source being down does not lose the analysis | Every failure becomes a gap with a code and a stated effect on confidence; the run completes |
| Results are reproducible | Identical input produces an identical report, including under the topologies that run roles concurrently |
| Runs are auditable | Every tool call, model exchange and verdict is persisted with an integrity digest; replay reproduces the report with the network off |
| Credentials never leak | Redaction at the log handler, not the call site; secrets cannot be printed in any format |

## Coverage today

| | Status |
|---|---|
| **Middleware** | Redis, Kafka, MongoDB, Pulsar, Milvus and OceanBase knowledge packs ship. Anything else is pack-only work: see the [pack-authoring guide](./docs/en/knowledge-packs.md) — no Go change, no rebuild |
| **Telemetry** | Prometheus, VictoriaMetrics, Thanos, Mimir; Loki |
| **Environments** | Kubernetes (read-only API, plus opt-out in-container inspection); local host |
| **Source** | Network repository with automatic fallback to a local mirror, plus code search |
| **Models** | Anthropic, any OpenAI-compatible endpoint, and a deterministic mock — routable per agent role, with cost reported per role when you supply prices |
| **Topologies** | `supervisor` (default), `single` (control condition), `plan-execute` (adaptive), `debate` (adversarial), `blackboard` (data-driven) |
| **Interfaces** | CLI, HTTP API (bearer tokens with `read`/`diagnose` scopes and optional per-tenant partitioning; refuses to bind off-host without credentials), a read-only web console at `/ui/`, container image |
| **Run store** | Filesystem, in-memory, or any S3-compatible bucket — shared by every replica, with each step an immutable object |

Deliberately not here yet: rate limiting, per-tenant budgets, and starting a
diagnosis from the browser — the console reads, the CLI and API write.
The [user manual](./docs/en/user-manual.md#14-what-is-deliberately-not-here-yet)
says so plainly so you can plan around it.

## Quick start

```bash
docker pull ghcr.io/zlrrr/multi-agent-system-turbo:latest

# Validate configuration and probe every endpoint
mas doctor

# Diagnose
mas diagnose --target redis-prod --symptom "p99 latency spike" --since 1h

# Compare topologies on the same case
for t in single supervisor plan-execute debate blackboard; do
  mas diagnose -t redis-prod -s "latency spike" --topology "$t" -f json -o "$t.json"
done

# Run the corpus of cases with known causes against the recorded baseline
mas eval --matrix --baseline internal/eval/baseline.json
```

See the [user manual](./docs/en/user-manual.md) for configuration, RBAC, the API
and knowledge-pack authoring, and the [evaluation guide](./docs/en/evaluation.md)
for what `mas eval` measures and how to write your own cases.

## Development

```bash
make build          # bin/mas and bin/sddctl
make test           # the full suite; no test needs a network
make ci             # what CI enforces: fmt, vet, lint, race tests, SDD checks, build, corpus
make eval           # the case corpus against its baseline; non-zero exit on a regression
make eval-baseline  # re-record the baseline (review the diff before committing)
make sdd-verify     # parity, cascade freshness, requirement coverage, declared tests
make docker         # container image
```

This project is built specification-first. The chain — goal → spec → plan →
high-level design → low-level design → tasks → code — lives in
[`specs/001-mvp-core/`](./specs/001-mvp-core/), and the rules it follows are in
[`.specify/memory/constitution.md`](./.specify/memory/constitution.md). CI
enforces them: every document exists in English and Chinese, no artifact may be
derived from a stale upstream, every requirement must be claimed by a task, and
every test a task names must actually exist — that last check was added after it
found six task rows marked done whose tests had never been written.

## Licence

Apache 2.0. See [LICENSE](./LICENSE).
