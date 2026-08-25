# 错误码参考

> 本文件由 `mas errcodes --format markdown --lang zh` 生成，请勿手工编辑。
> 双语对应文件：[`../en/error-codes.md`](../en/error-codes.md)

每个跨越边界的错误都携带一个稳定的 `MAS-NNNN` 错误码。错误码按域分段（宪章第五条）。

## 配置与请求

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-1001` | error | `ConfigInvalid` | 配置文件 %q 无法解析 | 检查 YAML 语法；执行 `mas doctor --config <path>` 可定位到具体位置。 |
| `MAS-1002` | error | `ConfigUnknownField` | 未知的配置字段 %q | 删除该字段，或对照 docs/zh/configuration.md 核对正确名称。 |
| `MAS-1003` | error | `ConfigValidation` | 配置在 %s 处非法：%s | 修正指出的配置路径；全部约束见 docs/zh/configuration.md。 |
| `MAS-1004` | error | `ConfigNotFound` | 在 %v 中未找到配置文件 | 使用 --config 指定、设置 MAS_CONFIG，或在工作目录放置 mas.yaml。 |
| `MAS-1005` | error | `TargetUnknown` | 未知目标 %q | 执行 `mas targets` 查看已配置的目标。 |
| `MAS-1006` | error | `SecretUnresolvable` | 密钥引用 %q 无法解析 | 确认被引用的环境变量或文件存在且可读。 |
| `MAS-1007` | error | `RequestInvalid` | 诊断请求非法：%s | 修正请求；`mas diagnose --help` 说明了每个字段。 |
| `MAS-1008` | error | `EnvUnknown` | 目标 %q 引用了未知环境 %q | 在配置文件的 `envs:` 下声明该环境。 |
| `MAS-1010` | error | `WindowInvalid` | 时间窗口非法：%s | 使用 --since（如 1h），或显式给出满足 from < to 的 --from/--to。 |
| `MAS-1011` | error | `ModeInvalid` | 运行模式 %q 非法 | 使用 `offline`（仅遥测）或 `online`（同时读取实时环境）。 |
| `MAS-1012` | error | `TelemetrySourceUnknown` | 未知遥测数据源 %q | 在 `telemetry.metrics` 或 `telemetry.logs` 下声明该数据源。 |

## LLM 供应商

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-2001` | error | `ProviderUnavailable` | LLM 供应商 %q 不可用 | 检查网络连通性与 base_url；本次运行将仅输出确定性结论。 |
| `MAS-2002` | error | `ProviderAuthFailed` | LLM 供应商 %q 拒绝了凭据 | 确认 llm.api_key 有效，且该密钥有权访问所请求的模型。 |
| `MAS-2003` | error | `ProviderRateLimited` | LLM 供应商 %q 对请求进行了限流 | 稍后重试、降低 run.budget，或改用配额更高的模型。 |
| `MAS-2004` | error | `ToolCallUnparseable` | 模型为 %q 产出了无法使用的工具调用：%s | Agent 会以修复提示重试；持续失败通常说明模型能力不足。 |
| `MAS-2005` | error | `ProviderUnknown` | 未知 LLM 供应商 %q | 请使用：mock、anthropic、openai 之一。 |
| `MAS-2006` | warn | `ModelRefused` | 模型拒绝回答：%s | 重新描述症状，或使用 --log-level debug 检查提示词。 |
| `MAS-2007` | warn | `TokenBudgetExceeded` | token 预算在 %d tokens 后耗尽 | 提高 run.budget.max_tokens，或接受被截断的分析结果。 |
| `MAS-2008` | error | `ProviderResponseMalformed` | LLM 供应商 %q 返回了格式错误的响应：%s | 确认 base_url 指向兼容的 API；开启 debug 日志可查看响应体。 |
| `MAS-2009` | error | `MockScriptExhausted` | mock provider 脚本在第 %d 轮没有可用回复 | 扩展 mock 脚本；该情况仅出现在测试与演示中。 |
| `MAS-2010` | warn | `CitationUnresolved` | 模型引用了本次运行并未采集到的 %d 条引用：%s | 这些无法解析的引用已从报告中剔除。模型引用了未曾提供给它的证据，说明它在猜；请在转录中查看是哪个角色产出的。 |

