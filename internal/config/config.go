// Package config defines the configuration model and its precedence rules.
//
// Governs: specs/001-mvp-core/design-lld.md §2.3, design-hld.md §7.4
package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that reads and writes Go duration strings in YAML
// and JSON ("15s", "5m"), so the configuration file stays human-readable.
type Duration time.Duration

// D returns the underlying time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		var i int64
		if err2 := n.Decode(&i); err2 == nil {
			*d = Duration(time.Duration(i) * time.Second)
			return nil
		}
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// Config is the complete runtime configuration.
type Config struct {
	Version   string               `yaml:"version" json:"version"`
	Log       LogConfig            `yaml:"log" json:"log"`
	LLM       LLMConfig            `yaml:"llm" json:"llm"`
	Telemetry TelemetryConfig      `yaml:"telemetry" json:"telemetry"`
	Envs      map[string]EnvConfig `yaml:"envs" json:"envs"`
	Targets   []TargetConfig       `yaml:"targets" json:"targets"`
	Knowledge KnowledgeConfig      `yaml:"knowledge" json:"knowledge"`
	Source    SourceConfig         `yaml:"source" json:"source"`
	Run       RunConfig            `yaml:"run" json:"run"`
	Store     StoreConfig          `yaml:"store" json:"store"`
	Server    ServerConfig         `yaml:"server" json:"server"`
	Safety    SafetyConfig         `yaml:"safety" json:"safety"`
}

// LogConfig controls structured logging (HLD §7.2).
type LogConfig struct {
	Level  string   `yaml:"level" json:"level"`   // debug | info | warn | error
	Format string   `yaml:"format" json:"format"` // json | text
	Redact []string `yaml:"redact" json:"redact"` // extra regexes to redact
}

// AgentModel overrides the model used by one agent role (G8.2).
type AgentModel struct {
	// Provider names an entry in LLMConfig.Providers. Empty means the run's
	// default provider — a role that overrides only the temperature must not
	// lose the endpoint and the key that go with it.
	Provider    string  `yaml:"provider" json:"provider"`
	Model       string  `yaml:"model" json:"model"`
	Temperature float64 `yaml:"temperature" json:"temperature"`
}

// ModelPrice is what one model costs, per million tokens.
//
// This project ships no prices. They change, differ by contract and region, and
// a stale number that looks authoritative is a false claim — so they are
// configuration, and a model with no entry makes the run's cost unknown rather
// than zero.
type ModelPrice struct {
	InputPerMTok  float64 `yaml:"input_per_mtok" json:"input_per_mtok"`
	OutputPerMTok float64 `yaml:"output_per_mtok" json:"output_per_mtok"`
}

// LLMConfig selects and configures the model provider.
type LLMConfig struct {
	Provider    string                `yaml:"provider" json:"provider"` // mock | anthropic | openai
	Model       string                `yaml:"model" json:"model"`
	APIKey      Secret                `yaml:"api_key" json:"api_key"`
	BaseURL     string                `yaml:"base_url" json:"base_url"`
	Timeout     Duration              `yaml:"timeout" json:"timeout"`
	MaxTokens   int                   `yaml:"max_tokens" json:"max_tokens"`
	Temperature float64               `yaml:"temperature" json:"temperature"`
	MockScript  string                `yaml:"mock_script" json:"mock_script"` // path, mock provider only
	PerAgent    map[string]AgentModel `yaml:"per_agent" json:"per_agent"`

	// Providers are named alternatives a role may be routed to. Each inherits
	// every field of the default it does not set.
	Providers map[string]ProviderConfig `yaml:"providers" json:"providers"`

	// Pricing is the operator's price list, keyed by model.
	Pricing map[string]ModelPrice `yaml:"pricing" json:"pricing"`
}

