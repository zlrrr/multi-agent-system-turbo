# 用已知原因的 case 度量 MAS-Turbo

> **双语对照**：[`../en/evaluation.md`](../en/evaluation.md)
> 适用于 MAS-Turbo 0.1.x · 另见：[用户手册](./user-manual.md) · [知识包](./knowledge-packs.md) · [错误码](./error-codes.md)

---

## 1. 它度量什么，不度量什么

`mas eval` 运行一个由**诊断 case** 组成的语料库。每个 case 携带合成遥测数据，
以及一次正确诊断应当得出的故障模式。整条流水线都会真实跑起来 ——
入口与 `mas diagnose` 完全相同，并通过真实 HTTP 访问桩化的指标与日志服务 ——
因此查询构造、安全护栏对每次调用的判定、解码、规则引擎与智能体全都被真正执行，
而不是被 mock 掉。

**语料库是合成的。** 它度量的是与其自身标签的一致程度，
而不是在真实故障上的准确率。满分意味着系统的行为符合语料库作者的预期 ——
这比“它诊断得对”更弱，但同样有用：它正是用来抓住那种
“改了知识包之后悄悄什么结论都不再得出”的回归。

有三件事它刻意不做：

- **不给单一分数。** 四类结果并排呈现，绝不合并。漏判与错误结论是两种不同的失败 ——
  前者让运维人员留在原地，后者自信地把他们送错方向 ——
  任何加权求和都会让“用漏判换错误结论”的改动看起来像是进步。
- **不做文本相似度。** 打分只读故障模式 id 与缺口错误码，
  绝不读摘要或假设陈述。相似度打分会奖励“把提示词复述一遍”的模型，
  并产出一个谁也说不清含义的数字。
- **不对模型下结论。** 使用 `mock` provider 时，脚本中本就写着答案，
  因此这次运行对模型质量不说明任何事情。每一种输出形式都会明说这一点 ——
  表格里有，JSON 里也有。

## 2. 如何运行

```bash
mas eval                        # 内置语料库，配置中的拓扑
mas eval --matrix               # 全部拓扑，同一批 case
mas eval --topology debate      # 指定单一拓扑
mas eval --cases ./my-cases     # 你自己的 case，与内置语料库一同运行
mas eval --json                 # 机器可读，caveat 以字段形式携带
mas eval --lang zh              # 表格与 caveat 使用中文
```

`--cases` 是**追加**目录，绝不会取代内置语料库，
因此你自己的 case 不会悄悄把回归基线移除。无法读取的路径会报 `MAS-9104`，
而不是被跳过 —— 否则一个写错的路径就会去跑内置语料库并报告成功。

只要有任何 case 漏判或得出被排除的结论，退出码即非零（`MAS-9103`）。
这正是它可以直接用作 CI 闸门的原因：

```yaml
- name: 在全部拓扑上运行语料库
  run: mas eval --matrix
```

`make eval` 做的是同一件事，`make ci` 已将其包含在内。

## 3. 如何阅读结果

```
CASE                                   TOPOLOGY    RESULT  FALSE  GAPS  CALLS  COST
kafka-broker-loss-under-replicated     supervisor  hit     0      ok    8      unpriced
mongodb-replication-lag-write-concern  supervisor  miss    0      ok    8      unpriced

supervisor     5/6 hit · 1 miss · 0 false conclusion(s) · 0 gap(s) missed

  mongodb-replication-lag-write-concern / supervisor:
    not concluded: replication-lag
```

| 列 | 含义 |
|---|---|
| `RESULT` | `hit` —— 期望的结论全部得出、被排除的一个都没得出、期望的缺口全部声明。否则为 `miss`、`wrong` 或 `failed` |
| `FALSE` | 该 case 明确排除、却仍被得出的结论数量 |
| `GAPS` | 该 case 期望的缺口是否都被真正声明 |
| `CALLS` | 模型调用次数，让拓扑的成本与其结果并排可见 |
| `COST` | 已配置价格时的金额。`unpriced` 永远不等于 `$0.00` |

