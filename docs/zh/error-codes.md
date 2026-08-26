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
| `MAS-4210` | warn | `KubeExecDisabled` | 环境 %q 已关闭容器内执行 | 将 `envs.<name>.exec` 设为 true（或直接删除该键），即可交由护栏按命令判定。该开关只能收紧，绝不会放宽护栏允许的范围。 |
| `MAS-4211` | error | `KubeExecPodNotInTarget` | Pod %q 不是目标 %q 的实例 | exec 被绑定在目标解析出的实例集合上，因此一次运行永远无法触及集合之外的 Pod。请检查该目标的选择器。 |
| `MAS-4212` | warn | `KubeExecUpgradeFailed` | 无法与 %s 建立远程命令流：%s | 通常是 RBAC 问题：该凭据需要对 `pods/exec` 具备 `create` 权限。也可能是 Pod 已消失，或准入策略禁止 exec。 |
| `MAS-4213` | error | `KubeExecStreamMalformed` | 来自 %s 的远程命令流格式错误：%s | apiserver 或中间设备并未遵循 v4.channel.k8s.io 协议。请检查是否有代理改写了 WebSocket 流量。 |
| `MAS-4214` | warn | `KubeExecNoExitStatus` | %s 中的命令结束时未报告退出状态 | 其输出会被照常报告，但状态记为未知。它不会被当作成功：一条结果从未到达的命令，并不能被认为已经成功。 |
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
| `MAS-5016` | error | `RuleRangesOverlap` | %s id %q 的两次声明版本区间重叠：%q 与 %q（%s、%s） | 规则 id 只能以版本变体的形式重复，且任意两个变体不得同时适用于某个版本。请收窄其中一个区间，或给这两条规则不同的 id。 |
| `MAS-5017` | warn | `NoVariantApplies` | %s %q 存在版本变体，但没有一个适用于版本 %s | 该知识包覆盖了这个版本，却无法为这条规则在其中定位。请放宽某个变体的区间，或为这个版本补一个变体。 |
| `MAS-5018` | warn | `VersionUnknownForVariants` | %s %q 存在与版本相关的变体，而目标的版本未知 | 请设置 `targets[].version`，以便选中正确的变体。在没有版本的情况下随便挑一个，会去查询一个可能并不存在的指标名，并把它的“查不到”当成数据来读。 |
| `MAS-5019` | info | `RulesNotApplicable` | 有 %d 条规则不适用于版本 %s：%s | 没有任何损失：这些检查对该版本本就不存在。列出它们，是为了让读者能区分“版本限定”与“没能跑起来的检查”。 |

