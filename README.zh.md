# MAS-Turbo

**面向开源中间件的只读诊断型多 Agent 系统。**

[English](./README.md) · [用户手册](./docs/zh/user-manual.md) · [配置参考](./docs/zh/configuration.md) · [知识包编写](./docs/zh/knowledge-packs.md) · [评测指南](./docs/zh/evaluation.md) · [错误码](./docs/zh/error-codes.md)

---

当一个 Redis 集群开始驱逐 key、或者一个 Kafka 消费组开始堆积时，最初的三十分钟都花在
**拼装上下文**上：打开 Grafana、猜一条合适的 PromQL、tail Pod 日志、回忆这类故障该看 `INFO`
的哪个字段。真正需要专业判断的分析，要等这些做完才开始 —— 通常是在压力之下，而且通常不是由
最熟悉这套系统的人来做。

MAS-Turbo 替你完成拼装、套用中间件专属的专家知识、展示证据，并明确告诉你哪些它没能检查。

**它不对被检查的系统执行任何操作。** 不重启、不改配置、不执行 `FLUSHALL`。在每一项能力与
外部世界之间都横着一道安全守卫，任何不在只读白名单内的操作都会被拒绝，且不存在任何可以
关闭它的配置项。

## 一条命令试一试

```bash
git clone https://github.com/zlrrr/multi-agent-system-turbo
cd multi-agent-system-turbo
make demo
```

不需要凭据、不需要集群、不需要模型 API。本地桩服务提供一个自洽的 Redis 内存压力场景，
你会拿到三份报告：确定性短路路径、完整的多 Agent 调查，以及同一次调查的中文版。

## 报告长什么样

```markdown
## 结论摘要

Redis 已达到其配置的内存上限。驱逐先于延迟上升发生，且同一时间窗内日志显示写入被拒绝，
因此内存压力是原因而非结果。容器并未被 OOM-kill：被触及的是 Redis 自身的 maxmemory。

## 假设

### 1. Redis 达到 maxmemory；驱逐无法足够快地释放空间。
- **状态**：证据支持   - **置信度**：85%
- **推理过程**：三个独立来源相互印证，且驱逐先于延迟这一顺序排除了“延迟导致”的可能。
- **支持证据**：ev-1、ev-2

### 2. 某条慢命令阻塞了事件循环。
- **状态**：证据反驳     - **置信度**：5%
- **推理过程**：与本次运行采集到的 CPU 与 fork 证据相矛盾。

## 证据缺口
- **kube.nodes()** — 被安全守卫拒绝（`MAS-4201`）
  - 对本次分析的影响：无法排除节点级内存压力
```

## 工作原理

两个阶段，而顺序本身就是设计。

**确定性优先。** 知识包中的剧本在**回路中没有模型**的情况下运行：采集 → 求值 → 结论。
如果某条规则以足够的置信度给出定论，运行就此结束 —— 常规故障因此零成本、两秒内返回。

**只有在规则无法定论之处才动用 Agent。** 规划者判断还有什么没有定论；专项调查者 ——
每个证据域一个、并发运行 —— 采集定向证据；关联者对假设排序；批判者拿证据逐条挑战；
报告者撰写结论。批判者很关键：一个从未被挑战过的解释，只不过是最先想到的那个而已。

```
请求 ─▶ 准入 ─▶ ┌─ 确定性剧本 ────────┐─▶ 报告
                │   （零模型调用）     │
                └─ 未定论时才用 Agent ─┘
                      每次工具调用 ─▶ 安全守卫 ─▶ 只读白名单
```

## 凭什么值得信任

