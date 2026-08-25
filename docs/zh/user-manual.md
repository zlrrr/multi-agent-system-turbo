# MAS-Turbo 用户手册

> **双语对应文件**：[`../en/user-manual.md`](../en/user-manual.md)
> 适用于 MAS-Turbo 0.1.x · 另见：[配置参考](./configuration.md) · [错误码参考](./error-codes.md)

---

## 1. 这个工具做什么，以及它绝不会做什么

MAS-Turbo 诊断开源中间件的运行时问题 —— Redis、Kafka、MongoDB、Pulsar、Milvus、
OceanBase 等 —— 方法是关联四类证据：来自 Prometheus 兼容后端的指标、来自 Loki 的日志、
来自 Kubernetes 或主机的实时状态，以及中间件自身的源码。

它输出的是：排序后的假设、每条假设的正反证据、已通过的检查、未能获取的证据，以及建议的
后续动作。

**它不对被检查的系统执行任何操作。** 不重启、不改配置、不执行 `FLUSHALL`。这不是一个你可以
改掉的默认值：在每一项能力与外部世界之间都横着一道安全守卫，任何不在只读白名单内的操作都会
被拒绝，并且不存在任何可以关闭它的配置项。守卫的对抗性测试套件会尝试把变更类命令送过去 ——
各种大小写、参数注入、经由知识包内容 —— 并断言没有任何一条能够抵达。

这种克制本身就是价值所在。一个你敢在故障期间指向生产环境的工具，比一个能修但没人敢跑的
工具更有用。

## 2. 安装

### 容器镜像（推荐）

```bash
docker pull ghcr.io/zlrrr/multi-agent-system-turbo:latest
docker run --rm ghcr.io/zlrrr/multi-agent-system-turbo:latest version
```

镜像以非特权用户（uid 65532）运行，不需要任何 capability。

### 二进制

从发布页下载对应平台的压缩包，校验后安装：

```bash
sha256sum -c SHA256SUMS --ignore-missing
tar -xzf mas-linux-amd64.tar.gz
sudo install -m 0755 mas /usr/local/bin/mas
mas version
```

### 从源码构建

```bash
git clone https://github.com/zlrrr/multi-agent-system-turbo
cd multi-agent-system-turbo
make build          # 产出 bin/mas
make demo           # 无需任何凭据即可跑出一次完整诊断
```

## 3. 五分钟拿到第一份报告

`make demo` 会启动本地遥测桩并运行三次诊断，让你在配置真实环境之前先看到输出形态：

```
==> 1/3 deterministic only: a confident rule short-circuits the agent phase
==> 2/3 full multi-agent investigation (supervisor topology, mock provider)
==> 3/3 the same investigation, reported in Chinese
```

先读 `.demo/report.zh.md`，然后再配置你自己的环境。

## 4. 配置

创建 `mas.yaml`（完整带注释的参考见
[`deploy/config/mas.example.yaml`](../../deploy/config/mas.example.yaml)）：

```yaml
version: "1"

llm:
  provider: anthropic
  model: claude-opus-5
  api_key: "${env:ANTHROPIC_API_KEY}"

telemetry:
  metrics:
    - name: primary
      type: prometheus
      url: http://prometheus.monitoring.svc:9090
  logs:
    - name: primary
      type: loki
      url: http://loki.monitoring.svc:3100

envs:
  prod-k8s:
    type: kubernetes
    namespace: middleware       # kubeconfig 留空 ⇒ 使用集群内 ServiceAccount

targets:
  - id: redis-prod
    kind: redis
    env: prod-k8s
    selector: "app=redis,role=master"
    labels:
      job: redis                # 会变成 PromQL 选择器 {job="redis"}
```

配置是分层的：**默认值 → 配置文件 → `MAS_*` 环境变量 → 命令行参数**，后者依次覆盖前者。

