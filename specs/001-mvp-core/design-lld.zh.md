# 详细设计（LLD）：MVP 内核

> **特性 ID**：`001-mvp-core` · **版本**：1.0.0 · **状态**：已批准
> **双语对应文件**：[`design-lld.md`](./design-lld.md) · **上游**：[`design-hld.zh.md`](./design-hld.zh.md) v1.0.0 · **下游**：[`tasks.zh.md`](./tasks.zh.md)、代码

模块路径：`github.com/zlrrr/multi-agent-system-turbo`

## 1. 包结构

```
cmd/
  mas/                    CLI 入口
  sddctl/                 SDD 校验器（双语对等、可追溯性、需求覆盖）
pkg/
  errs/                   MAS-NNNN 注册表 + 带码错误类型            （公开 API）
internal/
  version/                通过 -ldflags 注入的构建元信息
  core/                   领域模型 —— 只引用 pkg/errs
  config/                 配置模型、加载/合并/校验、Secret
  obs/                    slog 初始化、脱敏 handler、运行上下文、自身指标
  safety/                 Guard、白名单、Redactor
  tool/                   Tool、Registry、Invoker、JSON Schema 校验
  collector/
    promql/               Prometheus 兼容客户端 + 工具
    loki/                 Loki 兼容客户端 + 工具
  envadapter/             Adapter 接口 + 注册表
    kube/                 只读 Kubernetes REST 客户端 + 工具
    local/                本地主机只读巡检 + 工具
  source/                 源码获取（网络→本地回退）+ 代码检索
  knowledge/              知识包 Schema、加载器、校验
    packs/                内置 YAML 知识包（redis.yaml、kafka.yaml）
  rules/                  确定性剧本引擎
  llm/                    Provider、Registry、规范消息类型
    mock/  anthropic/  openai/
  agent/                  Agent、State、角色、提示词、预算
  orchestrator/           Orchestrator、Registry、single/、supervisor/
  report/                 Markdown + JSON 渲染器
  store/                  RunStore + fs/ + memory/
  service/                准入、流水线、doctor、统计
  httpapi/                HTTP 服务端
  cli/                    cobra 命令
```

## 2. 包契约

### 2.1 `pkg/errs` —— 管辖 HLD §7.1

```go
type Severity string // "error" | "warn" | "info"

type Definition struct {
    Code        string   // "MAS-4001"
    Symbol      string   // "MetricsUnreachable"
    Severity    Severity
    MessageEN   string   // may contain %s verbs
    MessageZH   string
    RemedyEN    string
    RemedyZH    string
}

type Error struct { /* code, args, cause, fields */ }

func New(code string, args ...any) *Error
func Wrap(err error, code string, args ...any) *Error
func (e *Error) Error() string          // "MAS-4001: metrics endpoint ... unreachable"
func (e *Error) Code() string
func (e *Error) Unwrap() error
func (e *Error) With(k string, v any) *Error   // structured fields for logging
func CodeOf(err error) string                  // "" when uncoded
func Is(err error, code string) bool
func Lookup(code string) (Definition, bool)
func All() []Definition                        // sorted; drives `mas errcodes` and docs
```

- **不变量**：每个 `Definition.Code` 匹配 `^MAS-[1-9][0-9]{3}$`；错误码唯一；对未注册的错误码调用
  `New` 会 panic（由 `TestAllCodesRegistered` 拦截）。
- **测试**：注册表唯一性；消息格式化；穿透 wrap 的 `CodeOf`；双语完整性（中文字段不得为空）。

### 2.2 `internal/core` —— 管辖 HLD §6