| 性质 | 如何保证 |
|---|---|
| 无法变更目标 | 所有副作用都经过单一收口点；默认拒绝；不存在关闭它的配置项。对抗性套件会尝试各种大小写的 FLUSHALL、参数注入、`pods/exec`、以及知识包提供的命令 —— 没有一条能抵达 |
| 缺失的测量绝不会被当成健康 | 输入采集失败的检查会被**跳过**并记录缺口，绝不会被求值为“通过” |
| 数据源挂掉不会丢掉分析 | 每个失败都变成带错误码、并说明其对置信度影响的缺口；运行照常完成 |
| 结果可复现 | 相同输入产出相同报告，即便在会并发运行角色的拓扑下也是如此 |
| 运行可审计 | 每次工具调用、模型交互与结论都带完整性摘要持久化；重放可在断网下复现报告 |
| 凭据绝不泄漏 | 脱敏发生在日志 handler 而非调用点；密钥在任何格式下都无法被打印 |

## 当前覆盖范围

| | 状态 |
|---|---|
| **中间件** | 已内置 Redis、Kafka、MongoDB、Pulsar、Milvus、OceanBase 知识包。其余中间件属于纯知识包工作：见[知识包编写指南](./docs/zh/knowledge-packs.md) —— 不改 Go 代码、不需重新编译 |
| **遥测** | Prometheus、VictoriaMetrics、Thanos、Mimir；Loki |
| **环境** | Kubernetes（只读 API，另含可关闭的容器内检查）；本地主机 |
| **源码** | 网络仓库并在不可达时自动回退到本地镜像，另含代码检索 |
| **模型** | Anthropic、任意 OpenAI 兼容端点，以及确定性 mock —— 可按 Agent 角色路由；在你提供价格后可按角色报告成本 |
| **拓扑** | `supervisor`（默认）、`single`（对照组）、`plan-execute`（自适应）、`debate`（对抗式）、`blackboard`（数据驱动） |
| **接口** | CLI、HTTP API、容器镜像 |

明确尚未提供：Kubernetes 容器内命令执行、API 认证、Web UI。
[用户手册](./docs/zh/user-manual.md#14-当前明确尚未提供的能力) 会如实说明，方便你据此规划。

## 快速上手

```bash
docker pull ghcr.io/zlrrr/multi-agent-system-turbo:latest

# 校验配置并探测每个端点
mas doctor

# 执行诊断
mas diagnose --target redis-prod --symptom "p99 延迟毛刺" --since 1h

# 在同一个 case 上比较拓扑
for t in single supervisor plan-execute debate blackboard; do
  mas diagnose -t redis-prod -s "延迟毛刺" --topology "$t" -f json -o "$t.json"
done

# 让根因已知的 case 语料库与已记录的基线比较
mas eval --matrix --baseline internal/eval/baseline.json
```

配置、RBAC、API 与知识包编写详见[用户手册](./docs/zh/user-manual.md)；
`mas eval` 度量什么、以及如何编写自己的 case，详见[评测指南](./docs/zh/evaluation.md)。

## 开发

```bash
make build          # 产出 bin/mas 与 bin/sddctl
make test           # 完整测试套件；任何测试都不需要网络
make ci             # CI 强制的全部内容：fmt、vet、lint、race 测试、SDD 校验、构建、语料库
make eval           # 让 case 语料库与其基线比较；出现回归时退出码非零
make eval-baseline  # 重新记录基线（提交前请先评审 diff）
make sdd-verify     # 双语对等、级联新鲜度、需求覆盖、所声明的测试
make docker         # 容器镜像
```

本项目采用规格优先的方式构建。整条链路 —— 目标 → 规格 → 计划 → 概要设计 → 详细设计 →
任务 → 代码 —— 位于 [`specs/001-mvp-core/`](./specs/001-mvp-core/)，其遵循的规则位于
[`.specify/memory/constitution.zh.md`](./.specify/memory/constitution.zh.md)。CI 会强制执行
这些规则：每份文档都必须中英文俱全，任何产出物都不得基于陈旧的上游派生，每条需求都必须
被某个任务认领，且任务声明的每一个测试都必须真实存在 ——
最后这项检查是在发现"六行任务被标记为完成、而其测试从未写过"之后加上的。

## 许可证

Apache 2.0，见 [LICENSE](./LICENSE)。
