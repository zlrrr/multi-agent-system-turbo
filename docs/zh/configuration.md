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
| `per_agent` | — | 按角色覆盖 `provider`、`model` 与 `temperature` |
| `providers` | — | 可供角色路由到的具名备选 provider |
| `pricing` | — | 按模型的价格，用于计算一次运行的成本 |

### 把角色路由到不同的模型

`per_agent` 的存在是因为不同角色需求不同。调查者主要做抽取与归纳；真正需要判断力的是关联与
批判。角色可以覆盖模型、温度，也可以整体覆盖 **provider**：

```yaml
llm:
  provider: anthropic
  model: claude-opus-5              # 关联者、批判者、报告者
  api_key: ${env:ANTHROPIC_API_KEY}

  providers:                        # 具名备选
    local:
      provider: openai
      base_url: http://127.0.0.1:11434/v1
      model: qwen2.5:14b

  per_agent:
    investigator: { provider: local }     # 廉价抽取
    executor:     { provider: local }
    correlator:   { temperature: 0.1 }    # 默认 provider，温度更低
```

具名 provider 会继承默认配置中它没有设置的每一个字段，角色也会继承它没有覆盖的一切。
这一点比看上去更要紧：只改了温度的角色不应因此丢掉端点与密钥 ——
按角色把配置重述一遍，正是生产运行栽在"某人漏掉的那一个字段"上的方式。

一次运行所路由到的每个 provider 都在准入时打开，因此错误的凭据会让这次运行被拒，
而不是变成三分钟后才发现的一条缺口。执行 `mas models` 可查看实际会生效的路由。

### 定价

**本项目不内置任何价目表。** 价格会变、随合同与区域而异，
而一个看起来权威的过期数字就是一句假话。所以价格由你来提供：

```yaml
llm:
  pricing:
    claude-opus-5:  { input_per_mtok: 5.00, output_per_mtok: 25.00 }
    qwen2.5:14b:    { input_per_mtok: 0,    output_per_mtok: 0 }   # 自建
```

**没有条目**的模型会让本次运行的成本变为*未知*。它绝不会被报告为 0 ——
一份声称本次运行没花钱的报告，比一份什么都不说的报告更糟，因为你会相信它。
而显式配置为 `0` 的价格是另一回事，仍属已知：自建模型在边际上确实免费，
写下 `0` 就是在有意这么说。

对部分模型定了价、部分没有的运行，会同时报告已定价的那部分**并**点名无法定价的模型，
因此那个数字不会被误当成总额。`mas doctor` 与 `mas models` 都会报告哪些模型已定价。

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
| `type` | `fs` | `fs`、`memory` 或 `s3` |
| `dir` | `runs` | `fs` 模式下的目录 |
| `s3.*` | *（无）* | `s3` 模式下的桶配置 |

无论存放在哪里，记录都携带 SHA-256 摘要。被修改或被截断的记录会以 `MAS-6003` 被拒绝，
而不会被当作真实记录重放。

### `store.s3` —— 所有副本都能看到的存储

`fs` 把运行记录放在单机磁盘上。在 Kubernetes 中，那通常是 Pod 的磁盘，
因此一次重启就会丢掉历史，而第二个副本也看不到第一个副本的运行记录 ——
于是 `GET /api/v1/diagnoses` 的回答会取决于哪个 Pod 接到了请求。

```yaml
store:
  type: s3
  s3:
    endpoint: http://minio:9000        # 或 https://s3.eu-west-1.amazonaws.com
    region: us-east-1
    bucket: mas-runs
    prefix: prod                       # 可选，便于一个桶承载多套部署
    access_key_id: "${env:MAS_S3_KEY_ID}"
    secret_access_key: "${env:MAS_S3_SECRET}"
    path_style: true                   # MinIO、Ceph RGW 与多数自建部署
    timeout: 30s
```

| 键 | 含义 |
|---|---|
| `endpoint` | 服务 URL。必填 |
| `region` | 签名所用 region。必填 —— MinIO 接受任意值，AWS 不接受 |
| `bucket` | 必填 |
| `prefix` | 键前缀，便于一个桶服务多套部署 |
| `access_key_id` / `secret_access_key` | 密钥。要么都填，要么都留空（匿名桶） |
| `path_style` | 把桶放在路径而不是主机名里。多数自建部署应为 `true` |
| `timeout` | 单请求超时，默认 `30s` |

凭据只来自配置 —— 不来自实例元数据，也不来自 `~/.aws`，
因为多两个来源就意味着多两种"搞不清用的是哪个身份"的意外。

**什么东西存在哪里。** 每次运行各占一个前缀：

```
<prefix>/runs/<runID>/record.json      Create 时写入，Finish 时再写一次
<prefix>/runs/<runID>/steps/0001.json  只写一次，此后不再写
```

没有任何东西被重写，因此"只追加"的保证在一个没有 append 的后端上依然成立 ——
并且一次在这两次写入之间被打断的运行仍然可读，
因为它的步骤在发生的当时就已经持久化了。
重建出的运行会保持 `status: running`：
它是被记录下来的样子，而不是"它完成了"的主张。

桶策略归桶策略。加密、版本控制、对象锁定、生命周期与保留，
都在配置桶的地方、由拥有桶的人来配置。

`mas doctor` 会报告当前使用的是哪种存储；对 `s3` 还会报告该桶是否应答。
如果存储是在分析**完成之后**才失败的，你仍然会拿到报告，
并附有一条"未持久化"的说明 ——
因为没能把答案归档就把答案一起丢掉，在故障处置中是错误的交易。

## `server`