```go
type MiddlewareKind string // "redis" | "kafka" | "mongodb" | "pulsar" | "milvus" | "oceanbase" | ...
type Mode string           // "offline" | "online"

type Window struct{ From, To time.Time }

type Target struct {
    ID        string
    Kind      MiddlewareKind
    Version   string
    Env       EnvBinding          // Kubernetes or local
    Labels    map[string]string   // telemetry selectors
    Endpoints EndpointOverrides
}

type DiagnoseRequest struct {
    Target   string
    Symptom  string
    Window   Window
    Mode     Mode
    Topology string
    Budget   Budget
    Options  map[string]string
}

type EvidenceKind string // "metric_series" | "log_lines" | "kube_object" | "host_state" | "command_output" | "source_ref" | "note"

type Evidence struct {
    ID          string
    Kind        EvidenceKind
    Source      string            // "promql" | "loki" | "kube" | "local" | "source"
    Query       string
    CollectedAt time.Time
    Payload     any               // typed per Kind; JSON-serialisable
    Summary     string            // one-line human/model-readable digest
    Truncated   bool
    Digest      string            // sha256 of canonical payload
}

type GapReason string // "unavailable" | "refused" | "truncated" | "not_configured" | "unsupported"

type Gap struct { ID, Intent string; Reason GapReason; Code string; Impact string }

type Severity string // "critical" | "major" | "minor" | "info"

type Finding struct {
    ID         string
    Origin     string   // "rule:redis.memory-pressure" | "agent:correlator"
    Severity   Severity
    Statement  string
    Evidence   []string // Evidence IDs
    Confidence float64  // 0..1
}

type HypothesisStatus string // "proposed" | "supported" | "refuted" | "inconclusive"

type Hypothesis struct {
    ID            string
    Statement     string
    Status        HypothesisStatus
    Confidence    float64
    Supporting    []string // Evidence/Finding IDs
    Contradicting []string
    Rationale     string
    Rank          int
}

type Risk string // "low" | "medium" | "high"

type Recommendation struct {
    Statement string
    Risk      Risk
    Rationale string
    Refs      []string
    Advisory  bool // always true — CON-003
}

type Usage struct {
    LLMCalls, PromptTokens, CompletionTokens, ToolCalls int
    CostUSD    float64
    WallMillis int64
}

type Report struct {
    Schema  string // "report/v1"
    RunID   string
    Target  Target
    Request DiagnoseRequest
    GeneratedAt time.Time
    Summary string
    Hypotheses      []Hypothesis
    Findings        []Finding
    ChecksPassed    []string
    Gaps            []Gap
    Recommendations []Recommendation
    Evidence        []Evidence
    Usage           Usage
    Topology        string
    Truncated       bool
}

type StepKind string // "tool_call" | "llm_call" | "rule_eval" | "phase" | "note"

type Step struct {
    ID string; Kind StepKind; At time.Time; DurationMillis int64
    Actor string; Name string
    Input any; Output any; Code string; Err string
}

type RunRecord struct {
    ID string; Request DiagnoseRequest; Target Target
    StartedAt, FinishedAt time.Time
    Steps []Step; Report *Report; Usage Usage
    Versions map[string]string // binary, packs, topology, provider/model
    Status string             // "running" | "completed" | "failed"
}
```

- **不变量**：`Evidence.ID` 形如 `ev-<n>` 且在单次运行内唯一；`Recommendation.Advisory` 恒为
  `true`；`Hypothesis.Confidence ∈ [0,1]`；`Report.Schema == "report/v1"`。
- **测试**：JSON 往返；不变量断言；`TestNoUpwardImports` 证明 `core` 只引用 `pkg/errs` 与标准库。

### 2.3 `internal/config` —— 管辖 HLD §7.4

```go
type Config struct {
    Version   string
    Log       LogConfig
    LLM       LLMConfig
    Telemetry TelemetryConfig    // Metrics + Logs sources by name
    Envs      map[string]EnvConfig
    Targets   []TargetConfig
    Knowledge KnowledgeConfig    // extra pack dirs
    Source    SourceConfig       // repos, mirrors, cache dir, timeouts
    Run       RunConfig          // budgets, short-circuit threshold, default topology
    Store     StoreConfig
    Server    ServerConfig
    Safety    SafetyConfig       // extra denies only; cannot widen
}

type Secret string
func (Secret) String() string { return "***" }          // and MarshalJSON → "***"
func (s Secret) Reveal(ctx context.Context) (string, error) // resolves ${env:..}/${file:..}

func Load(paths []string, env []string, overrides map[string]string) (*Config, error)
func (c *Config) Validate() error                       // coded, path-qualified errors
func (c *Config) Target(id string) (TargetConfig, error) // MAS-1005 when unknown
func Default() *Config
```

- **不变量**：`Safety` 只能新增拒绝项；`Load` 绝不返回无法通过 `Validate` 的配置；`Secret` 绝不
  序列化其明文值。
