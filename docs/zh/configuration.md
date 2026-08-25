# 配置参考

> **双语对应文件**：[`../en/configuration.md`](../en/configuration.md)
> 完整带注释的示例：[`deploy/config/mas.example.yaml`](../../deploy/config/mas.example.yaml)

---

## 优先级

四个层次，后者依次覆盖前者：

```
内置默认值  →  配置文件  →  MAS_* 环境变量  →  命令行参数
```

`mas config` 会打印所有密钥都已脱敏的最终结果。当某个配置不符合预期时，从这里开始查。

### 配置文件的查找顺序

依次为：`--config <path>`、`$MAS_CONFIG`、`./mas.yaml`、`./mas.yml`、`/etc/mas/mas.yaml`。
文件不存在本身不是错误 —— 零配置运行会使用默认值 —— 但**你显式指定**却不存在的路径是错误
（`MAS-1004`）。

未知字段会被拒绝（`MAS-1002`）而不是被忽略，因为在与安全相关的配置项上，一个被静默忽略的
拼写错误比启动失败更糟糕。

## 密钥

绝不要把凭据写进配置文件。有两种间接引用方式：

| 形式 | 解析来源 |
|---|---|
| `${env:NAME}` | 使用时从环境变量读取 |
| `${file:/path}` | 使用时从文件读取，去掉行尾换行 |

`Secret` 无法被打印：无论经由 `fmt`、JSON 还是 YAML，它都渲染为 `***`，因此不会通过日志行、
API 响应或缺陷报告泄漏。无法解析的引用返回 `MAS-1006`。

## `log`

| 键 | 默认值 | 含义 |
|---|---|---|
| `level` | `info` | `debug`、`info`、`warn`、`error`。`debug` 会记录提示词与工具参数 —— 在脱敏之后 |
| `format` | `json` | `json` 或 `text` |
| `redact` | — | 额外的正则，会从日志、报告、运行记录与模型提示词中脱敏 |

脱敏发生在日志 handler 层而非各个调用点，因此新增的日志语句不会因为“忘了脱敏”而泄漏。
常见凭据形态 —— bearer token、`api_key=` 赋值、`user:pass@host` 形式的 URL、JWT、PEM 头 ——
默认已覆盖；`redact` 用于你特有的敏感值。

## `llm`

| 键 | 默认值 | 含义 |
|---|---|---|
| `provider` | `mock` | `mock`、`anthropic` 或 `openai` |
| `model` | `mock-1` | 该供应商下的模型名 |
| `api_key` | — | 请使用 `${env:...}` |
| `base_url` | — | 用于 OpenAI 兼容服务端：DeepSeek、Qwen、vLLM、Ollama |
| `timeout` | `60s` | 单次请求超时 |
| `max_tokens` | `4096` | 单次补全上限 |
| `temperature` | `0` | 采样温度 |
| `mock_script` | — | 脚本化对话文件路径（仅 mock provider） |
| `per_agent` | — | 按角色覆盖 `model` 与 `temperature` |

`per_agent` 的存在是因为不同角色需求不同。调查者主要做抽取与归纳；真正需要判断力的是关联与
批判：

```yaml
llm:
  provider: anthropic
  model: claude-opus-5              # 关联者、批判者、报告者
  per_agent:
    investigator:
      model: claude-haiku-4-5-20251001
```

`mock` provider 重放脚本化对话。正是它让测试套件具备确定性、让演示无需凭据；`mas doctor`
在检测到它时会给出警告，因为那不是一次真实分析。

## `telemetry`

### `telemetry.metrics[]`

| 键 | 默认值 | 含义 |
|---|---|---|
| `name` | `metrics-N` | 由 `targets[].metrics_source` 引用 |
| `type` | `prometheus` | `prometheus`、`victoriametrics`、`thanos`、`mimir` —— 同一套 wire API |
| `url` | 必填 | 基础 URL，不含结尾的 `/api/v1` |
| `auth.type` | `none` | `none`、`bearer`、`basic`、`header` |
| `auth.token` | — | 用于 `bearer` 与 `header` |
| `auth.username` / `auth.password` | — | 用于 `basic` |
| `auth.header` | — | 头名称，用于 `header` |
| `timeout` | `15s` | 单次查询超时 |
| `max_samples` | `11000` | 截断上限；区间查询的 step 会被放宽以遵守它 |
| `headers` | — | 额外请求头 |