密钥绝不写在文件里。使用 `${env:NAME}` 或 `${file:/path}`；它们在发起请求的那一刻才被解析，
并且无法被打印出来 —— `mas config` 打印的是所有密钥都已脱敏的有效配置。

配置完成后先做体检：

```bash
mas doctor
```

`doctor` 会校验配置并探测**每一个**已配置的端点，逐项报告而不是在第一个问题处停下 ——
因为你在搭建工具时想看到的是完整清单。

```
STATUS  CHECK             DETAIL
ok      configuration     valid; 2 target(s), 1 environment(s)
ok      knowledge packs   2 pack(s) covering [kafka redis]
ok      safety guard      read-only enforced; 14 allow-listed command(s), 19 allow-listed read path(s)
ok      metrics: primary  http://prometheus.monitoring.svc:9090 reachable
FAIL    logs: primary     MAS-4101  log source "primary" is unreachable: dial tcp: i/o timeout
ok      llm provider      anthropic configured with model claude-opus-5
ok      run store         fs store is writable and readable
```

## 5. 执行一次诊断

```bash
mas diagnose --target redis-prod --symptom "p99 延迟毛刺" --since 1h
```

用你自己的话描述症状。这段描述会被用来选择运行哪些排查剧本，所以“消费组堆积”与“写入报 OOM”
会导向完全不同的调查路径。中英文表述都能被识别。

| 参数 | 含义 |
|---|---|
| `--target`、`-t` | 配置中的目标 id（必填） |
| `--symptom`、`-s` | 你观察到的现象（必填） |
| `--since` | 回溯多久：`30m`、`1h`、`24h` |
| `--from` / `--to` | 用显式的 RFC3339 时间窗替代 `--since` |
| `--mode` | `offline`（仅遥测）或 `online`（同时读取实时环境） |
| `--topology` | 使用哪种 Agent 拓扑；见 `mas topologies` |
| `--format`、`-f` | `markdown`（默认）、`json` 或 `text` |
| `--output`、`-o` | 写入文件而非标准输出 |
| `--force-agents` | 即使确定性检查已给出定论，也仍然运行 Agent 阶段 |
| `--lang` | 报告语言：`en` 或 `zh` |

### 离线与在线

**离线**是默认模式，只读取你的遥测后端。它不需要任何集群凭据，适合事后分析故障，或分析
客户导出的数据。

**在线**会额外读取实时环境：Pod、事件、节点、工作负载、主机进程与端口。它需要只读凭据。
即便如此也不会有任何写入 —— “在线”扩大的只是**可读取**的范围，绝不是可执行的操作。

## 6. 一次诊断究竟是怎么跑的

流水线分两个阶段，而且顺序很重要。

**阶段 1 —— 确定性。** 先运行知识包中的剧本：有序的 采集 → 求值 → 结论 步骤，**回路中完全
没有模型**。对于一次 Redis 内存压力故障，这一阶段会以零成本、完全可复现的方式确定：已用内存
是否超过 maxmemory、是否正在发生 key 驱逐、命中率是否塌陷。

如果某条确定性结论的置信度超过配置阈值（`run.deterministic_short_circuit`，默认 0.85），
本次运行**就此结束**。你的常规故障因此零成本、两秒内返回。

**阶段 2 —— Agent 化。** 只有在规则无法定论之处，Agent 才会介入，而且它们是从确定性结论
出发，而不是从零开始。在默认的 `supervisor` 拓扑下：规划者判断还有什么没有定论；专项调查者
—— 每个证据域一个，并发运行 —— 采集定向证据；关联者把结果合并为排序假设；批判者拿证据逐条
挑战并驳倒站不住脚的假设；报告者撰写摘要与建议。

批判者不是装饰。一个从未被挑战过的解释，只不过是最先想到的那个而已 —— 而报告会告诉你哪些
假设被驳倒了，以及为什么。

### 当某样东西不可用时