- **错误**：`MAS-1001` 配置文件非法、`MAS-1002` 未知字段、`MAS-1003` 校验失败、`MAS-1005` 未知
  目标、`MAS-1006` 密钥引用无法解析。
- **测试**：优先级（默认→文件→环境变量→命令行）；每种校验错误；`Secret` 在 JSON/`%v`/`%s` 下的
  脱敏；`${env:}`/`${file:}` 解析。

### 2.4 `internal/obs` —— 管辖 HLD §7.2

```go
func Setup(cfg config.LogConfig, r *safety.Redactor) *slog.Logger
type RunContext struct{ RunID string; Logger *slog.Logger; Metrics *Metrics }
func WithRun(ctx context.Context, rc *RunContext) context.Context
func FromContext(ctx context.Context) *RunContext
func Log(ctx context.Context) *slog.Logger   // always non-nil

type Metrics struct{ /* counters, histograms */ }
func (m *Metrics) IncCounter(name string, labels map[string]string)
func (m *Metrics) Observe(name string, v float64, labels map[string]string)
func (m *Metrics) WriteProm(w io.Writer) error  // text exposition format
```

- **不变量**：`Log` 绝不返回 nil；每个 handler 都被脱敏器包裹。
- **测试**：`run_id` 透传；已登记密钥在消息与属性中的脱敏；Prometheus 暴露格式可被解析。

### 2.5 `internal/safety` —— 管辖 HLD §7.3

```go
type Class string // "read_only" | "mutating"

type HTTPEffect struct{ Method, URL string }
type CommandEffect struct{ Binary string; Args []string }

type Call struct {
    Tool    string
    Class   Class
    HTTP    *HTTPEffect
    Command *CommandEffect
    Bytes   int
    Timeout time.Duration
}

type Guard struct{ /* immutable after New */ }
func NewGuard(cfg config.SafetyConfig) (*Guard, error)
func (g *Guard) Authorize(ctx context.Context, c Call) error   // nil = allowed
func (g *Guard) AllowedCommands() []CommandRule                // for `mas doctor`

type Redactor struct{}
func NewRedactor(patterns []string, secrets []string) *Redactor
func (r *Redactor) Redact(s string) string
func (r *Redactor) RedactAny(v any) any
```

守卫规则，全部默认拒绝：

| 检查 | 规则 |
|---|---|
| 1 · 类别 | `c.Class != read_only` ⇒ `MAS-8001` |
| 2 · HTTP 方法 | 方法 ∉ {GET, POST} ⇒ `MAS-8001`；POST 仅允许用于显式标记为查询端点的路径（`/api/v1/query`、`/api/v1/query_range`、`/loki/api/v1/query_range`） |
| 3 · HTTP 路径 | 路径必须匹配其数据源的白名单模式；Kubernetes 路径限制为对 `pods`、`pods/log`、`events`、`nodes`、`endpoints`、`services`、`deployments`、`statefulsets`、`configmaps`（仅元数据）的 `GET` ⇒ 否则 `MAS-8002` |
| 4 · 命令可执行文件 | 不在白名单（`redis-cli`、`kafka-topics.sh`、`mongosh`、`ps`、`ss`、`git` …）⇒ `MAS-8002` |
| 5 · 命令参数 | 任一参数命中变更类动词黑名单、Shell 元字符 ``[;&|`$><\n]`` 或 `..` 路径穿越 ⇒ `MAS-8005` |
| 6 · 上限 | `Bytes > max_response_bytes` 或 `Timeout > max_timeout` ⇒ `MAS-8010` |

- **不变量**：`Authorize` 是纯函数且不做 I/O；追加配置只能使规则更严。
- **测试**（对抗性，FR-006/NFR-003）：变更类动词出现在任意位置与任意大小写；`redis-cli --eval`
  与 `CONFIG SET`；`kubectl delete`；`git push`；元字符注入；URL 路径穿越；超限上限；未注册工具；
  在已知变更类命令上伪造 `Class` 为只读。

### 2.6 `internal/tool` —— 管辖 HLD §4.1

```go
type Safety = safety.Class

type Tool interface {
    Name() string
    Description() string
    ArgsSchema() Schema
    Safety() Safety
    Plan(args map[string]any) (safety.Call, error) // declares the effect for the guard
    Invoke(ctx context.Context, args map[string]any) (core.Evidence, error)
}

type Registry struct{}
func (r *Registry) Register(t Tool) error
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) List() []Tool
func (r *Registry) Definitions() []llm.ToolDefinition

