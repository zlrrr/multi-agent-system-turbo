# 详细设计（LLD）：中间件知识广度

> **特性 ID**：`002-middleware-packs` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)

## 1. 新增文件

```
internal/knowledge/packs/mongodb.yaml
internal/knowledge/packs/pulsar.yaml
internal/knowledge/packs/milvus.yaml
internal/knowledge/packs/oceanbase.yaml
internal/knowledge/conformance_test.go
docs/en/knowledge-packs.md
docs/zh/knowledge-packs.md
```

除新增测试外，没有任何 `.go` 文件被修改。这就是 FR-010，并通过对 diff 的评审来检查。

## 2. 一致性契约

`conformance_test.go` 对库加载到的每个包做表驱动检查，因此未来新增的包会自动继承这条下限。

```go
type floor struct {
    middleware   string
    minSignals   int
    minPatterns  int
    minModes     int
    minPlaybooks int
    requiredModes []string // 规格中点名的失效模式
}
```

对每个包施加的检查：

| 检查 | 规则 | 需求 |
|---|---|---|
| 深度 | 各项计数满足下限 | FR-005 |
| 点名的失效模式 | `requiredModes` 中每一项都存在 | FR-001…004 |
| 双语 | 每对 `{en,zh}` 均完整且不相同 | CON-001 |
| 建议 | 每个失效模式至少 1 条建议 | FR-007 |
| 建议措辞 | 任何建议都不得匹配 `^(restarted\|scaled\|applied\|deleted\|fixed\|increased\|disabled\|enabled) ` 或其中文等价表述 | CON-003 |
| 风险词表 | 每条建议的风险取值为 low/medium/high | FR-007 |
| 守卫洁净 | 每条 `inspect` 命令都能通过 `safety.Guard.Authorize` | FR-006 |
| 表达式 | 每条 `evaluate` 与 `conclude.when` 都能在沙箱中编译 | FR-009 |
| 信号引用 | 每个 `{{signal:id}}` 都能解析 | FR-009 |
| 被引用的失效模式 | 每个 `conclude.failureMode` 都已声明 | FR-005 |
| 可达性 | 每个剧本至少有 1 个 collect 步骤，以及至少 1 个 conclude 或 finding | FR-005 |
| 覆盖诚实性 | 每个失效模式的 `indicators` 都提到某个已声明的信号 id 或日志模式 id | NFR-002 |
| 常开剧本 | 恰好有一个剧本没有 `matches`，以保证总有检查在跑 | HLD §4 |

表达式检查需要 rules 包，而这会让 `internal/knowledge` 依赖 `internal/rules` —— 方向是错的。
因此该测试放在 `knowledge_test` 包中并引入 `rules`，这只是测试依赖，不影响生产代码的分层。

## 3. 信号来源

每个包的指标名都来自一个广泛部署的 exporter。记录来源，正是让 NFR-001 可被读者核验的方式。

| 中间件 | 指标来源 | 前缀 |
|---|---|---|
| MongoDB | `percona/mongodb_exporter` | `mongodb_` |
| Pulsar | Pulsar broker 内置 Prometheus 端点 | `pulsar_` |
| Milvus | Milvus 内置指标 | `milvus_` |
| OceanBase | `obagent` / OceanBase exporter | `ob_` |

当 exporter 同时暴露旧名与新名时，包中采用新名，并在编写指南中标注备选名称。

## 4. 巡检命令

| 中间件 | 命令 | 守卫状态 |
|---|---|---|
| MongoDB | `mongosh --eval db.serverStatus()` 及其他白名单内的 eval | 已允许 |
| MongoDB | `mongosh --eval rs.status()` | 已允许 |
| Pulsar | `pulsar-admin brokers healthcheck`、`topics stats`、`namespaces list` | 可执行文件已允许；动词由一致性测试检查 |
| Milvus | 无 | 无白名单 CLI；诊断依靠指标与日志 |
| OceanBase | 无 | 巡检需要守卫未允许的 SQL 客户端 |

Milvus 与 OceanBase 不带 inspect 命令发布是刻意为之。另一条路是申请新增守卫白名单条目，
而那属于本特性未提出的规格变更 —— 一个悄悄需要更宽守卫的知识包，恰恰是本设计要避免的失败。

## 5. 剧本表达式环境

与 001-mvp-core §2.12 相同。每个采集槽位暴露：

```
.empty .series .count .latest .last .latestMin .min .max .avg .sum .delta .byLabel .summary
```

辅助函数为 `contains`、`matches`、`countMatching`、`ratio`、`pct`、`finite`。
日志槽位暴露 `.empty .count .lines .text .summary`。

对知识包作者而言最关键的一条指导：**阈值判断务必先用 `not X.empty` 兜底**。采集失败会让槽位
保持未设置从而跳过该步骤，但一个“采集到零条序列”的槽位若不加保护，会被当作 0 参与比较，
从而读起来像“健康”。

## 6. 测试矩阵

| 测试 | 层级 | 目标 | 需求 |
|---|---|---|---|
| `TestPackConformance` | 单元 | 每个内置包 | FR-005、FR-001…004 |
| `TestPackInspectCommandsPassTheGuard` | 单元 | 每个内置包 | FR-006 |
| `TestPackExpressionsCompile` | 单元 | 每个内置包 | FR-009 |
| `TestPackRecommendationsAreAdvisory` | 单元 | 每个内置包 | FR-007、CON-003 |
| `TestPackCoverageIsHonest` | 单元 | 每个内置包 | NFR-002 |
| `TestNewPacksRunAgainstStubTelemetry` | 集成 | 四个新增包 | FR-001…004 |
| 既有 `TestEmbeddedPacksValid`、`TestBilingualPackFields` | 单元 | 每个内置包 | CON-001 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | 初版详细设计 | `tasks.zh.md`、知识包 |