数据源挂掉不会让运行失败。它会产生一条被记录的**缺口**，带错误码，并说明这对结论意味着什么：

```markdown
## 证据缺口

- **kube.nodes()** — 被安全守卫拒绝（`MAS-4201`）
  - 对本次分析的影响：无法排除节点级内存压力
```

输入缺失的检查会被**跳过**，而绝不会被当作“通过”来处理。缺失的测量不等于健康的测量，
报告绝不会让你把两者搞混。

## 7. 如何阅读报告

章节顺序就是 on-call 工程师需要它们的顺序：

1. **结论摘要** —— 结论优先。
2. **假设** —— 已排序，每条附状态（证据支持 / 证据反驳 / 无法定论）、置信度、推理过程，
   以及正反两方的证据 ID。
3. **发现** —— 确定性检查与 Agent 各自确立的事实，每条都可追溯到产生它的规则或角色。
4. **已通过的检查** —— 被排除掉的可能性。其价值往往不亚于被发现的问题。
5. **证据缺口** —— 什么没能检查，以及这带来了什么代价。
6. **建议的后续动作** —— 带风险标注，并明确声明只是建议。
7. **证据** —— 每一项采集内容及产生它的查询。
8. **运行统计** —— 模型调用、工具调用、token、耗时。

缺口刻意排在建议**之前**，这样你不会在没看到局限性的情况下就照着建议动手。

建议带有风险等级：**低**（只读巡检）、**中**（可回滚的变更）、**高**（可能丢数据或引发中断）。
每一条都是留给你去做的事 —— 报告里会明说，JSON 形式中的 `advisory` 字段恒为 `true`。

## 8. 选择拓扑

```bash
mas topologies
```

本项目内置五种架构。它们的差异**只在控制流**：每一种读的都是同一份确定性结论、
用的都是同一批受护栏约束的工具、写的都是同一份共享状态。
正因如此，"选哪一种"才是一个可以用证据来定的决策。

| 拓扑 | 形态 | 成本 | 何时选它 | 何时不选 |
|---|---|---|---|---|
| `supervisor` | 规划者 → 并发的分域调查者 → 关联者 → 批判者 → 报告者 | 中等且可预期；各域调用相互重叠 | 默认：证据分散在多个域，一遍广度扫描即可覆盖 | 证据获取代价高、且一项检查很可能就能定论时 |
| `single` | 一个持有全部工具的全能 Agent | 最便宜：仅一次对话 | 你需要对照组 —— 其余拓扑都必须胜过的那条基线 | 故障本身有歧义时；没有任何环节会挑战一个看似合理的初始答案 |
| `plan-execute` | strategist 提出目标 → executor 推进 → strategist 重新规划 → … | 首个目标即可定论时最便宜；否则最贵 | 证据获取代价高，或某一项检查很可能就能回答问题。它是唯一能在一步后收手的拓扑 | 你已知涉及多个域时：调用次数与 `supervisor` 相同，却是串行 |
| `debate` | 调查 → 关联 → 每个立场各有一名 advocate 论证 → judge 裁决 | 最贵：在 `supervisor` 之上，每个立场再加一次，最多三个 | 两种解释都吻合同一份证据，且选错代价很高时 | 证据已明确指向某一侧时；硬办辩论可能让弱立场获得不配得的分量 |
| `blackboard` | 当共享状态使贡献者具备条件时它才行动，按轮推进，直到某一轮什么都没改变 | 随现有证据而变；控制本身不消耗模型调用 | 证据到达不均匀，固定脚本会白白花调用去发现这一点时 | 你需要可预期的转录时：运行什么取决于状态 |

`mas topologies` 会用你配置的语言打印同一张表；
`mas topologies --json` 则同时携带两种语言，便于集成方自行渲染。

### 在你自己的故障上做对比

由于在同一个 case 的多次运行之间，拓扑是**唯一**变化的变量，
把一个 case 跑过多种拓扑就是一次真正意义上的比较，而不只是印象：