type Invoker struct{ /* registry, guard, store, metrics */ }
func (in *Invoker) Invoke(ctx context.Context, name string, args map[string]any) (core.Evidence, *core.Gap)
```

`Invoker.Invoke` 是到达 `Tool.Invoke` 的**唯一**导出路径。它依次：按 Schema 校验参数 → 调用
`Tool.Plan` → `Guard.Authorize` → 带超时执行 → 记录一条 `Step` → 把任何错误转换为 `Gap`
（绝不把原始错误抛给调用方）。

- **测试**：未知工具；Schema 违规；守卫拒绝转化为带正确错误码的 `Gap`；超时转化为 `MAS-8010`；
  成功时记录步骤；`TestNoUnguardedIO` 扫描包依赖图，检查 `net/http` / `os/exec` 是否被用在
  `Invoke` 实现之外。

### 2.7 `internal/collector/promql`

```go
type Client struct{}
func New(cfg config.MetricsSource, hc *http.Client) *Client
func (c *Client) Instant(ctx, query string, at time.Time) (Result, error)
func (c *Client) Range(ctx, query string, w core.Window, step time.Duration) (Result, error)
func (c *Client) Series(ctx, matchers []string, w core.Window) ([]map[string]string, error)
func Tools(c *Client) []tool.Tool // promql.instant, promql.range, promql.series
```

- **错误**：`MAS-4001` 不可达、`MAS-4002` 非 2xx、`MAS-4003` 响应格式错误、`MAS-4004` 查询被服务端
  拒绝、`MAS-4005` 结果被截断（warn）。
- **测试**：instant/range/series 的 `httptest` 桩；bearer 与 basic 鉴权头；超时；在 `max_samples`
  处截断；按状态码的错误映射。

### 2.8 `internal/collector/loki`

```go
func (c *Client) Query(ctx, logQL string, w core.Window, limit int, dir Direction) (Streams, error)
func (c *Client) Labels(ctx, w core.Window) ([]string, error)
func (c *Client) LabelValues(ctx, label string, w core.Window) ([]string, error)
func Tools(c *Client) []tool.Tool // loki.query, loki.labels
```

- **错误**：`MAS-4101`…`MAS-4105`，与 promql 一组对应。
- **测试**：桩数据流；条数上限强制；时间窗收敛；标签发现。

### 2.9 `internal/envadapter` + `kube` + `local`

```go
type Binding struct {
    Kind      string            // "kubernetes" | "local"
    Instances []Instance        // pod or process identities
    Version   string
    Labels    map[string]string
    Notes     []string
}
type Adapter interface {
    Name() string
    Resolve(ctx context.Context, t config.TargetConfig) (Binding, error)
    Tools() []tool.Tool
    Probe(ctx context.Context) error   // for `mas doctor`
}
```

`kube` 实现一个只读 REST 客户端：

```go
func NewClient(cfg config.KubeConfigSpec) (*Client, error) // in-cluster | kubeconfig | explicit
func (c *Client) ListPods(ctx, ns string, selector string) ([]Pod, error)
func (c *Client) PodLogs(ctx, ns, pod, container string, opts LogOptions) (string, error)
func (c *Client) ListEvents(ctx, ns string, fieldSelector string) ([]Event, error)
func (c *Client) ListNodes(ctx) ([]Node, error)
func (c *Client) ListWorkloads(ctx, ns string) ([]Workload, error)
```

鉴权方式：集群内 ServiceAccount token；kubeconfig 的 bearer token、客户端证书，或 `exec` 凭据
插件（该插件本身也要经过守卫的命令白名单）。每个请求都是 `GET`；客户端不存在任何发出其他动词的
方法 —— 这是结构性保证，而非流程性保证。

`local` 实现本地主机只读巡检：进程列表、监听套接字、资源占用，以及知识包声明的白名单中间件巡检
命令。

- **错误**：`MAS-4201` 无权限、`MAS-4202` 无凭据、`MAS-4203` API 不可达、`MAS-4204` 对象不存在、
  `MAS-4301` 主机命令失败、`MAS-4302` 可执行文件不存在。
- **测试**：为每个端点与每种鉴权方式提供 `httptest` Kubernetes 桩；`local` 使用桩命令执行器；
  `TestKubeClientHasNoMutatingMethods`（对客户端类型做反射）。

### 2.10 `internal/source` —— 管辖 HLD §5.3

```go
type Origin string // "cache" | "network" | "local-mirror"
type Fetched struct{ Path string; Origin Origin; Ref string; Fallback bool }