## 运行存储

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-6001` | error | `RunNotFound` | 运行记录 %q 不存在 | 执行 `mas runs` 查看已存储的运行记录。 |
| `MAS-6002` | error | `RunWriteFailed` | 无法持久化运行记录 %q：%s | 确认 store.dir 存在且可写。 |
| `MAS-6003` | error | `RunCorrupt` | 运行记录 %q 已损坏：%s | 该记录未通过完整性校验，无法重放。 |
| `MAS-6004` | error | `RunStoreUnavailable` | 运行存储不可用：%s | 检查 store.type 与 store.dir。 |
| `MAS-6010` | error | `ObjectStoreConfigInvalid` | 对象存储配置在 %s 处非法：%s | 请修正 `store.s3` 下指出的配置路径。endpoint、region 与 bucket 为必填；两个凭据必须同时设置或同时留空 —— 只配一半，意味着你以为自己配好了访问权限，而其实没有。 |
| `MAS-6011` | error | `ObjectStoreRejected` | 对象存储返回 HTTP %d（%s）：%s | 消息中的 S3 错误码说明了该怎么做：AccessDenied 是凭据或策略问题，NoSuchBucket 是桶或 region 弄错了，SignatureDoesNotMatch 通常是时钟偏差超过了几分钟。 |
| `MAS-6012` | error | `ObjectStoreUnreachable` | 对象存储 %s 不可达：%s | 检查 endpoint 与网络策略。运行仍会完成；它们会被报告为“未持久化”，而不是被静默丢失。 |
| `MAS-6013` | error | `ObjectRecordMalformed` | 存储中的记录 %s 格式错误：%s | 该对象存在，但不是一条运行记录。要么有本工具之外的东西写入了这个前缀，要么该对象被截断了。 |
| `MAS-6014` | error | `RunTooManySteps` | 运行 %s 产生的步骤数超出了键布局可排序的范围（%d） | 这远超步数预算，本不应发生。写入被拒绝而不是回绕 —— 因为一份被静默重排的审计轨迹，比一份缺失的更糟。 |

## API 与 CLI

| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |
|---|---|---|---|---|
| `MAS-7001` | error | `BadRequest` | 请求非法：%s | 请求结构见 docs/zh/user-manual.md。 |
| `MAS-7002` | error | `MethodNotAllowed` | %s 方法不允许用于 %s | 请对该端点使用文档规定的方法。 |
| `MAS-7003` | error | `ServerInternal` | 服务器内部错误：%s | 根据关联的 run_id 检查服务端日志。 |
| `MAS-7005` | error | `ServerStartFailed` | 服务器在 %s 上启动失败：%s | 确认该地址未被占用且进程有权绑定。 |
| `MAS-7010` | error | `APIUnauthenticated` | API 将在 %s 上监听，但未配置任何认证 | 任何能访问到该地址的东西，都可以列出你的目标、读取已存储的诊断，并发起新的诊断。请配置 `server.auth.tokens`，或改为监听 127.0.0.1 并在其前面放置你自己的代理。 |
| `MAS-7011` | error | `APIPlaintextCredentials` | API 将在 %s 上以明文传输凭据 | 未加密连接上的 Bearer token，就是一份放在线路上的凭据。请设置 `server.tls.cert_file` 与 `key_file`；若本进程之前已有组件终止 TLS，则设置 `server.tls.terminated_by_proxy: true`。 |
| `MAS-7012` | error | `APICredentialMissing` | 请求未携带可用的凭据 | 请在请求中携带 `Authorization: Bearer <token>`，其中 token 需在 `server.auth.tokens` 下配置。 |
| `MAS-7013` | error | `APITokenScopeInvalid` | API token %q 不可用：%s | 每个 token 至少要声明一个 scope，且每个 scope 都必须是本版本认识的。一个被忽略的 scope，是一次你以为自己已经授出的授权。 |
| `MAS-7014` | error | `APIScopeMissing` | 该凭据缺少此路由所需的 %q scope | 请在 `server.auth.tokens[].scopes` 下给该 token 加上这个 scope，或改用已具备该 scope 的 token 调用。 |
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
| `MAS-9100` | error | `CaseMalformed` | 诊断 case %s 格式错误：%s | case 需要声明遥测数据与一次正确诊断应得出的结果，且两种语言都要有。参见 docs/zh/evaluation.md。 |
| `MAS-9101` | error | `CaseModeUndeclared` | 诊断 case %s 期望故障模式 %q，而知识包 %s 并未声明它 | case 只能断言该知识包有能力得出的结论；否则它会永远失败且毫无信息量。请用 `mas packs --show` 核对模式 id。 |
| `MAS-9102` | error | `CaseNoPack` | 诊断 case 指向的中间件 %q 没有对应的知识包 | 为该中间件提供知识包，或让该 case 指向一个已存在的中间件。 |
| `MAS-9103` | error | `CorpusRegressed` | 语料库出现回归：%d 个 case 漏判，%d 个得出了该 case 明确排除的结论 | 执行 `mas eval` 查看具体是哪些。漏判与错误结论是两种不同的失败：前者让运维人员留在原地，后者自信地把他们送错方向。 |
| `MAS-9104` | error | `CaseDirUnreadable` | case 目录 %s 无法读取：%s | 若不报错，写错的 --cases 路径会去跑内置语料库并报告成功 —— 这正是绝不能被意外产生的结果。 |
| `MAS-9105` | error | `BaselineRegressed` | 有 %d 个格子相对基线发生回归：%s | 某个原本命中的格子不再命中。执行 `mas eval --baseline <文件>` 查看每个格子失去了什么。若该变化是有意为之，请用 --write-baseline 记录，使新状态在 diff 中被评审。 |
| `MAS-9106` | error | `BaselineUnreadable` | 基线 %s 无法读取：%s | 请让 --baseline 指向由 --write-baseline 写出的文件。把无法读取的文件当作空基线，会让每个格子都看起来是新增的 —— 那读起来就像一次干净的比较。 |
| `MAS-9107` | warn | `BaselineProviderMismatch` | 基线是在 provider %q 下记录的，而本次运行使用的是 %q | 跨 provider 比较正是模型矩阵的意义所在；但不加说明地这么做则不是。比较会继续进行，并同时披露这一点。 |