// ProviderConfig is a named alternative provider. Every field is optional: what
// it does not set, it inherits from the run's default provider.
type ProviderConfig struct {
	Provider    string   `yaml:"provider" json:"provider"`
	Model       string   `yaml:"model" json:"model"`
	APIKey      Secret   `yaml:"api_key" json:"api_key"`
	BaseURL     string   `yaml:"base_url" json:"base_url"`
	Timeout     Duration `yaml:"timeout" json:"timeout"`
	MaxTokens   int      `yaml:"max_tokens" json:"max_tokens"`
	Temperature float64  `yaml:"temperature" json:"temperature"`
	MockScript  string   `yaml:"mock_script" json:"mock_script"`
}

// AuthConfig describes how to authenticate against a telemetry backend.
type AuthConfig struct {
	Type     string `yaml:"type" json:"type"` // none | bearer | basic | header
	Token    Secret `yaml:"token" json:"token"`
	Username string `yaml:"username" json:"username"`
	Password Secret `yaml:"password" json:"password"`
	Header   string `yaml:"header" json:"header"`
}

// MetricsSource is one Prometheus-compatible endpoint.
type MetricsSource struct {
	Name       string            `yaml:"name" json:"name"`
	Type       string            `yaml:"type" json:"type"` // prometheus | victoriametrics
	URL        string            `yaml:"url" json:"url"`
	Auth       AuthConfig        `yaml:"auth" json:"auth"`
	Timeout    Duration          `yaml:"timeout" json:"timeout"`
	MaxSamples int               `yaml:"max_samples" json:"max_samples"`
	Headers    map[string]string `yaml:"headers" json:"headers"`
	TLSSkip    bool              `yaml:"tls_insecure_skip_verify" json:"tls_insecure_skip_verify"`
}

// LogsSource is one Loki-compatible endpoint.
type LogsSource struct {
	Name     string            `yaml:"name" json:"name"`
	Type     string            `yaml:"type" json:"type"` // loki
	URL      string            `yaml:"url" json:"url"`
	Auth     AuthConfig        `yaml:"auth" json:"auth"`
	Timeout  Duration          `yaml:"timeout" json:"timeout"`
	MaxLines int               `yaml:"max_lines" json:"max_lines"`
	TenantID string            `yaml:"tenant_id" json:"tenant_id"`
	Headers  map[string]string `yaml:"headers" json:"headers"`
	TLSSkip  bool              `yaml:"tls_insecure_skip_verify" json:"tls_insecure_skip_verify"`
}

// TelemetryConfig groups the observability backends the tool reads from.
type TelemetryConfig struct {
	Metrics []MetricsSource `yaml:"metrics" json:"metrics"`
	Logs    []LogsSource    `yaml:"logs" json:"logs"`
}

// EnvConfig binds a logical environment name to a concrete adapter.
type EnvConfig struct {
	Type       string   `yaml:"type" json:"type"` // kubernetes | local
	Kubeconfig string   `yaml:"kubeconfig" json:"kubeconfig"`
	Context    string   `yaml:"context" json:"context"`
	Namespace  string   `yaml:"namespace" json:"namespace"`
	APIServer  string   `yaml:"api_server" json:"api_server"`
	Token      Secret   `yaml:"token" json:"token"`
	CAFile     string   `yaml:"ca_file" json:"ca_file"`
	TLSSkip    bool     `yaml:"tls_insecure_skip_verify" json:"tls_insecure_skip_verify"`
	Timeout    Duration `yaml:"timeout" json:"timeout"`

	// Exec turns in-container execution off for this environment. It is a
	// pointer so that "unset" and "false" are distinguishable: unset means the
	// guard decides per command, which is the default.
	//
	// The switch can only narrow. There is deliberately no key that widens what
	// may run — Constitution Art. IV.2 — so setting it to true grants nothing
	// beyond the allow-list that already applies.
	Exec *bool `yaml:"exec" json:"exec,omitempty"`
}

// ExecEnabled reports whether in-container execution may be offered for this
// environment. The guard still decides every individual command.
func (e EnvConfig) ExecEnabled() bool { return e.Exec == nil || *e.Exec }