type Fetcher struct{}
func New(cfg config.SourceConfig, run CommandRunner) *Fetcher
func (f *Fetcher) Fetch(ctx context.Context, kind core.MiddlewareKind, version string) (Fetched, *core.Gap)
func Search(root, pattern string, opts SearchOptions) ([]Match, error)
func Tools(f *Fetcher) []tool.Tool // source.fetch, source.search
```

回退顺序及其带码结果完全遵循 HLD §5.3。网络尝试受 `source.network_timeout`（默认 10 秒）约束，
因此网络分区的代价是秒级而非分钟级。

- **错误**：`MAS-4401` 已回退到本地镜像（warn）、`MAS-4402` 无可用源码、`MAS-4403` ref 不存在、
  `MAS-4404` 检索模式非法。
- **测试**：远端不可达 ⇒ `Fallback==true`、`Origin=="local-mirror"`、记录 Gap；无镜像 ⇒
  `MAS-4402`；命中缓存则跳过网络；检索返回文件/行号/上下文。

### 2.11 `internal/knowledge` —— 管辖 HLD §4（数据插件）

知识包 YAML Schema（加载时校验）：

```yaml
apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata: { middleware: redis, name: redis-core, version: 1.0.0, versionRange: ">=5.0" }
signals:            # named PromQL fragments, parameterised by {{.selector}}
  - id: memory_used
    promql: 'redis_memory_used_bytes{{"{{"}}.selector{{"}}"}}'
    unit: bytes
    description: { en: "...", zh: "..." }
logPatterns:
  - id: oom_command_not_allowed
    regex: 'OOM command not allowed when used memory'
    severity: critical
    meaning: { en: "...", zh: "..." }
failureModes:
  - id: memory-pressure
    title: { en: "...", zh: "..." }
    symptoms: ["latency", "evictions", "write errors"]
    indicators: ["memory_used/maxmemory > 0.9"]
    recommendations:
      - { risk: low, statement: { en: "...", zh: "..." } }
playbooks:
  - id: redis.memory-pressure
    matches: ["latency", "eviction", "oom"]
    steps: [...]     # see §2.12
inspect:             # commands the local/kube adapters may run for this middleware
  - id: info
    binary: redis-cli
    args: ["-h", "{{.host}}", "-p", "{{.port}}", "INFO"]
source:
  repos: ["https://github.com/redis/redis"]
```

```go
type Pack struct{ /* mirrors the YAML */ }
type Library struct{}
func Load(embedded fs.FS, extraDirs []string) (*Library, error)
func (l *Library) For(kind core.MiddlewareKind, version string) (*Pack, error)
func (l *Library) All() []*Pack
func (p *Pack) Validate() error
```

- **错误**：`MAS-5001` Schema 违规（带路径）、`MAS-5002` 知识包 id 重复、`MAS-5003` 该中间件无
  知识包、`MAS-5004` 版本区间非法。
- **测试**：逐种 Schema 违规；用户目录覆盖内置包；版本区间选择；每个 `{en,zh}` 字段的双语完整性；
  `inspect` 命令通过守卫校验。

### 2.12 `internal/rules` —— 管辖 HLD §5.1 阶段 1

一个剧本是一组有序步骤。共三种步骤类型：

```yaml
steps:
  - id: collect-memory
    collect: { tool: promql.instant, args: { query: "{{signal:memory_used}}" }, as: memory_used }
  - id: eval-pressure
    evaluate: 'memory_used.last / maxmemory.last > 0.9'
    onTrue:  { finding: { severity: major, confidence: 0.85,
                          statement: { en: "...", zh: "..." } } }
    onFalse: { pass: "memory headroom is adequate" }
  - id: conclude
    conclude: { failureMode: memory-pressure }