## Agent 与编排

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-3001` | error | `TopologyUnknown` | 未知拓扑 %q | 执行 `mas topologies` 查看已注册的拓扑。 |
| `MAS-3002` | error | `OrchestratorFailed` | 拓扑 %q 执行失败：%s | 使用 `mas replay <run-id> --steps` 检查运行记录。 |
| `MAS-3003` | error | `AgentFailed` | Agent %q 执行失败：%s | 在运行记录中检查失败步骤；本次运行会输出部分结论。 |
| `MAS-3005` | warn | `StepBudgetExceeded` | 步数预算 %d 已耗尽 | 若分析被过早截断，可提高 run.budget.max_steps。 |
| `MAS-3006` | warn | `WallBudgetExceeded` | 墙钟预算 %s 已耗尽 | 提高 run.budget.max_wall，或收窄时间窗口。 |
| `MAS-3007` | warn | `ToolCallBudgetExceeded` | 工具调用预算 %d 已耗尽 | 若证据采集被过早截断，可提高 run.budget.max_tool_calls。 |
| `MAS-3010` | warn | `NoProgress` | Agent %q 连续 %d 步没有进展 | 本次运行以已收集内容作结；可考虑更换更强的模型。 |

## 采集器、适配器与源码

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-4001` | error | `MetricsUnreachable` | 指标数据源 %q 不可达：%s | 检查 telemetry.metrics[].url 与网络策略；分析将在无指标的情况下继续。 |
| `MAS-4002` | error | `MetricsStatus` | 指标数据源 %q 返回 HTTP %d | 核对该指标数据源的端点路径与凭据。 |
| `MAS-4003` | error | `MetricsMalformed` | 指标数据源 %q 返回了格式错误的响应：%s | 确认该 URL 指向 Prometheus 兼容的 HTTP API v1。 |
| `MAS-4004` | error | `MetricsQueryRejected` | 指标查询被拒绝：%s | 服务端拒绝了该 PromQL；请检查知识包中的信号定义。 |
| `MAS-4005` | warn | `MetricsTruncated` | 指标结果在 %d 个样本处被截断 | 收窄时间窗口，或提高 telemetry.metrics[].max_samples。 |
| `MAS-4101` | error | `LogsUnreachable` | 日志数据源 %q 不可达：%s | 检查 telemetry.logs[].url；分析将在无日志的情况下继续。 |
| `MAS-4102` | error | `LogsStatus` | 日志数据源 %q 返回 HTTP %d | 核对该日志数据源的端点路径与凭据。 |
| `MAS-4103` | error | `LogsMalformed` | 日志数据源 %q 返回了格式错误的响应：%s | 确认该 URL 指向 Loki 兼容的 HTTP API v1。 |
| `MAS-4104` | error | `LogsQueryRejected` | 日志查询被拒绝：%s | 服务端拒绝了该 LogQL；请检查知识包中的日志模式定义。 |
| `MAS-4105` | warn | `LogsTruncated` | 日志结果在 %d 行处被截断 | 收窄时间窗口，或提高 telemetry.logs[].max_lines。 |
| `MAS-4201` | warn | `KubeForbidden` | Kubernetes 拒绝了对 %s 的读取：%s | 为该 ServiceAccount 授予对应资源的 `get`/`list` 权限；本次运行将带缺口继续。 |
| `MAS-4202` | warn | `KubeNoCredentials` | 环境 %q 没有可用的 Kubernetes 凭据 | 设置 envs.<name>.kubeconfig，或以带 ServiceAccount 的集群内方式运行。 |
| `MAS-4203` | error | `KubeUnreachable` | Kubernetes API Server 不可达：%s | 检查 API Server 地址、CA 证书与网络策略。 |
| `MAS-4204` | warn | `KubeNotFound` | Kubernetes 对象不存在：%s | 检查目标定义中的命名空间与选择器。 |
| `MAS-4205` | error | `KubeMalformed` | Kubernetes API 返回了格式错误的响应：%s | 确认该 URL 指向 Kubernetes API Server。 |
| `MAS-4301` | warn | `HostCommandFailed` | 主机命令 %q 执行失败：%s | 确认该命令存在，且当前进程有权执行它。 |
| `MAS-4302` | warn | `HostBinaryNotFound` | PATH 中未找到可执行文件 %q | 在镜像或主机上安装该工具，或跳过依赖它的检查。 |
| `MAS-4303` | warn | `HostUnsupported` | 主机巡检 %q 在 %s 上不受支持 | 该检查将被跳过；它仅在 Linux 主机上可用。 |
| `MAS-4401` | warn | `SourceFellBackToMirror` | %q 的源码网络不可达；改用本地镜像 %s | 离网环境下属预期行为；请确认镜像版本与部署版本一致。 |
| `MAS-4402` | warn | `SourceUnavailable` | %q 无可用源码：%s | 配置 source.repos 或 source.mirrors；基于源码的分析将被跳过。 |
| `MAS-4403` | warn | `SourceRefNotFound` | %q 的源码 ref %q 不存在 | 检查目标版本，或改为拉取默认分支。 |
| `MAS-4404` | error | `SourceSearchInvalid` | 源码检索模式非法：%s | 请使用合法的 RE2 正则表达式。 |
| `MAS-4405` | warn | `SourceToolMissing` | git 不可用；源码获取功能被禁用 | 在镜像或主机上安装 git 以启用基于源码的分析。 |

