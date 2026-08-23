# Low-Level Design (LLD): MVP Core

> **Feature ID**: `001-mvp-core` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

Module path: `github.com/zlrrr/multi-agent-system-turbo`

## 1. Package layout

```
cmd/
  mas/                    CLI entrypoint
  sddctl/                 SDD verifier (bilingual parity, traceability, requirement coverage)
pkg/
  errs/                   MAS-NNNN registry + coded error type          (public API)
internal/
  version/                build metadata injected via -ldflags
  core/                   domain model — imports only pkg/errs
  config/                 config model, load/merge/validate, Secret
  obs/                    slog setup, redaction handler, run context, self-metrics
  safety/                 Guard, allow-lists, Redactor
  tool/                   Tool, Registry, Invoker, JSON-schema validation
  collector/
    promql/               Prometheus-compatible client + tools
    loki/                 Loki-compatible client + tools
  envadapter/             Adapter interface + registry
    kube/                 read-only Kubernetes REST client + tools
    local/                local host read-only inspection + tools
  source/                 source acquisition (network→local fallback) + code search
  knowledge/              Pack schema, loader, validation
    packs/                embedded YAML packs (redis.yaml, kafka.yaml)
  rules/                  deterministic playbook engine
  llm/                    Provider, Registry, canonical message types
    mock/  anthropic/  openai/
  agent/                  Agent, State, roles, prompts, budgets
  orchestrator/           Orchestrator, Registry, single/, supervisor/
  report/                 Markdown + JSON renderers
  store/                  RunStore + fs/ + memory/
  service/                admission, pipeline, doctor, accounting
  httpapi/                HTTP server
  cli/                    cobra commands
```

## 2. Package contracts

### 2.1 `pkg/errs` — governs HLD §7.1

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

- **Invariants**: every `Definition.Code` matches `^MAS-[1-9][0-9]{3}$`; codes are unique;
  `New` panics on an unregistered code (caught by `TestAllCodesRegistered`).
- **Tests**: registry uniqueness; message formatting; `CodeOf` through wrapping;
  bilingual completeness (no empty ZH field).

### 2.2 `internal/core` — governs HLD §6

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

- **Invariants**: `Evidence.ID` is `ev-<n>` and unique in a run; `Recommendation.Advisory` is
  always `true`; `Hypothesis.Confidence ∈ [0,1]`; `Report.Schema == "report/v1"`.
- **Tests**: JSON round-trip; invariant assertions; `TestNoUpwardImports` proves `core` imports
  only `pkg/errs` and the standard library.

### 2.3 `internal/config` — governs HLD §7.4

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

- **Invariants**: `Safety` may only add denies; `Load` never returns a config that fails
  `Validate`; `Secret` never serialises its value.
- **Errors**: `MAS-1001` invalid config file, `MAS-1002` unknown field, `MAS-1003` validation
  failure, `MAS-1005` unknown target, `MAS-1006` unresolvable secret reference.
- **Tests**: precedence (default→file→env→flag), each validation error, secret redaction in
  JSON/`%v`/`%s`, `${env:}`/`${file:}` resolution.

### 2.4 `internal/obs` — governs HLD §7.2

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

- **Invariants**: `Log` never returns nil; every handler is wrapped by the redactor.
- **Tests**: `run_id` propagation; redaction of registered secrets in message and attrs;
  Prometheus exposition parses.

### 2.5 `internal/safety` — governs HLD §7.3

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

Guard rules, all deny-by-default:

| Check | Rule |
|---|---|
| 1 · class | `c.Class != read_only` ⇒ `MAS-8001` |
| 2 · HTTP method | method ∉ {GET, POST} ⇒ `MAS-8001`; POST allowed only on paths explicitly marked query-endpoints (`/api/v1/query`, `/api/v1/query_range`, `/loki/api/v1/query_range`) |
| 3 · HTTP path | path must match an allow-list pattern for its source; Kubernetes paths restricted to `GET` on `pods`, `pods/log`, `events`, `nodes`, `endpoints`, `services`, `deployments`, `statefulsets`, `configmaps`(metadata only) ⇒ else `MAS-8002` |
| 4 · command binary | binary ∉ allow-list (`redis-cli`, `kafka-topics.sh`, `mongosh`, `ps`, `ss`, `git`, …) ⇒ `MAS-8002` |
| 5 · command args | any arg matching the mutating-verb denylist, shell metacharacters ``[;&|`$><\n]``, or `..` traversal ⇒ `MAS-8005` |
| 6 · ceilings | `Bytes > max_response_bytes` or `Timeout > max_timeout` ⇒ `MAS-8010` |

- **Invariants**: `Authorize` is pure and has no I/O; adding config can only narrow.
- **Tests** (adversarial, FR-006/NFR-003): mutating verbs in every position and case;
  `redis-cli --eval` and `CONFIG SET`; `kubectl delete`; `git push`; metacharacter injection;
  URL path traversal; oversized ceilings; unregistered tool; `Class` forged as read-only on a
  known-mutating command.

### 2.6 `internal/tool` — governs HLD §4.1

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

`Invoker.Invoke` is the **only** exported path that reaches `Tool.Invoke`. It: validates args
against the schema → calls `Tool.Plan` → `Guard.Authorize` → invokes with timeout → records a
`Step` → converts any error into a `Gap` (never propagating a raw error to a caller).

- **Tests**: unknown tool; schema violation; guard refusal becomes a `Gap` with the right code;
  timeout becomes `MAS-8010`; success records a step; `TestNoUnguardedIO` scans the package
  graph for `net/http` / `os/exec` use outside `Invoke` implementations.

### 2.7 `internal/collector/promql`

```go
type Client struct{}
func New(cfg config.MetricsSource, hc *http.Client) *Client
func (c *Client) Instant(ctx, query string, at time.Time) (Result, error)
func (c *Client) Range(ctx, query string, w core.Window, step time.Duration) (Result, error)
func (c *Client) Series(ctx, matchers []string, w core.Window) ([]map[string]string, error)
func Tools(c *Client) []tool.Tool // promql.instant, promql.range, promql.series
```

- **Errors**: `MAS-4001` unreachable, `MAS-4002` non-2xx, `MAS-4003` malformed response,
  `MAS-4004` query rejected by server, `MAS-4005` result truncated (warn).
- **Tests**: `httptest` stub for instant/range/series; bearer + basic auth headers; timeout;
  truncation at `max_samples`; error mapping per status.

### 2.8 `internal/collector/loki`

```go
func (c *Client) Query(ctx, logQL string, w core.Window, limit int, dir Direction) (Streams, error)
func (c *Client) Labels(ctx, w core.Window) ([]string, error)
func (c *Client) LabelValues(ctx, label string, w core.Window) ([]string, error)
func Tools(c *Client) []tool.Tool // loki.query, loki.labels
```

- **Errors**: `MAS-4101`…`MAS-4105` mirroring the promql set.
- **Tests**: stub streams; limit enforcement; window clamping; label discovery.

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

`kube` implements a read-only REST client:

```go
func NewClient(cfg config.KubeConfigSpec) (*Client, error) // in-cluster | kubeconfig | explicit
func (c *Client) ListPods(ctx, ns string, selector string) ([]Pod, error)
func (c *Client) PodLogs(ctx, ns, pod, container string, opts LogOptions) (string, error)
func (c *Client) ListEvents(ctx, ns string, fieldSelector string) ([]Event, error)
func (c *Client) ListNodes(ctx) ([]Node, error)
func (c *Client) ListWorkloads(ctx, ns string) ([]Workload, error)
```

Auth modes: in-cluster service-account token; kubeconfig with bearer token, client
certificate, or an `exec` credential plugin (the plugin is itself run through the guard's
command allow-list). Every request is `GET`; the client has no method that issues any other
verb — a structural, not procedural, guarantee.

`local` implements read-only host inspection: process listing, listening sockets, resource
usage, and allow-listed middleware inspection commands declared by knowledge packs.

- **Errors**: `MAS-4201` forbidden, `MAS-4202` no credentials, `MAS-4203` API unreachable,
  `MAS-4204` object not found, `MAS-4301` host command failed, `MAS-4302` binary not found.
- **Tests**: `httptest` Kubernetes stub for each endpoint and each auth mode; stubbed command
  runner for `local`; `TestKubeClientHasNoMutatingMethods` (reflection over the client type).

### 2.10 `internal/source` — governs HLD §5.3

```go
type Origin string // "cache" | "network" | "local-mirror"
type Fetched struct{ Path string; Origin Origin; Ref string; Fallback bool }

type Fetcher struct{}
func New(cfg config.SourceConfig, run CommandRunner) *Fetcher
func (f *Fetcher) Fetch(ctx context.Context, kind core.MiddlewareKind, version string) (Fetched, *core.Gap)
func Search(root, pattern string, opts SearchOptions) ([]Match, error)
func Tools(f *Fetcher) []tool.Tool // source.fetch, source.search
```

Fallback order and its coded outcomes are exactly HLD §5.3. Network attempts are bounded by
`source.network_timeout` (default 10 s) so a partition costs seconds, not minutes.

- **Errors**: `MAS-4401` fell back to local mirror (warn), `MAS-4402` no source available,
  `MAS-4403` ref not found, `MAS-4404` search pattern invalid.
- **Tests**: unreachable remote ⇒ `Fallback==true`, `Origin=="local-mirror"`, gap recorded;
  no mirror ⇒ `MAS-4402`; cache hit skips network; search returns file/line/context.

### 2.11 `internal/knowledge` — governs HLD §4 (data plugin)

Pack YAML schema (validated on load):

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

- **Errors**: `MAS-5001` schema violation (path-qualified), `MAS-5002` duplicate pack id,
  `MAS-5003` no pack for middleware, `MAS-5004` bad version range.
- **Tests**: each schema violation; user dir overrides embedded; version-range selection;
  bilingual completeness of every `{en,zh}` field; `inspect` commands are guard-clean.

### 2.12 `internal/rules` — governs HLD §5.1 Phase 1

A playbook is an ordered list of steps. Three step types:

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

Expressions are evaluated with `expr-lang/expr` over a sandboxed environment exposing only the
step results (`.last`, `.max`, `.min`, `.avg`, `.count`, `.rate`) and helper functions
(`contains`, `matches`, `duration`). No environment access, no I/O.

- **Errors**: `MAS-5010` expression compile error, `MAS-5011` expression type error,
  `MAS-5012` unknown signal reference, `MAS-5013` step budget exceeded.
- **Tests**: full playbook happy path; missing evidence ⇒ step skipped with gap, not failure;
  expression errors are coded; `LLMCalls == 0`; NFR-002 wall-clock assertion.

### 2.13 `internal/llm` — governs HLD §4.2

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

- `mock`: a scripted provider driven by a YAML/JSON script matching on the last user message;
  deterministic, records calls, supports fixed tool-call sequences. Enables NFR-010.
- `anthropic`: `POST /v1/messages`, native tool use, `x-api-key`, `anthropic-version` header.
- `openai`: `POST /v1/chat/completions`, `tools`/`tool_calls`, configurable `base_url` so
  OpenAI-compatible servers work unchanged.
- **Errors**: `MAS-2001` unavailable, `MAS-2002` auth failed, `MAS-2003` rate-limited,
  `MAS-2004` unparseable tool call, `MAS-2005` unknown provider, `MAS-2006` model refused,
  `MAS-2007` token budget exceeded.
- **Tests**: `httptest` stubs for both real providers incl. tool-call round-trip; error mapping;
  mock determinism; redaction of API keys in error strings.

### 2.14 `internal/agent` — governs HLD §4.3

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

Budgets (`MaxSteps`, `MaxToolCalls`, `MaxTokens`, `MaxWall`) are enforced by the shared
`toolLoop` helper; exceeding one yields `MAS-3005` and a truncation note rather than an error.

Prompts live in `internal/agent/prompts/*.tmpl`, embedded, one per role, and are rendered with
the target, pack summary, prior findings and evidence digests — never with raw secrets.

- **Tests**: each role against the mock provider with a scripted transcript; budget
  enforcement; invalid tool call ⇒ bounded repair then gap; hypotheses always carry evidence
  IDs (structural quality assertion for RSK-007).

### 2.15 `internal/orchestrator` — governs HLD §4.4

```go
type Orchestrator interface {
    Name() string
    Run(ctx context.Context, s *agent.State) error
}
func Register(name string, f Factory)
func Open(name string, deps Deps) (Orchestrator, error)
func Names() []string   // drives `mas topologies`
```

- `single`: one generalist agent, all tools, bounded ReAct loop, then a report step.
- `supervisor`: planner produces an investigation plan → investigators run **concurrently**,
  one per evidence domain (metrics, logs, cluster, source), each with only its domain's tools →
  correlator merges into hypotheses → critic challenges each against the evidence and adjusts
  status/confidence → reporter writes the summary and recommendations.
- **Errors**: `MAS-3001` unknown topology, `MAS-3002` orchestrator failed,
  `MAS-3005` budget exceeded, `MAS-3010` no progress.
- **Tests**: both topologies over identical mock scripts produce a valid report; registry
  rejects duplicates and unknown names; concurrency safety under `-race`.

### 2.16 `internal/report`, `internal/store`, `internal/service`, `internal/httpapi`, `internal/cli`

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

HTTP API (`report/v1` bodies):

| Method | Path | Meaning |
|---|---|---|
| POST | `/api/v1/diagnoses` | create a run; `?wait=true` blocks, otherwise returns `202` + id |
| GET | `/api/v1/diagnoses/{id}` | run status + report |
| GET | `/api/v1/diagnoses` | list runs |
| GET | `/api/v1/targets` | configured targets |
| GET | `/api/v1/topologies` | available topologies |
| GET | `/api/v1/packs` | loaded knowledge packs |
| GET | `/healthz`, `/readyz` | liveness / readiness |
| GET | `/metrics` | Prometheus exposition |

CLI: `mas diagnose | serve | doctor | replay | errcodes | packs | topologies | targets | version`.

## 3. Configuration schema

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

Environment overlay: `MAS_LLM_PROVIDER`, `MAS_LOG_LEVEL`, `MAS_STORE_DIR`, … (upper-snake of
the dotted path, `MAS_` prefixed).

## 4. Error-code allocations

Full registry lives in `pkg/errs/registry.go` and is rendered to `docs/*/error-codes.md`.
Allocation blocks are HLD §7.1; specific codes are listed per package in §2 above.

## 5. Test matrix

| Test | Level | Target | Requirement |
|---|---|---|---|
| `errs` registry integrity, bilingual completeness | unit | `pkg/errs` | FR-017, NFR-009 |
| Config precedence & validation & secret redaction | unit | `internal/config` | FR-001, FR-016 |
| Guard adversarial suite (≥30 hostile inputs) | unit | `internal/safety` | FR-006, NFR-003 |
| `TestNoUnguardedIO`, `TestKubeClientHasNoMutatingMethods` | structural | repo | NFR-003 |
| `TestNoUpwardImports` | structural | repo | HLD §3 |
| promql / loki / kube stubs incl. auth, timeout, truncation | integration | collectors | FR-003…005, NFR-004 |
| Local adapter with stubbed runner | unit | `envadapter/local` | FR-021 |
| Source fallback under unreachable remote | integration | `internal/source` | FR-022 |
| Code search on a fixture tree | unit | `internal/source` | FR-023 |
| Pack schema violations; embedded packs valid | unit | `internal/knowledge` | FR-007 |
| Playbook run with `LLMCalls == 0`, < 2 s | integration | `internal/rules` | FR-008, NFR-002 |
| Provider round-trips incl. tool calls | integration | `internal/llm` | FR-010 |
| Role behaviour against scripted mock | unit | `internal/agent` | FR-009 |
| Both topologies produce valid reports; `-race` | integration | `internal/orchestrator` | FR-009 |
| Report golden files (md + json, en + zh) | unit | `internal/report` | FR-011 |
| Run store round-trip; replay without network | integration | `store`, `service` | FR-012 |
| Degradation: every source down ⇒ run completes with gaps | integration | `service` | FR-013 |
| End-to-end diagnose < 5 s with mock | integration | `service` | NFR-001 |
| Determinism: two identical runs ⇒ identical report | integration | `service` | NFR-010 |
| CLI smoke for every subcommand | integration | `cli` | FR-014 |
| API tests for every endpoint | integration | `httpapi` | FR-015 |
| Doctor against stubs | integration | `service` | FR-018 |
| Pack-only middleware addition | integration | `knowledge` | NFR-007 |
| Image builds, non-root, runs diagnose | CI | delivery | FR-020, NFR-005 |
| `sddctl verify` — parity, traceability, coverage | CI | repo | NFR-009, FR-017 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-23 | Initial low-level design | `tasks.md`, code |