```

```go
type Engine struct{}
func New(inv *tool.Invoker, lib *knowledge.Library) *Engine
func (e *Engine) Select(pack *knowledge.Pack, symptom string) []*knowledge.Playbook
func (e *Engine) Run(ctx context.Context, pb *knowledge.Playbook, in Input) (Output, error)

type Output struct {
    Findings     []core.Finding
    Evidence     []core.Evidence
    Gaps         []core.Gap
    ChecksPassed []string
    LLMCalls     int // asserted == 0 by FR-008
}
```

表达式由 `expr-lang/expr` 在沙箱环境中求值，环境中只暴露步骤结果（`.last`、`.max`、`.min`、
`.avg`、`.count`、`.rate`）与辅助函数（`contains`、`matches`、`duration`）。不可访问环境变量，
不可做 I/O。

- **错误**：`MAS-5010` 表达式编译错误、`MAS-5011` 表达式类型错误、`MAS-5012` 未知信号引用、
  `MAS-5013` 步骤预算超限。
- **测试**：完整剧本正向路径；证据缺失 ⇒ 该步骤跳过并记 Gap，而非失败；表达式错误带码；
  `LLMCalls == 0`；NFR-002 墙钟断言。

### 2.13 `internal/llm` —— 管辖 HLD §4.2

```go
type Role string // "system" | "user" | "assistant" | "tool"
type Message struct {
    Role Role; Content string
    ToolCalls []ToolCall     // assistant
    ToolCallID string        // tool result
}
type ToolCall struct{ ID, Name string; Args map[string]any }
type ToolDefinition struct{ Name, Description string; Schema tool.Schema }

type Request struct {
    Model string; Messages []Message; Tools []ToolDefinition
    Temperature float64; MaxTokens int; StopAfterToolResult bool
}
type Response struct{ Text string; ToolCalls []ToolCall; StopReason string; Usage Usage }

type Provider interface {
    Name() string
    Complete(ctx context.Context, req Request) (Response, error)
    Close() error
}
func Register(name string, f Factory)
func Open(cfg config.LLMConfig) (Provider, error)
```

- `mock`：由 YAML/JSON 脚本驱动、按最后一条用户消息匹配的脚本化 provider；确定性、记录调用、
  支持固定的工具调用序列。它使 NFR-010 成为可能。
- `anthropic`：`POST /v1/messages`，原生 tool use，`x-api-key`、`anthropic-version` 头。
- `openai`：`POST /v1/chat/completions`，`tools`/`tool_calls`，`base_url` 可配，因而 OpenAI 兼容
  服务端可直接使用。
- **错误**：`MAS-2001` 不可用、`MAS-2002` 鉴权失败、`MAS-2003` 被限流、`MAS-2004` 工具调用无法
  解析、`MAS-2005` 未知供应商、`MAS-2006` 模型拒绝、`MAS-2007` token 预算超限。
- **测试**：两个真实供应商的 `httptest` 桩，含工具调用往返；错误映射；mock 确定性；错误串中的
  API key 脱敏。

### 2.14 `internal/agent` —— 管辖 HLD §4.3

```go
type Role string // "planner" | "investigator" | "correlator" | "critic" | "reporter"

type State struct {
    Run       *core.RunRecord
    Request   core.DiagnoseRequest
    Target    core.Target
    Pack      *knowledge.Pack
    Prior     []core.Finding   // deterministic phase output
    Evidence  []core.Evidence
    Gaps      []core.Gap
    Hypotheses []core.Hypothesis
    Recommendations []core.Recommendation
    Notes     []string
    Budget    Budget
    Usage     core.Usage
    Tools     *tool.Invoker
    Provider  llm.Provider
    // guarded accessors keep concurrent investigators safe
}

type Outcome struct{ Done bool; Message string }