// TargetConfig declares a middleware deployment that may be diagnosed.
type TargetConfig struct {
	ID            string            `yaml:"id" json:"id"`
	Kind          string            `yaml:"kind" json:"kind"`
	Env           string            `yaml:"env" json:"env"`
	Version       string            `yaml:"version" json:"version"`
	Namespace     string            `yaml:"namespace" json:"namespace"`
	Selector      string            `yaml:"selector" json:"selector"`
	Labels        map[string]string `yaml:"labels" json:"labels"`
	MetricsSource string            `yaml:"metrics_source" json:"metrics_source"`
	LogsSource    string            `yaml:"logs_source" json:"logs_source"`
	LogQuery      string            `yaml:"log_query" json:"log_query"`
	Hosts         []string          `yaml:"hosts" json:"hosts"`
	Port          int               `yaml:"port" json:"port"`
}

// KnowledgeConfig points at additional knowledge-pack directories.
type KnowledgeConfig struct {
	PackDirs []string `yaml:"pack_dirs" json:"pack_dirs"`
}

// SourceConfig configures source acquisition and its local fallback (G6).
type SourceConfig struct {
	Enabled        bool              `yaml:"enabled" json:"enabled"`
	CacheDir       string            `yaml:"cache_dir" json:"cache_dir"`
	NetworkTimeout Duration          `yaml:"network_timeout" json:"network_timeout"`
	CacheTTL       Duration          `yaml:"cache_ttl" json:"cache_ttl"`
	Repos          map[string]string `yaml:"repos" json:"repos"`
	Mirrors        map[string]string `yaml:"mirrors" json:"mirrors"`
}

// BudgetConfig caps one run.
type BudgetConfig struct {
	MaxSteps     int      `yaml:"max_steps" json:"max_steps"`
	MaxToolCalls int      `yaml:"max_tool_calls" json:"max_tool_calls"`
	MaxTokens    int      `yaml:"max_tokens" json:"max_tokens"`
	MaxWall      Duration `yaml:"max_wall" json:"max_wall"`
}

// RunConfig sets run-level defaults.
type RunConfig struct {
	DefaultTopology           string       `yaml:"default_topology" json:"default_topology"`
	DefaultMode               string       `yaml:"default_mode" json:"default_mode"`
	DefaultWindow             Duration     `yaml:"default_window" json:"default_window"`
	DeterministicShortCircuit float64      `yaml:"deterministic_short_circuit" json:"deterministic_short_circuit"`
	Language                  string       `yaml:"language" json:"language"`
	Budget                    BudgetConfig `yaml:"budget" json:"budget"`
	MaxConcurrency            int          `yaml:"max_concurrency" json:"max_concurrency"`
}

// StoreConfig selects where run records live.
type StoreConfig struct {
	Type string   `yaml:"type" json:"type"` // fs | memory | s3
	Dir  string   `yaml:"dir" json:"dir"`
	S3   S3Config `yaml:"s3" json:"s3"`
}

// S3Config points the run store at an S3-compatible bucket: AWS S3, MinIO,
// Ceph RGW, or anything else speaking the same subset. It is what makes the
// audit trail shared between replicas rather than private to one pod's disk.
type S3Config struct {
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	Region   string `yaml:"region" json:"region"`
	Bucket   string `yaml:"bucket" json:"bucket"`
	Prefix   string `yaml:"prefix" json:"prefix"`
	// Credentials come from configuration only. Instance metadata and ~/.aws
	// would be two more code paths and two more ways to be surprised about
	// which identity is in use.
	AccessKeyID     Secret `yaml:"access_key_id" json:"access_key_id"`
	SecretAccessKey Secret `yaml:"secret_access_key" json:"secret_access_key"`
	// PathStyle puts the bucket in the path rather than the host, which is what
	// self-hosted deployments almost always need.
	PathStyle bool     `yaml:"path_style" json:"path_style"`
	Timeout   Duration `yaml:"timeout" json:"timeout"`
}