失败的 case 会在汇总下方展开，指出漏掉了什么、以及不该得出却得出了什么，
这样没人需要为了搞清“是哪个模式变了”而重跑一遍。

## 4. 编写自己的 case

一个 case 就是一份 YAML 文档。把它放在任意目录，然后用 `--cases` 指向该目录。

```yaml
apiVersion: mas.turbo/v1
kind: DiagnosticCase
metadata:
  id: redis-maxmemory-eviction        # 唯一；会出现在表格中
  middleware: redis                   # 必须存在对应知识包
  version: "7.2.4"                    # 用于选中知识包的版本范围
  title:
    en: "Redis at maxmemory, evicting, refusing writes"
    zh: "Redis 触及 maxmemory，正在驱逐并拒绝写入"
  description:
    en: >-
      What the case is testing, and which wrong answers it rules out.
    zh: >-
      本用例检验什么，以及它排除了哪些错误答案。

symptom:                              # 由它来选中要运行的 playbook
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

逐字段说明：

- **`metadata.title`、`metadata.description`、`symptom`** 必须**两种语言都有**。
  只翻译一半的 case 会被加载器拒绝。
- **`symptom`** 不是装饰：它决定哪些 playbook 会运行，
  与真实诊断中的机制完全一致。一个未命中任何 playbook 匹配词的症状，
  只会触发那些始终运行的 playbook。
- **`telemetry.metrics`** 的键是按**展开后 PromQL 的子串**匹配的，
  最长键优先 —— 因此 case 不必照抄知识包里的完整表达式，
  重命名一个 signal 也不会毫无诊断意义地弄坏所有 case。值就是该查询返回的序列，按顺序排列。
- 一个**没有**命中任何键的查询会返回**空**结果，而不是 0。
  空表示“该部署并不导出这个指标”，规则引擎会将其记为缺口；
  而 0 是一次测量结果，并且是错误的测量结果。
- **`expect.failure_modes`** 必须是知识包已声明的 id。
  指向任何知识包都未声明的模式，会在加载时报 `MAS-9101`，因为它永远无法通过。
- **`expect.not_failure_modes`** 是那些看似合理的错误答案。
  正是这一半，能抓住“系统变得更自信而不是更正确”的退化，
  所以请至少给每个 case 写一个。

执行 `mas packs --show redis` 可查看某个知识包声明了哪些故障模式 id。

### 检验诚实，而不只是正确

一个 case 可以把某个数据源拿走：

```yaml
telemetry:
  withhold: [logs]                    # "logs" 或 "metrics"
  metrics:
    redis_memory_used_bytes: [940, 970, 990]
    redis_memory_max_bytes: [1000, 1000, 1000]
expect:
  failure_modes: [memory-pressure]
  gaps: ["MAS-4102"]                  # 日志数据源以 HTTP 错误应答
