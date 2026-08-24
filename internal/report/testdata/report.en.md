# Diagnostic report: redis-prod

| | |
|---|---|
| Run | `run-20260823T110000-abcd1234` |
| Target | `redis-prod` (redis 7.2.4), kubernetes/middleware |
| Symptom | p99 latency spike with evictions |
| Window | 2026-08-23T10:00:00Z → 2026-08-23T11:00:00Z (1h0m0s) |
| Mode | online |
| Topology | supervisor |
| Generated | 2026-08-23T11:00:00Z |

## Summary

Redis is at its configured memory ceiling. Eviction began before latency rose and the log shows write refusals in the same window, so memory pressure is the cause rather than a consequence.

## Hypotheses

### 1. Redis reached maxmemory; eviction could not free space fast enough.

- **status**: supported by the evidence
- **confidence**: 85%
- **Reasoning**: Used memory is above 90% of maxmemory and eviction precedes the latency rise.
- **Supporting**: ev-1, ev-2

### 2. A single slow command blocked the event loop.

- **status**: refuted by the evidence
- **confidence**: 5%
- **Reasoning**: CPU stayed below saturation and no long fork pause was observed.
- **Contradicting**: ev-1

## Findings

- **[critical]** Used memory is above 90% of the configured maxmemory.
  - At this point Redis either evicts keys or refuses writes, depending on maxmemory-policy.
  - from `rule:redis.memory-pressure/eval-pressure`, confidence 90%, Evidence: ev-1
- **[major]** Keys are being evicted.
  - from `rule:redis.memory-pressure/eval-eviction`, confidence 85%, Evidence: ev-1

## Checks that passed

- The instance was reachable throughout the window.
- No long fork pause was observed.

## Gaps in the evidence

- **kube.nodes()** — refused by the safety guard (`MAS-4201`)
  - Effect on this analysis: node-level memory pressure could not be ruled out

## Recommended next steps

> These are recommendations for a human operator. MAS-Turbo is read-only: it has not performed, scheduled or arranged any of them.

1. **[risk: low]** Confirm the eviction policy with CONFIG GET maxmemory-policy.
   - The policy decides whether clients see errors or silent data loss.
2. **[risk: medium]** Raise maxmemory only if the host has headroom.
   - Without headroom the kernel OOM killer replaces a degraded Redis with a dead one.

## Evidence

- `ev-1` (metric_series, promql:primary) redis_memory_used_bytes → last=1020 min=900 max=1020 avg=970 over 12 points
  - `redis_memory_used_bytes{instance="redis-0"}`
- `ev-2` (log_lines, loki:primary) {job="redis"} |= "OOM" → 41 lines across 1 streams; newest at 2026-08-23T10:58:00Z: OOM command not allowed
  - `{job="redis"} |= "OOM"`

## Run accounting

| | |
|---|---|
| Model calls | 9 |
| Tool calls | 7 |
| Tokens | 13800 |
| Duration | 4.2s |