type Agent interface {
    Role() Role
    Step(ctx context.Context, s *State) (Outcome, error)
}
```

预算（`MaxSteps`、`MaxToolCalls`、`MaxTokens`、`MaxWall`）由共享的 `toolLoop` 辅助函数强制；
超出任一预算会产出 `MAS-3005` 与一条截断说明，而不是一个错误。

提示词位于 `internal/agent/prompts/*.tmpl`，内嵌，每个角色一份，渲染时带入目标、知识包摘要、
前置结论与证据摘要 —— 绝不带入原始密钥。

- **测试**：每个角色对着带脚本的 mock provider；预算强制；非法工具调用 ⇒ 有界修复后记 Gap；
  假设始终携带证据 ID（针对 RSK-007 的结构性质量断言）。

### 2.15 `internal/orchestrator` —— 管辖 HLD §4.4

```go
type Orchestrator interface {
    Name() string
    Run(ctx context.Context, s *agent.State) error
}
func Register(name string, f Factory)
func Open(name string, deps Deps) (Orchestrator, error)
func Names() []string   // drives `mas topologies`
```

- `single`：一个全能 Agent，持有全部工具，做有界 ReAct 循环，随后执行报告步骤。
- `supervisor`：规划者产出调查计划 → 调查者**并发**运行，每个证据域（指标、日志、集群、源码）
  一个，各自只拿本域工具 → 关联者合并为假设 → 批判者拿证据逐条挑战并调整状态/置信度 → 报告者
  撰写摘要与建议。
- **错误**：`MAS-3001` 未知拓扑、`MAS-3002` 编排器失败、`MAS-3005` 预算超限、`MAS-3010` 无进展。
- **测试**：两种拓扑在相同 mock 脚本下均产出合法报告；注册表拒绝重复与未知名称；`-race` 下的
  并发安全。

### 2.16 `internal/report`、`internal/store`、`internal/service`、`internal/httpapi`、`internal/cli`

```go
// report
func Markdown(r *core.Report, lang string) ([]byte, error)   // lang: "en" | "zh"
func JSON(r *core.Report) ([]byte, error)

// store
type RunStore interface {
    Create(ctx context.Context, rec *core.RunRecord) error
    Append(ctx context.Context, runID string, step core.Step) error
    Finish(ctx context.Context, runID string, rep *core.Report, u core.Usage) error
    Get(ctx context.Context, runID string) (*core.RunRecord, error)
    List(ctx context.Context, limit int) ([]core.RunSummary, error)
}

// service
type Service struct{}
func New(deps Deps) (*Service, error)
func (s *Service) Diagnose(ctx context.Context, req core.DiagnoseRequest) (*core.Report, error)
func (s *Service) Replay(ctx context.Context, runID string) (*core.Report, error)
func (s *Service) Doctor(ctx context.Context) ([]CheckResult, error)
```

HTTP API（`report/v1` 报文体）：

| 方法 | 路径 | 含义 |
|---|---|---|
| POST | `/api/v1/diagnoses` | 创建一次运行；`?wait=true` 阻塞，否则返回 `202` + id |
| GET | `/api/v1/diagnoses/{id}` | 运行状态 + 报告 |
| GET | `/api/v1/diagnoses` | 列出运行 |
| GET | `/api/v1/targets` | 已配置目标 |
| GET | `/api/v1/topologies` | 可用拓扑 |
| GET | `/api/v1/packs` | 已加载知识包 |
| GET | `/healthz`、`/readyz` | 存活 / 就绪 |
| GET | `/metrics` | Prometheus 暴露格式 |

CLI：`mas diagnose | serve | doctor | replay | errcodes | packs | topologies | targets | version`。

## 3. 配置 Schema

```yaml
version: "1"
log:   { level: info, format: json, redact: ["password", "token", "api[_-]?key"] }
llm:
  provider: mock                 # mock | anthropic | openai
  model: claude-opus-5
  api_key: "${env:ANTHROPIC_API_KEY}"
  base_url: ""                   # openai-compatible endpoints
  timeout: 60s
  max_tokens: 4096
  per_agent:                     # optional model override per role
    investigator: { model: claude-haiku-4-5-20251001 }
telemetry:
  metrics:
    - name: primary
      type: prometheus           # prometheus | victoriametrics (same wire API)
      url: http://prometheus:9090
      auth: { type: bearer, token: "${env:PROM_TOKEN}" }
      timeout: 15s
      max_samples: 11000
  logs:
    - name: primary
      type: loki
      url: http://loki:3100
      timeout: 20s
      max_lines: 1000
envs:
  prod-k8s:
    type: kubernetes
    kubeconfig: "${env:KUBECONFIG}"   # empty ⇒ in-cluster
    context: ""
    namespace: middleware
  edge-host:
    type: local
targets:
  - id: redis-prod
    kind: redis
    env: prod-k8s
    selector: "app=redis,role=master"
    labels: { job: redis, instance_label: instance }
    version: "7.2"
knowledge: { pack_dirs: ["/etc/mas/packs"] }
source:
  cache_dir: /var/lib/mas/src
  network_timeout: 10s
  repos:   { redis: "https://github.com/redis/redis", kafka: "https://github.com/apache/kafka" }
  mirrors: { redis: "/srv/mirrors/redis.git" }
run:
  default_topology: supervisor
  default_mode: offline
  deterministic_short_circuit: 0.85
  budget: { max_steps: 24, max_tool_calls: 40, max_tokens: 120000, max_wall: 5m }
store:  { type: fs, dir: /var/lib/mas/runs }
server: { addr: ":8080", read_timeout: 30s }
safety: { extra_denied_args: [], max_response_bytes: 8388608, max_timeout: 120s }
```

环境变量叠加：`MAS_LLM_PROVIDER`、`MAS_LOG_LEVEL`、`MAS_STORE_DIR` …（点分路径的大写下划线形式，
加 `MAS_` 前缀）。

## 4. 错误码分配

完整注册表位于 `pkg/errs/registry.go`，并渲染到 `docs/*/error-codes.md`。分配区段见 HLD §7.1；
具体错误码见上文 §2 各包。

## 5. 测试矩阵

| 测试 | 层级 | 目标 | 需求 |
|---|---|---|---|
| `errs` 注册表完整性、双语完整性 | 单元 | `pkg/errs` | FR-017、NFR-009 |
| 配置优先级、校验、密钥脱敏 | 单元 | `internal/config` | FR-001、FR-016 |
| 守卫对抗性套件（≥30 条恶意输入） | 单元 | `internal/safety` | FR-006、NFR-003 |
| `TestNoUnguardedIO`、`TestKubeClientHasNoMutatingMethods` | 结构性 | 全仓库 | NFR-003 |
| `TestNoUpwardImports` | 结构性 | 全仓库 | HLD §3 |
| promql / loki / kube 桩，含鉴权、超时、截断 | 集成 | 采集器 | FR-003…005、NFR-004 |
| 本地适配器（桩执行器） | 单元 | `envadapter/local` | FR-021 |
| 远端不可达下的源码回退 | 集成 | `internal/source` | FR-022 |
| 固定代码树上的检索 | 单元 | `internal/source` | FR-023 |
| 知识包 Schema 违规；内置包合法 | 单元 | `internal/knowledge` | FR-007 |
| 剧本运行 `LLMCalls == 0` 且 < 2 秒 | 集成 | `internal/rules` | FR-008、NFR-002 |
| Provider 往返，含工具调用 | 集成 | `internal/llm` | FR-010 |
| 角色行为对脚本化 mock | 单元 | `internal/agent` | FR-009 |
| 两种拓扑产出合法报告；`-race` | 集成 | `internal/orchestrator` | FR-009 |
| 报告黄金文件（md + json，en + zh） | 单元 | `internal/report` | FR-011 |
| 运行存储往返；无网络重放 | 集成 | `store`、`service` | FR-012 |
| 降级：所有源不可用 ⇒ 运行完成并带 Gap | 集成 | `service` | FR-013 |
| 端到端诊断（mock）< 5 秒 | 集成 | `service` | NFR-001 |
| 确定性：两次相同运行 ⇒ 相同报告 | 集成 | `service` | NFR-010 |
| 每个子命令的 CLI 冒烟 | 集成 | `cli` | FR-014 |
| 每个端点的 API 测试 | 集成 | `httpapi` | FR-015 |
| doctor 对桩服务 | 集成 | `service` | FR-018 |
| 仅通过知识包新增中间件 | 集成 | `knowledge` | NFR-007 |
| 镜像可构建、非 root、可跑 diagnose | CI | 交付 | FR-020、NFR-005 |
| `sddctl verify` —— 对等、可追溯、覆盖 | CI | 全仓库 | NFR-009、FR-017 |

## Change Log

| 版本 | 日期 | 变更 | 影响 |
|---|---|---|---|
| 1.0.0 | 2026-08-23 | 初版详细设计 | `tasks.zh.md`、代码 |