```

被拿走的数据源由一个**必定失败**的处理器提供，
因此本次运行是真正经历了“缺失”，而不是被塞了一份空数据。
随后 `expect.gaps` 要求本次运行**明说**证据缺失。
若没有这一条，一个在缺少证据的情况下仍得出正确结论的系统，
会以“正确”通过，而它其实是靠运气到达终点的。

拿走数据源却不期望任何缺口，会在加载时被拒绝：那样除了“证据更少”之外什么也没检验。

缺口错误码就是采集器针对该类失败所抛出的那一个 ——
`MAS-4102` 表示日志数据源以某个 HTTP 状态码应答，`MAS-4101` 表示完全无法连通，
`MAS-4002` 与 `MAS-4001` 是指标数据源的对应项。
`mas errcodes --filter MAS-41` 列出日志相关错误码，`--filter MAS-40` 列出指标相关的。

这也正是发现了一个真实缺陷的那个 case。现在，已配置的数据源会在准入阶段被探测一次，
因此数据源不可用就是一个缺口 —— 无论本次运行的控制流是否恰好去查询过它。
在此之前，`supervisor` 会报告日志缺失，而 `single` 不会 ——
差别在于哪个智能体跑了，而不在于该部署到底能告诉我们什么。

## 5. 内置语料库里有什么

21 个 case，每个知识包至少 3 个。这个下限由测试强制，
因为"每个知识包一个 case"证明的是机制，而不是知识：
只有一个 case 的知识包，可以悄无声息地丢掉它所声明的其余每一个故障模式，
而不会有任何东西变红。

| 知识包 | case 数 | 它们得出的答案 |
|---|---|---|
| Redis | 5 | 触及 maxmemory 的内存压力、MISCONF 背后的持久化失败、触及 maxclients 上限的连接饱和、日志源不可用时的内存压力，以及一个健康实例 |
| Kafka | 3 | broker 丢失伴随副本不足、生产速率平稳下的消费积压增长、刷盘变慢导致的生产时延 |
| MongoDB | 4 | 复制滞后伴随写关注阻塞、连接饱和、全集合扫描，以及指标源不可用时仅凭日志得出的写关注阻塞 |
| Pulsar | 3 | 消费者仍在连接时的订阅积压、出在 broker 的发布时延、bookie 数不足以满足写入 quorum |
| Milvus | 3 | 排队而非执行导致的 query node 时延、对象存储与 etcd 故障、节点在内存上限处被 OOM 杀掉 |
| OceanBase | 3 | 单个租户顶到自身内存上限、redo 日志复制延迟、吞吐持平而响应时间上升 |

`mas eval` 会按 id 打印全部 case；任何一个 case 的权威定义都在
`internal/eval/cases/` 下的 YAML 里。

每个 case 的构造方式都是：被排除的模式恰好是**症状**会引诱人去猜、
而**证据**予以否定的那些答案 —— broker 丢失时的"消费者在落后"、
连接池耗尽时的"磁盘在被填满"、bookie 一切正常时的"写入变慢"。
一次跟着抱怨走而不跟着测量走的诊断，会在这些 case 上失败。

其中有两类根本不是关于故障的：

- **`redis-healthy-baseline`** 描述的是一个什么问题都没有的实例，
  而症状里仍然写着"延迟毛刺" —— 因为当应用变慢、运维人员第一个怀疑 Redis 时，
  他们就是这么写的。它排除了该知识包声明的每一个模式，
  因此一个"为了有话可说而编出故障"的系统会在它上面失败。
  任何"有正确答案"的 case 都抓不住这一点：那些 case 里本来就有答案可找。
- **`redis-logs-unavailable`** 与 **`mongodb-metrics-unavailable`** 拿走了一个数据源，
  要求本次运行既要基于剩余证据得出应有的结论，也要声明它没能看到什么。

### 它真的能抓住问题吗？

针对内置知识包做了两次变异实验：

| 变异 | 结果 |
|---|---|
| Redis 驱逐规则从 `evicted.avg > 0` 放宽为 `>= 0` | 3 个 case 变为 `WRONG`（含健康那个）；退出码 1 |
| Kafka 副本不足阈值从 `> 0` 提高到 `> 100000` | 1 个 case 变为 `MISS`；退出码 1 |

放宽的规则与收紧的规则会在不同的列上失败 —— 这正是这些列必须分开的全部理由。

## 5a. 基线：不只是"绿不绿"，而是"什么变了"

单独执行 `mas eval` 只回答一个问题 —— 是不是还全绿？
而在一次改动之后，问题变了：**什么变了？**
一次修好两个 case、又弄坏一个 case 的知识包改动，呈现出来只是"语料库出现回归"，
没有任何办法看清这笔交易。而一旦某个 case 确实无法通过，
让 CI 保持绿色的唯一办法就是删掉它 —— 而这会同时删掉"该缺口存在"的唯一记录。

基线按**格子**记录结果：（case、拓扑、模型）。

```bash
mas eval --matrix --write-baseline internal/eval/baseline.json   # 记录
mas eval --matrix --baseline internal/eval/baseline.json         # 比较
```

记录是人的行为。除此之外没有任何东西会写基线，
因为会自我更新的基线记录的是"发生了什么"，因此永远不会失败。

### 一次比较会说些什么

| 原来 | 现在 | 名称 | 闸门 |
|---|---|---|---|
| hit | hit | *（不报告 —— 一次没有变化的通过不是新闻）* | 通过 |
| hit | 其他任何 | **回归** | **失败** |
| 非 hit | hit | **改善** | 通过，并展示 |
| 非 hit | 非 hit，id 相同 | **已知为坏** | 通过，且每次运行都列出 |
| 非 hit | 非 hit，id 不同 | **失败方式改变** | 通过，并展示 |
| 不存在 | 任何 | **新增** | 通过，并展示 |
| 任何 | 不存在 | **未运行** | 通过，并展示 |

回归与改善并排报告，且**绝不相互抵消**。
一次修好两个格子、弄坏一个格子的改动，就是两条改善加一条回归；由评审的人来做决定。
把它们相加，会让其中一个把另一个藏起来 ——
而这正是本装置在其他每一处都拒绝的那种压缩。

### "已知为坏"才是重点

最重要的是第四行。一个与记录时完全一样地失败的格子 ——
同一类别、同一批故障模式 id —— 不构成一次转移。
它既让 CI 保持绿色，**又**会出现在每一次运行的比较结果里，
从而让这个缺口一直摆在有能力去补它的人面前。
这就是"不必为了让构建变绿而删掉一个 case"的由来。

一个开始以**不同方式**失败的已知为坏格子会被报告，
因为"它为何失败"本身就是被记录下来的一部分：
一个原先漏掉某个模式、如今却得出了一个错误结论的格子确实动了，
尽管两者都属于"不是 hit"。

### 模型维度

```bash
mas eval --matrix --models claude-haiku-4-5,claude-sonnet-5
```

每个指定的模型都会跑遍每个 case 与每个拓扑，
且每个格子都携带产出它的那个模型 ——
因此成本与调用次数被归属到真正花掉它们的模型，
而不是归到"最后一次恰好被配置"的那个模型头上。

**每个格子只是一个样本。** 在确定性 provider 下，那是一次测量；
在真实模型下，它是一次抽样，而两次抽样可以不同。
比较报告的是"什么变了"，而不是"这个变化是否显著"：
一次运行支撑不了那个论断，因此我们也不做那个论断。
这句声明会像这里其他每一条限定声明一样，以字段形式随 JSON 一同传递。

允许把一次在某个 provider 下的运行，与在另一个 provider 下记录的基线相比较 ——
这正是模型矩阵的意义所在 —— 并且每次都会予以披露（`MAS-9107`），
因为真正错误的是**悄悄地**这么做。

### 仓库自身的基线

`internal/eval/baseline.json` 覆盖了内置语料库在确定性 provider 下、
全部五种拓扑的结果，`make ci` 会与之比较。
`make eval-baseline` 会重新记录它；提交前请先评审那份 diff ——
因为那份 diff 是"被记录下来的回归"与"一次绿色构建"之间唯一的屏障。

---

## 6. 刻意不做的事

- **不使用真实故障数据。** 语料库中的任何内容都不来自生产系统，
  因此其中任何内容都不可能泄露生产系统。
- **不针对语料库调提示词。** 一个为这些 case 拟合过的提示词，
  只会让数字变好看、含义变稀薄。语料库是回归闸门，不是训练集。
- **不对模型或厂商排名。** 本工具运行的是你所配置的任意 provider；
  它给出的比较，同样是对你的价格、你的提示词与你的知识包的比较，
  而不仅仅是对某个模型的比较。
- **不做显著性检验。** 本工具跑的就是每格一个样本。
  由此算出的置信区间，读起来像严谨，实则毫无严谨。
- **不自动更新基线。** 会自我更新的基线记录的是"发生了什么"，
  而一次永远不会失败的构建，什么也教不了你。
