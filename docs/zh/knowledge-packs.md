# 编写知识包

> **双语对照**：[`docs/en/knowledge-packs.md`](../en/knowledge-packs.md)
> **约束来源**：`specs/002-middleware-packs/` · **Schema 归属**：`internal/knowledge/pack.go`

知识包承载本系统对某一种中间件的全部认知：值得读取的指标、值得匹配的日志行、它可能出现的故障，
以及用来区分这些故障的有序检查。知识包是**带版本的 YAML 数据，而不是代码**。
新增一种中间件的支持不需要改 Go 代码、也不需要重新编译 —— 放在配置目录下的知识包会在启动时
与内置知识包一并加载。

本指南就是编写知识包的契约。其中的每一条都由 `internal/knowledge/conformance_test.go` 强制执行：
遵循本指南的知识包能通过 CI，不遵循的会被判失败，并给出说明原因的错误信息。

---

## 1. 知识包存放位置

| 来源 | 路径 | 加载时机 |
|---|---|---|
| 内置 | `internal/knowledge/packs/*.yaml` | 构建时嵌入二进制 |
| 本地 | `knowledge.pack_dirs` 中列出的任意目录 | 启动时，在内置知识包之后 |

知识包的 id 是 `metadata.middleware` + `/` + `metadata.name`。本地知识包若声明了与内置知识包
相同的 id，就会**替换**它 —— 这正是在不 fork 二进制的前提下修正已发布知识的受支持方式。
*不*受支持的是两个本地知识包声明同一个 id：那样谁生效将取决于目录顺序，
因此加载器会报告该冲突（`MAS-5002`），而不是自行了断。

`mas doctor` 会列出所有加载失败的知识包及其错误码；`mas packs` 会列出已加载的知识包及其来源。

---

## 2. Schema

```yaml
apiVersion: mas.turbo/v1        # 必填，取值固定
kind: KnowledgePack             # 必填，取值固定
metadata:
  middleware: redis             # 必填；与诊断目标的 `kind` 匹配
  name: redis-core              # 必填；同一中间件下唯一
  version: 1.0.0                # 必填；知识包自身的版本
  versionRange: ">=6.0"         # 可选；适用于哪些中间件版本
```

每一个面向运维人员的字符串都是**双语对**：

```yaml
description:
  en: "English text."
  zh: "中文文本。"
```

两半都必填。`Text.Complete()` 会拒绝任意一侧为空的双语对，因此仅有英文的知识包会直接加载失败，
而不会对中文使用者静默降级。

### 2.1 `signals` —— 测量什么

```yaml
signals:
  - id: memory_ratio                                    # 通过 {{signal:memory_ratio}} 引用
    promql: 'redis_memory_used_bytes{{.selector}} / clamp_min(redis_memory_max_bytes{{.selector}}, 1)'
    unit: ratio                                         # 自由文本；会在报告中展示
    description:
      en: "Used memory against maxmemory."
      zh: "已用内存与 maxmemory 之比。"
```

`{{.selector}}` 会在运行时被替换为诊断目标的标签选择器
（`{job="redis",instance="10.0.0.1:9121"}`），因此只需写出指标名，并把选择器放在 PromQL
标签匹配器应在的位置。做除法时请用 `clamp_min(x, 1)` 而不是裸除：分母为 0 会得到 `NaN`，
而 `NaN` 的比较结果恒为假 —— 那读起来就像“健康”。

### 2.2 `logPatterns` —— 一行日志意味着什么

```yaml
logPatterns:
  - id: oom_command
    regex: '(OOM command not allowed|used memory > .maxmemory.)'
    severity: critical            # info | minor | major | critical
    meaning:
      en: "Redis refused a write because it is at maxmemory."
      zh: "Redis 因达到 maxmemory 而拒绝了一次写入。"
```