```bash
for t in single supervisor plan-execute debate blackboard; do
  mas diagnose -t redis-prod -s "延迟毛刺" --since 1h \
    --topology "$t" -f json -o "runs/$t.json"
done

# 各自花了多少、又得出了什么结论：
jq -r '[.topology, (.usage.llm_calls|tostring), (.usage.tool_calls|tostring),
        (.usage.wall_millis|tostring), .hypotheses[0].statement] | @tsv' runs/*.json
```

本项目只做拓扑之间的**对比**，不做**打分**。要宣布赢家，需要一个带已知根因的
case 语料库，那是另一项工作 —— 因此上面的成本数字是测量结果，而结论由你来判断。

## 9. 扩展知识

中间件专业知识存放在 YAML 知识包中，而不是代码里。新增一种中间件无需重新编译。

```bash
mas packs                  # 已加载了什么
mas packs --show redis     # 详细列出信号、日志模式与失效模式
```

一个知识包声明：

- **signals** —— 具名 PromQL 片段，由目标的选择器参数化；
- **logPatterns** —— 正则及其含义；
- **failureModes** —— 这种中间件会怎样出问题，以及每种问题对应的、经过审核的建议；
- **playbooks** —— 确定性检查；
- **inspect** —— 适配器可以执行的只读命令。

把你的知识包放进 `knowledge.pack_dirs` 列出的目录。与内置包 id 相同的包会替换内置包，
因此你可以修正我们的知识而无需 fork 二进制。

每个面向运维人员的字符串都必须**中英文俱全**；只翻译一半的包会被加载器拒绝。`inspect` 命令
在调用时会被安全守卫重新校验，因此无论知识包声称什么，它都无法引入变更类命令。

## 10. 以服务方式运行

```bash
mas serve --addr :8080
```

| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/api/v1/diagnoses` | 创建一次运行；`?wait=true` 阻塞并直接返回报告 |
| GET | `/api/v1/diagnoses` | 列出运行，最新在前 |
| GET | `/api/v1/diagnoses/{id}` | 运行状态与报告；`?steps=true` 附带审计轨迹 |
| GET | `/api/v1/targets` | 已配置的目标 |
| GET | `/api/v1/topologies` | 可用拓扑 |
| GET | `/api/v1/packs` | 已加载的知识包 |
| GET | `/healthz` `/readyz` | 存活与就绪 |
| GET | `/metrics` | MAS-Turbo 自身指标的 Prometheus 暴露 |

```bash
curl -s localhost:8080/api/v1/diagnoses?wait=true \
  -H 'Content-Type: application/json' \
  -d '{"target":"redis-prod","symptom":"p99 延迟毛刺","since":"1h","language":"zh"}' | jq .
```

每个失败响应都携带 `code`、按配置语言给出的 `message`，以及 `remedy`。

## 11. 审计与重放

每次运行都会被持久化：请求、每一次工具调用及其参数与脱敏后的结果、每一次模型交互，以及
最终报告。

```bash
mas runs                       # 列出已存储的运行
mas replay <run-id>            # 复现报告
mas replay <run-id> --steps    # 完整审计轨迹
```

重放**不接触任何东西** —— 不碰遥测、不碰集群、不碰模型。一次已存储的运行可以在断网的笔记本上
复现，这正是它之所以是“审计轨迹”而非“归档文件”的原因。记录带完整性摘要；被修改或被截断的
记录会以 `MAS-6003` 被拒绝，而不会被当作真实记录重放。

## 12. 出问题时怎么办

每个错误都携带稳定的错误码、描述与处理建议：

```
MAS-4001  指标数据源 "primary" 不可达：dial tcp 10.0.0.5:9090: i/o timeout
          检查 telemetry.metrics[].url 与网络策略；分析将在无指标的情况下继续。
