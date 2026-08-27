# 诊断报告: redis-prod

| | |
|---|---|
| 运行编号 | `run-20260823T110000-abcd1234` |
| 诊断目标 | `redis-prod` (redis 7.2.4), kubernetes/middleware |
| 症状描述 | p99 latency spike with evictions |
| 时间窗口 | 2026-08-23T10:00:00Z → 2026-08-23T11:00:00Z (1h0m0s) |
| 运行模式 | online |
| 拓扑 | supervisor |
| 生成时间 | 2026-08-23T11:00:00Z |

## 结论摘要

Redis is at its configured memory ceiling. Eviction began before latency rose and the log shows write refusals in the same window, so memory pressure is the cause rather than a consequence.

## 假设

### 1. Redis reached maxmemory; eviction could not free space fast enough.

- **状态**: 证据支持
- **置信度**: 85%
- **推理过程**: Used memory is above 90% of maxmemory and eviction precedes the latency rise.
- **支持证据**: ev-1, ev-2

### 2. A single slow command blocked the event loop.

- **状态**: 证据反驳
- **置信度**: 5%
- **推理过程**: CPU stayed below saturation and no long fork pause was observed.
- **反对证据**: ev-1

## 发现

- **[严重]** Used memory is above 90% of the configured maxmemory.
  - At this point Redis either evicts keys or refuses writes, depending on maxmemory-policy.
  - 来源 `rule:redis.memory-pressure/eval-pressure`, 置信度 90%, 证据: ev-1
- **[主要]** Keys are being evicted.
  - 来源 `rule:redis.memory-pressure/eval-eviction`, 置信度 85%, 证据: ev-1

## 已通过的检查

- The instance was reachable throughout the window.
- No long fork pause was observed.

## 证据缺口

- **kube.nodes()** — 被安全守卫拒绝 (`MAS-4201`)
  - 对本次分析的影响: node-level memory pressure could not be ruled out

## 建议的后续动作

> 以下均为给人类运维人员的建议。MAS-Turbo 是只读的：它没有执行、也没有安排其中任何一项操作。

1. **[风险: 低]** Confirm the eviction policy with CONFIG GET maxmemory-policy.
   - The policy decides whether clients see errors or silent data loss.
2. **[风险: 中]** Raise maxmemory only if the host has headroom.
   - Without headroom the kernel OOM killer replaces a degraded Redis with a dead one.

## 证据

- `ev-1` (metric_series, promql:primary) redis_memory_used_bytes → last=1020 min=900 max=1020 avg=970 over 12 points
  - `redis_memory_used_bytes{instance="redis-0"}`
- `ev-2` (log_lines, loki:primary) {job="redis"} |= "OOM" → 41 lines across 1 streams; newest at 2026-08-23T10:58:00Z: OOM command not allowed
  - `{job="redis"} |= "OOM"`

## 运行统计

| | |
|---|---|
| 模型调用 | 9 |
| 工具调用 | 7 |
| Token | 13800 |
| 耗时 | 4.2s |
| 成本 | 未定价 —— 请设置 llm.pricing |