| 键 | 默认值 | 含义 |
|---|---|---|
| `addr` | `:8080` | 监听地址 |
| `read_timeout` | `30s` | 请求读取超时 |
| `write_timeout` | `120s` | 响应写入超时 |
| `auth.tokens[]` | *（无）* | API 接受的 Bearer 凭据 |
| `tls.cert_file` / `tls.key_file` | *（无）* | 用这一对证书与私钥提供 TLS |
| `tls.terminated_by_proxy` | `false` | 本进程之前已有组件终止 TLS |

### 挂在地址上的那条规则

这个要求不是一个开关 —— 它由"这个套接字能被谁访问到"推出：

| 监听地址 | 认证 | TLS | 结果 |
|---|---|---|---|
| 回环（`127.0.0.1`、`[::1]`、`localhost`） | 不要求 | 不要求 | 启动 |
| 其他任何地址 | **必需** | —— | 拒绝启动（`MAS-7010`） |
| 其他任何地址 | 已配置 | 缺失，且未声明代理 | 拒绝启动（`MAS-7011`） |
| 其他任何地址 | 已配置 | 已提供，或已声明代理 | 启动 |

笔记本上无需任何配置，而暴露出去的部署，也不会因为谁忘了设置而处于未认证状态。
`mas doctor` 会以警告形式报告暴露面；`mas serve` 则直接拒绝。

`terminated_by_proxy` 记录的是本进程无法验证的一个事实 ——
它前面有一个 ingress 或 sidecar 在终止 TLS。它必须被亲手写下，
因为"写下它"本身就是那份确认。

### `server.auth.tokens[]`

| 键 | 含义 |
|---|---|
| `name` | 主体。它是审计行中出现的名字，也是运行记录中作为调用者被记下的名字 |
| `token` | 凭据，以密钥形式给出：字面量、`${env:VAR}` 或 `${file:/path}` |
| `scopes` | `read`、`diagnose`，或两者 |

```yaml
server:
  addr: "0.0.0.0:8080"
  auth:
    tokens:
      - name: dashboard
        token: "${env:MAS_DASHBOARD_TOKEN}"
        scopes: [read]
      - name: oncall
        token: "${file:/etc/mas/oncall.token}"
        scopes: [read, diagnose]
  tls:
    terminated_by_proxy: true
```

`read` 覆盖一切已经算好的东西：已存储的诊断、目标、拓扑、知识包与 `/metrics`。
`diagnose` 覆盖 `POST /api/v1/diagnoses` —— 它会花掉模型 token 并读取生产遥测 ——
一个状态页需要前者，且绝不该拥有后者。

没有任何 scope 的 token，或含本版本不认识 scope 的 token，会在加载时失败（`MAS-7013`）。
一个被忽略的 scope，是一次你以为自己已经授出的授权。

`/healthz` 与 `/readyz` 永不要求凭据：
一个需要凭据的存活探针，会在凭据出问题时一起失败。

### 租户 —— 用同一个部署服务多个团队

默认情况下，一个可以做诊断的凭据，可以诊断**任何**已配置的目标。
对于"一个团队照看自己那片资产"来说这没问题；
而当这片资产不属于某一个团队时，它就是错的。

在目标上写下 `tenant`，就是在说它属于资产中的哪一片：

```yaml
targets:
  - id: payments-redis
    kind: redis
    tenant: payments
  - id: search-kafka
    kind: kafka
    tenant: search

server:
  auth:
    tokens:
      - name: payments-oncall
        token: "${env:PAYMENTS_TOKEN}"
        scopes: [read, diagnose]
        tenants: [payments]
      - name: platform
        token: "${env:PLATFORM_TOKEN}"
        scopes: [read]
        tenants: [payments, search]
```

**这里没有开关。** 只要有任何一个目标写下了 tenant，这份配置就是多租户的，
其余规则在加载时生效：

| 配置情况 | 结果 |
|---|---|
| 没有任何目标写 tenant | 租户关闭；无需配置任何东西，也什么都不会变 |
| 一部分目标写了，另一部分没写 | 拒绝（`MAS-1013`）。不属于任何人的目标，要么谁都能碰、要么谁都不能碰 |
| 某个凭据没写 tenants | 拒绝。在已分区的部署里，那是一个没有人声明过的超级用户 |
| 某个凭据写了没有任何目标声明过的租户 | 拒绝 |
| 某个凭据写了 tenants，但没有任何目标带租户 | 拒绝 —— 该限制会被静默忽略，而这正是"看起来已生效、实际并未生效"的控制的典型形态 |

用开关的话，一个已分区的部署有可能在未分区的状态下运行 ——
而这恰恰是本安排唯一要杜绝的失败。

**强制执行是什么样子。** 对别人的目标发起诊断，以及读取别人的运行记录，
都会返回 **`404`** —— 与一个从未被配置过的 id 完全一致。
一个指名了目标的 `403` 会确认它存在，
那是邻居的信息而不是调用者的，并且每猜一个 id 就泄露一次。
目标与运行记录的列举都会被过滤为调用者可见的部分。

每次运行都会把它所属的租户与"是谁请求的"一并记录下来。
它在运行发生时写入，而不是事后推导：
在查询时去读目标的租户，回答的是*那个目标现在归谁*，而审计问的是过去。

`mas doctor` 会报告租户是否开启、以及每个凭据能触达什么 ——
按名字给出，绝不给出凭据本身。
因为被过滤后的列表看起来与"资产为空"一模一样，所以答案必须只有一条命令之遥。

刻意不做的：按租户配置模型、预算、知识包或遥测数据源；按租户切分存储；
层级、分组或委派；以及配额。租户也不作用于 CLI ——
它以运维人员的身份、用运维人员自己的文件运行，文件里的每个目标本来就是他们的。

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