```

```bash
mas errcodes                   # 完整注册表
mas errcodes --filter 8001     # 查单个错误码
mas errcodes --lang zh         # 中文
```

退出码按域区分，脚本因此可以做出恰当反应：

| 退出码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 未分类失败 |
| 2 | 配置或请求问题 |
| 3 | 被安全守卫拒绝 |
| 4 | 采集器或适配器失败 |
| 5 | 模型供应商或编排失败 |
| 6 | 运行存储失败 |

需要深入排查时，`--log-level debug` 会记录提示词与工具参数 —— 在脱敏之后。每行日志都携带
`run_id`，因此可以从共享日志流中把整次运行提取出来。

## 13. Kubernetes 部署

MAS-Turbo 只需要读权限。不要给更多：

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mas-turbo-reader
rules:
  - apiGroups: [""]
    resources: ["pods", "pods/log", "events", "nodes", "services", "endpoints"]
    verbs: ["get", "list"]
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "daemonsets", "replicasets"]
    verbs: ["get", "list"]
  - apiGroups: ["metrics.k8s.io"]
    resources: ["pods", "nodes"]
    verbs: ["get", "list"]
```

对任何资源都没有 `create`、`update`、`patch`、`delete`。如果你哪天看到 MAS-Turbo
请求其中任何一项，那是一个值得上报的缺陷。

### 容器内检查（可选）

要读取中间件自身的诊断信息 —— `redis-cli INFO all`、
`mongosh --eval "rs.status()"` —— 就意味着要在其容器内执行命令，
而在 Kubernetes 中这需要多一项权限：

```yaml
  # 可选。只有当你希望启用容器内检查时才授予。
  - apiGroups: [""]
    resources: ["pods/exec"]
    verbs: ["create"]
```

这是一次**真实的放宽**，此处直说而不藏着掖着：`pods/exec` 让持有者能在该角色覆盖的
**任意** Pod 中执行**任意**命令。真正约束 MAS-Turbo 的不是这条 RBAC 规则，而是它自身的护栏；
而这个约束由四件互相独立的事构成，其中没有任何一件能被配置或提示词放宽：

| 边界 | 由什么设定 |
|---|---|
| 哪些二进制 | 只读命令白名单 —— `redis-cli`、`mongosh`、`kafka-*.sh` 等，别无其他 |
| 哪些参数 | 变更性动词识别与按 flag 的取值白名单：`INFO` 能跑，`FLUSHALL` 不能 |
| 哪些 Pod | 只有你所指定目标解析出的那些 Pod |
| 哪个端点 | 只有 exec 子资源；代码在结构上无法访问别处 |

**exec 改变的是"受审核命令能在哪里执行"，绝不改变"哪些命令通过了审核"。**
`kubectl` 不在白名单里，将来也不会加入：一个二进制名会把整个 Kubernetes API 塞进白名单，
而这正是"默认拒绝"要防的事。

如果你的策略无论命令是什么都禁止 exec，那就关掉它，并且什么权限都不必授予：

```yaml
envs:
  prod:
    type: kubernetes
    exec: false
```

此时该工具根本不会被注册，因此无论提示词怎么写都无法被调用。
`mas doctor` 会把它报告为一项策略决定（`MAS-4210`），而不是一项缺失的能力 ——
这样就不会有人为了别人有意设置的一个开关，花一下午去排查 RBAC。

## 14. 当前明确尚未提供的能力

如实说明范围，方便你据此规划：

- **API 认证** —— 暂时不要把 API 暴露到可信网络之外。
- **Web UI** —— 当前只有 CLI 与 API。

## 15. 获取帮助

- 任意命令均可 `mas <command> --help`
- [配置参考](./configuration.md)
- [错误码参考](./error-codes.md)
- 反馈问题时，请附上 `mas doctor` 的输出；在可以共享的前提下，再附上
  `mas replay <run-id> --steps` 的运行记录 —— 它包含理解问题所需的一切，且凭据已被脱敏。