// Configured reports whether an object store has been asked for.
func (c S3Config) Configured() bool { return strings.TrimSpace(c.Endpoint) != "" }

// ServerConfig configures the HTTP surface.
type ServerConfig struct {
	Addr         string     `yaml:"addr" json:"addr"`
	ReadTimeout  Duration   `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout Duration   `yaml:"write_timeout" json:"write_timeout"`
	Auth         ServerAuth `yaml:"auth" json:"auth"`
	TLS          ServerTLS  `yaml:"tls" json:"tls"`
}

// ServerAuth lists the credentials the API accepts. An empty list means the
// API is unauthenticated, which `httpapi.Admit` permits only on a loopback
// bind (specs/009-api-authentication/design-hld.md §2).
type ServerAuth struct {
	Tokens []APIToken `yaml:"tokens" json:"tokens"`
}

// APIToken is one bearer credential and what it may do.
type APIToken struct {
	// Name is the principal: what an audit line names and what a run record
	// records as the caller.
	Name   string   `yaml:"name" json:"name"`
	Token  Secret   `yaml:"token" json:"token"`
	Scopes []string `yaml:"scopes" json:"scopes"`
}

// ServerTLS describes how the API is protected on the wire.
type ServerTLS struct {
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
	// TerminatedByProxy records a fact the tool cannot verify: that something
	// in front of this process terminates TLS. It is an operator's statement,
	// which is why it has to be typed rather than inferred — and why serving
	// credentials in plaintext off-host is refused without it.
	TerminatedByProxy bool `yaml:"terminated_by_proxy" json:"terminated_by_proxy"`
}

// Enabled reports whether the server serves TLS itself.
func (t ServerTLS) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

// APIScopes are the scopes a token may hold. Deny by default: a scope this
// build does not recognise fails at load rather than being ignored, because an
// ignored scope is an authorisation the operator believes they granted.
var APIScopes = map[string]bool{"read": true, "diagnose": true}

// SafetyConfig may only narrow the guard, never widen it (HLD §7.3.4).
type SafetyConfig struct {
	ExtraDeniedArgs     []string `yaml:"extra_denied_args" json:"extra_denied_args"`
	ExtraDeniedBinaries []string `yaml:"extra_denied_binaries" json:"extra_denied_binaries"`
	MaxResponseBytes    int      `yaml:"max_response_bytes" json:"max_response_bytes"`
	MaxTimeout          Duration `yaml:"max_timeout" json:"max_timeout"`
}

// Default returns the built-in configuration, which is the base of the
// precedence chain (defaults → file → env → flags).
func Default() *Config {
	return &Config{
		Version: "1",
		Log:     LogConfig{Level: "info", Format: "json"},
		LLM: LLMConfig{
			Provider: "mock", Model: "mock-1",
			Timeout: Duration(60 * time.Second), MaxTokens: 4096, Temperature: 0,
		},
		Envs:      map[string]EnvConfig{},
		Knowledge: KnowledgeConfig{},
		Source: SourceConfig{
			Enabled: true, CacheDir: "", NetworkTimeout: Duration(10 * time.Second),
			CacheTTL: Duration(24 * time.Hour), Repos: map[string]string{}, Mirrors: map[string]string{},
		},
		Run: RunConfig{
			DefaultTopology: "supervisor", DefaultMode: "offline",
			DefaultWindow: Duration(time.Hour), DeterministicShortCircuit: 0.85, Language: "en",
			Budget: BudgetConfig{
				MaxSteps: 24, MaxToolCalls: 40, MaxTokens: 120000, MaxWall: Duration(5 * time.Minute),
			},
			MaxConcurrency: 4,
		},
		Store:  StoreConfig{Type: "fs", Dir: "runs"},
		Server: ServerConfig{Addr: ":8080", ReadTimeout: Duration(30 * time.Second), WriteTimeout: Duration(120 * time.Second)},
		Safety: SafetyConfig{MaxResponseBytes: 8 << 20, MaxTimeout: Duration(120 * time.Second)},
	}
}