### `telemetry.logs[]`

| 键 | 默认值 | 含义 |
|---|---|---|
| `name` | `logs-N` | 由 `targets[].logs_source` 引用 |
| `type` | `loki` | 目前仅支持 `loki` |
| `url` | 必填 | 基础 URL |
| `auth` | — | 同指标源 |
| `tenant_id` | — | 为多租户 Loki 设置 `X-Scope-OrgID` |
| `timeout` | `20s` | 单次查询超时 |
| `max_lines` | `1000` | 结果上限，既随请求发送也在本地强制 |

## `envs`

| 键 | 含义 |
|---|---|
| `type` | `kubernetes` 或 `local` |
| `kubeconfig` | 路径；留空表示使用集群内 ServiceAccount |
| `context` | 指定 kubeconfig context；留空表示 `current-context` |
| `namespace` | 默认命名空间 |
| `api_server` | 直接指定 API Server 地址，替代 kubeconfig |
| `token` | 与 `api_server` 搭配的 bearer token |
| `ca_file` | CA 证书路径 |
| `tls_insecure_skip_verify` | 关闭证书校验。生产环境请勿使用 |
| `timeout` | 单次请求超时 |
| `exec` | 设为 `false` 可关闭该环境的容器内检查。只能收紧：缺省或 `true` 表示由护栏的只读白名单逐条判定命令，与在主机上完全一致 |

支持的凭据来源：集群内 ServiceAccount、kubeconfig 中的 bearer token、`tokenFile`、
客户端证书、basic auth。

**不支持 `exec` 凭据插件。** 支持它意味着执行由配置文件指定的任意二进制，而这恰恰是默认拒绝的
命令白名单要防止的事情。MAS-Turbo 会以 `MAS-4202` 拒绝这类 kubeconfig，并提示你改为提供只读
ServiceAccount token。

## `targets[]`

| 键 | 含义 |
|---|---|
| `id` | 唯一标识；`--target` 引用的就是它 |
| `kind` | 中间件种类，用于匹配知识包：`redis`、`kafka` … |
| `version` | 固定知识包选择；留空则从运行中的镜像 tag 自动识别 |
| `env` | `envs` 中的环境名 |
| `namespace` | 覆盖环境的命名空间 |
| `selector` | Kubernetes 标签选择器 |
| `labels` | 会变成 PromQL 选择器，如 `{job="redis"}` |
| `metrics_source` / `logs_source` | 指定数据源名；留空表示第一个已配置的源 |
| `hosts` / `port` | 用于 `local` 环境 |

`labels` 与 `selector` 承担的是不同的工作：`labels` 在指标后端里选择**时间序列**，
`selector` 在 Kubernetes 里选择 **Pod**。两者通常并不相同。

## `knowledge`

| 键 | 默认值 | 含义 |
|---|---|---|
| `pack_dirs` | — | 搜索知识包的目录 |

与内置包 id 相同的包会替换内置包，因此你可以修正内置知识而无需 fork。非法的知识包会被
`mas doctor` 报告并跳过；它绝不会阻止其他包加载。

## `source`

| 键 | 默认值 | 含义 |
|---|---|---|
| `enabled` | `true` | 是否启用中间件源码获取 |
| `cache_dir` | `$TMPDIR/mas-src` | 已获取代码树的存放位置 |
| `network_timeout` | `10s` | 网络尝试在回退前最多允许的耗时 |
| `cache_ttl` | `24h` | 缓存代码树的新鲜期 |
| `repos` | — | 中间件种类 → 网络仓库 URL |
| `mirrors` | — | 中间件种类 → 本地镜像路径 |

回退链为：新鲜缓存 → 网络仓库 → 本地镜像。当网络不可达且已配置镜像时，运行会从镜像继续，
并记录 `MAS-4401`，因此报告会声明所参考的代码可能与部署版本不一致。在离网环境中，只配置
`mirrors` 即可，届时不会发起任何网络尝试。

## `run`