正则采用 Go 的 [RE2](https://github.com/google/re2/wiki/Syntax) 语法 —— 不支持反向引用与环视。
非法正则会让知识包在加载阶段失败。

### 2.3 `failureModes` —— 它如何失败，以及该怎么办

```yaml
failureModes:
  - id: memory-pressure
    severity: major
    title:       { en: "…", zh: "…" }
    explanation: { en: "…", zh: "…" }     # 为什么会发生、代价是什么
    symptoms: ["memory", "oom", "内存"]    # 运维人员的用语，两种语言都要有
    indicators: ["memory_ratio high", "oom_command in logs"]
    recommendations:
      - risk: low                          # low | medium | high
        statement: { en: "…", zh: "…" }
        rationale: { en: "…", zh: "…" }    # 可选；为什么值得这么做
```

一致性测试强制执行两条规则：

- **每一个 indicator 都必须指向已声明的 signal 或 logPattern 的 id。** 列出无从测量的
  indicator，等于宣称了知识包并不具备的覆盖能力（`TestPackCoverageIsHonest`）。
- **每一条 recommendation 都是建议，绝不能是“已执行动作”的陈述。** “检查淘汰策略”是合法的；
  “已调大 maxmemory”不是。该扫描会同时检查两种语言（`TestPackRecommendationsAreAdvisory`）——
  因为本系统是只读的，而读起来像操作日志的报告会歪曲这一事实。

### 2.4 `playbooks` —— 有序的检查

```yaml
playbooks:
  - id: redis.memory-pressure
    title:       { en: "…", zh: "…" }
    description: { en: "…", zh: "…" }
    matches: ["memory", "oom", "内存"]     # 省略该字段即为“常开”剧本
    steps:
      - id: collect-usage
        collect:
          tool: promql.range
          args: { query: "{{signal:memory_ratio}}" }
          as: usage                        # 表达式读取的槽位名
      - id: eval-usage
        evaluate: "not usage.empty and usage.max > 0.9"
        onTrue:
          finding:
            severity: critical
            confidence: 0.9
            statement: { en: "…", zh: "…" }
            detail:    { en: "…", zh: "…" }
        onFalse:
          pass: { en: "…", zh: "…" }       # 这项检查排除了什么
      - id: conclude
        conclude:
          failureMode: memory-pressure
          when: "not usage.empty and usage.max > 0.9"
```

每个步骤**有且仅有** `collect`、`evaluate`、`conclude` 之一（`MAS-5014`）。
步骤按文件顺序执行，槽位只有在采集它的步骤之后才可被读取。

**每个知识包必须恰好有一个常开剧本**（不带 `matches`）。它是无论运维人员输入什么都会运行的
健康检查，因此应覆盖存活性，以及那些会让其下游一切判断都不可靠的故障。

### 2.5 `inspect` —— 只读命令

```yaml
inspect:
  - id: server-info
    binary: redis-cli
    args: ["-h", "{{.host}}", "-p", "{{.port}}", "INFO", "all"]
    description: { en: "…", zh: "…" }
```

每一条 inspect 命令都会**在 CI 中、在其发布之前**先过一遍安全护栏
（`TestPackInspectCommandsPassTheGuard`）。护栏默认拒绝，且没有任何配置项能放宽它，
因此不在白名单内的命令会在构建阶段失败，而不是到了运维现场才失败。
`{{.host}}` 与 `{{.port}}` 由环境适配器填入。

若该中间件没有位于白名单内的只读 CLI，**就不带 inspect 命令发布**，并用注释说明原因。
Milvus 与 OceanBase 正是如此：`obclient` 是一个完整的 SQL 客户端，护栏无法分辨 `-e`
参数里是 `SELECT` 还是 `DELETE`。指标与日志仍然覆盖了那些故障模式；这一缺口被如实记录，
而不是被粉饰过去。

### 2.6 `source` —— 源码在哪里

```yaml
source:
  repos: ["https://github.com/redis/redis"]
```

供源码工具据此查找某条错误信息背后的代码。网络不可用时，拉取会回退到本地缓存。

---

## 3. 表达式环境

表达式使用 [expr](https://expr-lang.org/)，运行在一个沙箱中，其中**只暴露该剧本已采集到的内容**：
没有进程环境变量、没有文件系统、没有网络。

指标槽位提供：

| 字段 | 含义 |
|---|---|
| `empty` | 查询没有返回任何 series 时为 true |
| `series`、`count` | series 数量；采样点数量 |
| `latest` | 各 series 最后一个值的**最大值** |
| `latestMin` | 各 series 最后一个值的最小值 |
| `min`、`max`、`avg`、`sum` | 跨全部 series 的全部采样点统计 |
| `delta` | 最后一个值减去第一个值 |
| `byLabel` | 标签值 → 该 series 最后一个值的映射 |
| `summary` | 供人阅读的一行摘要 |

`latest` 取最大值，是因为阈值检查几乎总是在问“是否**有任何**实例越线”。
若要表达“是否**所有**实例都已恢复”，请用 `latestMin`。

日志槽位提供 `empty`、`count`、`lines`（字符串切片）、`text`（各行拼接）与 `summary`。

辅助函数：`contains(haystack, needle)`（忽略大小写）、`matches(s, pattern)`、
`countMatching(lines, pattern)`、`lower(s)`、`ratio(a, b)`、`pct(a, b)`、
`isNaN(x)`、`finite(x)`。

> 引号内正则里的词是数据，不是槽位名。`countMatching(logs.lines,
> 'NotEnoughBookies|Not enough bookies')` 只读取 `logs` 一个槽位。

---

## 4. 空值规则

**每一次数值比较，都必须提及所比较槽位的 `.empty`。**
不这么做的知识包会被 `TestPackThresholdsGuardAgainstEmpty` 判为失败。

原因在于：查询没有返回任何 series 时，槽位中的数值全为 0，而 0 与几乎任何阈值相比都像是“健康”。
对一个当前部署并未导出的指标写下裸的 `usage.max > 0.9`，会得到“使用率处于正常范围”——
而这是一句系统从未测量过的断言。

两种读法都合理，选择哪一种由你显式决定：

```yaml
evaluate: "not usage.empty and usage.max > 0.9"   # 空 ⇒ 未知
evaluate: "up.empty or up.latest < 1"             # 空 ⇒ 目标已宕机
```

引擎会为此兜底：当一个读取了“无 series 指标槽位”的表达式求值为**假**时，
引擎会记录一条缺口（`MAS-5015`），而不是走假分支 —— 这项检查根本没有执行，
因此它的 finding 与 `pass` 文本都不得被报告。而 `up.empty or …` 这种刻意的读法求值为**真**，
不受影响。

日志槽位不受此规则约束：日志查询没有返回内容是一项真实的观察结果（“该时间窗内没有日志”），
而指标查询没有返回内容意味着该信号在此处并不存在。

---

## 5. 一致性下限

每一个已发布的知识包都会按 `internal/knowledge/conformance_test.go` 中声明的下限来度量。
未声明下限就发布的知识包会让 `TestEveryShippedPackHasAFloor` 失败 ——
因此这份契约无法靠“遗漏”绕过。

| 要求 | 下限 |
|---|---|
| signals | ≥ 10 |
| logPatterns | ≥ 6 |
| failureModes | ≥ 6，且必须包含规格文档点名的那些 id |
| playbooks | ≥ 3，其中恰好一个常开 |
| 每个 failureMode | ≥ 1 条 recommendation，且分级为 `low`/`medium`/`high` |
| 每个 playbook | 既要采集证据，又要得出 finding 或 conclusion |
| 每个 conclusion | 必须指向该知识包已声明的 failureMode |
| 全部内置知识包合计 | ≤ 512 KiB |

这些下限并非随意设定：signal 少于约 10 个时，剧本无法把原因与结果区分开
（它能看到时延升高了，却看不出磁盘能否解释这一点）；failureMode 少于 6 个时，
可供批判者权衡的备选解释就不够了。

---

## 6. 信号从哪里来

| 中间件 | Exporter / 端点 | 指标前缀 |
|---|---|---|
| Redis | `oliver006/redis_exporter` | `redis_` |
| Kafka | 配套标准 Kafka 规则的 JMX exporter | `kafka_` |
| MongoDB | `percona/mongodb_exporter` | `mongodb_ss_`、`mongodb_mongod_` |
| Pulsar | broker 与 BookKeeper 自带的 `/metrics` | `pulsar_`、`bookkeeper_`、`bookie_` |
| Milvus | Milvus 各组件自带的 `/metrics` | `milvus_` |
| OceanBase | `obagent` 的 Prometheus 端点 | `ob_` |

以上每一种的指标名都会在小版本之间发生变动。这在预期之内且已被处理：
查询没有返回 series 的信号，会变成一条点名该信号的缺口记录，而不是一个错误的读数。
请优先选择长期稳定的指标族；当某个知识包确实只适用于部分版本时，请设置 `versionRange`。

---

## 7. 一个完整的最小知识包

下面这个知识包可以加载、通过校验并实际运行。它**有意**低于一致性下限 ——
下限约束的是*已发布*的知识包，而不是本地知识包 —— 因此请把它当作起始骨架，而不是达标目标。

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

## 8. 提交前的自检清单

1. `go test ./internal/knowledge/...` —— schema、一致性、护栏、表达式。
2. `go test ./internal/rules/...` —— 该知识包能在桩遥测下抵达其故障模式，且在健康读数下保持沉默。
3. 每一个面向运维人员的字符串都填齐了两种语言。
4. 每一次数值比较都提及了所比较槽位的 `.empty`。
5. 每一条 recommendation 在两种语言下读起来都是建议。
6. 恰好有一个常开剧本。
7. inspect 命令能通过护栏 —— 或者一条都不带，并用注释说明原因。
8. 在 `conformance_test.go` 的 `floors` 中加入该知识包的下限，并在上面的指标来源表中补上对应行。