## 知识包与规则

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-5001` | error | `PackSchemaViolation` | 知识包 %s 在 %s 处非法：%s | 对照 docs/zh/knowledge-packs.md 修正该包后重新加载。 |
| `MAS-5002` | error | `PackDuplicate` | 知识包 id %q 重复 | knowledge.pack_dirs 下有两个知识包声明了相同的 id，谁生效将取决于目录顺序。请重命名其中之一，或删除不再使用的那份。覆盖内置知识包是被支持的，不属于此错误。 |
| `MAS-5003` | warn | `PackNotFound` | 中间件 %q 没有对应的知识包 | 在 knowledge.pack_dirs 下新增知识包；否则仅执行通用检查。 |
| `MAS-5004` | error | `PackVersionRangeInvalid` | 知识包 %s 的版本区间 %q 非法 | 请使用形如 ">=5.0"、"<7"、">=5.0 <8.0" 的区间。 |
| `MAS-5005` | error | `PackReadFailed` | 知识包 %s 无法读取：%s | 检查知识包目录的文件权限。 |
| `MAS-5010` | error | `ExpressionCompile` | 剧本 %s 的步骤 %s 表达式非法：%s | 修正该表达式；作用域内只有步骤结果与辅助函数。 |
| `MAS-5011` | error | `ExpressionType` | 剧本 %s 的步骤 %s 表达式未求值为布尔：%s | `evaluate` 表达式必须求值为 true 或 false。 |
| `MAS-5012` | error | `SignalUnknown` | 剧本 %s 引用了未知信号 %q | 在同一知识包的 `signals:` 下声明该信号。 |
| `MAS-5013` | warn | `PlaybookBudgetExceeded` | 剧本 %s 超出其步数预算 %d | 拆分该剧本，或提高 run.budget.max_steps。 |
| `MAS-5014` | error | `PlaybookStepInvalid` | 剧本 %s 的步骤 %s 非法：%s | 每个步骤必须且只能声明 collect、evaluate、conclude 之一。 |
| `MAS-5015` | warn | `CheckNotPerformed` | 剧本 %s 的步骤 %s 已跳过：%s | 该检查没有可读取的数据，因此其故障模式既未被确认也未被排除。请确认该信号在当前部署中存在。 |

## 运行存储

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-6001` | error | `RunNotFound` | 运行记录 %q 不存在 | 执行 `mas runs` 查看已存储的运行记录。 |
| `MAS-6002` | error | `RunWriteFailed` | 无法持久化运行记录 %q：%s | 确认 store.dir 存在且可写。 |
| `MAS-6003` | error | `RunCorrupt` | 运行记录 %q 已损坏：%s | 该记录未通过完整性校验，无法重放。 |
| `MAS-6004` | error | `RunStoreUnavailable` | 运行存储不可用：%s | 检查 store.type 与 store.dir。 |

## API 与 CLI

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-7001` | error | `BadRequest` | 请求非法：%s | 请求结构见 docs/zh/user-manual.md。 |
| `MAS-7002` | error | `MethodNotAllowed` | %s 方法不允许用于 %s | 请对该端点使用文档规定的方法。 |
| `MAS-7003` | error | `ServerInternal` | 服务器内部错误：%s | 根据关联的 run_id 检查服务端日志。 |
| `MAS-7005` | error | `ServerStartFailed` | 服务器在 %s 上启动失败：%s | 确认该地址未被占用且进程有权绑定。 |
| `MAS-7404` | error | `NotFound` | 未找到：%s | 检查请求路径中的标识符。 |

## 安全守卫

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-8001` | error | `MutatingRefused` | 已拒绝：%s 会变更目标环境 | MAS-Turbo 依据宪章只读；如确需执行，请由人工操作。 |
| `MAS-8002` | error | `CommandNotAllowed` | 已拒绝：%s 不在只读白名单内 | 通过知识包与规格变更来新增该能力，而不是在运行期放行。 |
| `MAS-8003` | error | `PathNotAllowed` | 已拒绝：%s %s 不是白名单内的只读路径 | 只能调用文档中列明的只读端点。 |
| `MAS-8005` | error | `ArgumentRejected` | 已拒绝：参数 %q 被拒（%s） | 参数中不得包含 Shell 元字符、路径穿越或变更类动词。 |
| `MAS-8006` | error | `ToolNotRegistered` | 已拒绝：工具 %q 未注册 | 执行 `mas tools` 查看可用能力。 |
| `MAS-8010` | error | `CeilingExceeded` | 已拒绝：%s 超出配置的上限（%s） | 仅在确知资源代价的前提下，才在 `safety:` 中提高该上限。 |

## 内部错误

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-9001` | error | `InvariantViolated` | 内部不变量被破坏：%s | 这是缺陷；请附带运行记录反馈给我们。 |
| `MAS-9002` | error | `Unexpected` | 未预期的内部错误：%s | 这是缺陷；请附带运行记录反馈给我们。 |