| 键 | 默认值 | 含义 |
|---|---|---|
| `default_topology` | `supervisor` | 未指定 `--topology` 时使用 |
| `default_mode` | `offline` | `offline` 或 `online` |
| `default_window` | `1h` | 未给出 `--since` 或 `--from/--to` 时使用 |
| `deterministic_short_circuit` | `0.85` | 达到该置信度即完全跳过 Agent 阶段 |
| `language` | `en` | `en` 或 `zh` |
| `max_concurrency` | `4` | 并发调查者数量 |
| `budget.max_steps` | `24` | 每次运行的推理步数 |
| `budget.max_tool_calls` | `40` | 每次运行的工具调用次数 |
| `budget.max_tokens` | `120000` | 每次运行的 token 数 |
| `budget.max_wall` | `5m` | 墙钟上限 |

`deterministic_short_circuit` 决定了一次常规故障要花你多少钱。在 `0.85` 下，一条以 0.9 置信度
确认内存压力的规则会让运行就此结束：零模型调用、亚秒返回。设为 `0` 则总是运行 Agent。

超出预算绝不会让运行失败。它会截断、记录 `MAS-3005`，并在报告中说明 —— 一份标注了局限的
部分分析，胜过没有分析。

## `store`

| 键 | 默认值 | 含义 |
|---|---|---|
| `type` | `fs` | `fs` 或 `memory` |
| `dir` | `runs` | `fs` 模式下的目录 |

记录携带 SHA-256 摘要。被修改或被截断的记录会以 `MAS-6003` 被拒绝，而不会被当作真实记录重放。

## `server`

| 键 | 默认值 | 含义 |
|---|---|---|
| `addr` | `:8080` | 监听地址 |
| `read_timeout` | `30s` | 请求读取超时 |
| `write_timeout` | `120s` | 响应写入超时 |

API 目前尚无认证。请勿将其暴露到可信网络之外。

## `safety`

| 键 | 默认值 | 含义 |
|---|---|---|
| `extra_denied_binaries` | — | 从白名单中移除可执行文件 |
| `extra_denied_args` | — | 额外拒绝的参数模式 |
| `max_response_bytes` | `8388608` | 响应大小上限 |
| `max_timeout` | `120s` | 单次调用超时上限 |

**这里的每一项都只会收紧守卫，没有一项会放宽它。** 不存在任何可以新增可执行文件、新增只读
路径或关闭守卫的配置键，并且有测试断言这样的键不存在。扩展 MAS-Turbo 能做的事，属于对软件
及其规格的修改，而不是对你配置的修改。

`mas tools` 会打印当前生效的白名单。

## 环境变量

| 变量 | 覆盖的配置 |
|---|---|
| `MAS_CONFIG` | 配置文件路径 |
| `MAS_LOG_LEVEL`、`MAS_LOG_FORMAT` | `log.*` |
| `MAS_LLM_PROVIDER`、`MAS_LLM_MODEL`、`MAS_LLM_API_KEY`、`MAS_LLM_BASE_URL`、`MAS_LLM_MOCK_SCRIPT` | `llm.*` |
| `MAS_METRICS_URL`、`MAS_LOGS_URL` | 第一个遥测数据源的 URL |
| `MAS_STORE_TYPE`、`MAS_STORE_DIR` | `store.*` |
| `MAS_SERVER_ADDR` | `server.addr` |
| `MAS_RUN_TOPOLOGY`、`MAS_RUN_MODE`、`MAS_RUN_LANGUAGE` | `run.*` |
| `MAS_RUN_MAX_STEPS`、`MAS_RUN_MAX_WALL` | `run.budget.*` |
| `MAS_SOURCE_CACHE_DIR` | `source.cache_dir` |
| `MAS_KNOWLEDGE_PACK_DIRS` | `knowledge.pack_dirs`（按路径列表分隔符分隔） |

无法识别的 `MAS_*` 变量会被忽略而不是致命错误，因此共享环境中的无关变量不会导致工具无法启动。

## 校验

`Validate` 报告**第一个**问题及其配置路径，并在同一条消息中说明还发现了多少个其他问题：

```
MAS-1003  配置在 targets[1].env 处非法：target "kafka-prod" references
          unknown environment "staging"（and 2 more: …）
```

`mas doctor` 更进一步：它会探测每一个端点并逐项报告。任何配置变更之后都应该跑一次。
