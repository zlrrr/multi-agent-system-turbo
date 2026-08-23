package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
	"gopkg.in/yaml.v3"
)

// SearchPaths lists where Load looks when no explicit path is given.
var SearchPaths = []string{"mas.yaml", "mas.yml", "/etc/mas/mas.yaml"}

// Load builds the effective configuration by applying, in order:
// built-in defaults → configuration file → MAS_* environment → explicit
// overrides (typically command-line flags). Later stages win (HLD §7.4).
//
// An empty paths slice searches SearchPaths and tolerates absence: a
// zero-configuration run is valid and uses defaults.
func Load(paths []string, env []string, overrides map[string]string) (*Config, error) {
	cfg := Default()

	file, err := resolveFile(paths)
	if err != nil {
		return nil, err
	}
	if file != "" {
		if err := applyFile(cfg, file); err != nil {
			return nil, err
		}
	}
	if err := applyEnv(cfg, env); err != nil {
		return nil, err
	}
	if err := applyOverrides(cfg, overrides); err != nil {
		return nil, err
	}
	normalise(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFile reads exactly one configuration file, failing if it is absent.
func LoadFile(path string) (*Config, error) { return Load([]string{path}, nil, nil) }

func resolveFile(paths []string) (string, error) {
	if len(paths) > 0 {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err != nil {
				return "", errs.Wrap(err, "MAS-1004", paths)
			}
			return p, nil
		}
	}
	for _, p := range SearchPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", nil
}

func applyFile(cfg *Config, path string) error {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return errs.Wrap(err, "MAS-1001", path)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true) // unknown keys are an error, not a silent typo
	if err := dec.Decode(cfg); err != nil {
		if strings.Contains(err.Error(), "field ") && strings.Contains(err.Error(), "not found") {
			return errs.Wrap(err, "MAS-1002", extractUnknownField(err.Error()))
		}
		return errs.Wrap(err, "MAS-1001", path)
	}
	return nil
}

func extractUnknownField(msg string) string {
	// yaml.v3 phrasing: `line 4: field foo not found in type config.Config`
	i := strings.Index(msg, "field ")
	if i < 0 {
		return msg
	}
	rest := msg[i+len("field "):]
	if j := strings.Index(rest, " "); j > 0 {
		return rest[:j]
	}
	return rest
}

// envBindings maps MAS_* variables to setters. The mapping is explicit rather
// than reflective so that every supported override is documented, testable and
// discoverable by `mas doctor`.
var envBindings = map[string]func(*Config, string) error{
	"MAS_LOG_LEVEL":       func(c *Config, v string) error { c.Log.Level = v; return nil },
	"MAS_LOG_FORMAT":      func(c *Config, v string) error { c.Log.Format = v; return nil },
	"MAS_LLM_PROVIDER":    func(c *Config, v string) error { c.LLM.Provider = v; return nil },
	"MAS_LLM_MODEL":       func(c *Config, v string) error { c.LLM.Model = v; return nil },
	"MAS_LLM_API_KEY":     func(c *Config, v string) error { c.LLM.APIKey = Secret(v); return nil },
	"MAS_LLM_BASE_URL":    func(c *Config, v string) error { c.LLM.BaseURL = v; return nil },
	"MAS_LLM_MOCK_SCRIPT": func(c *Config, v string) error { c.LLM.MockScript = v; return nil },
	"MAS_STORE_TYPE":      func(c *Config, v string) error { c.Store.Type = v; return nil },
	"MAS_STORE_DIR":       func(c *Config, v string) error { c.Store.Dir = v; return nil },
	"MAS_SERVER_ADDR":     func(c *Config, v string) error { c.Server.Addr = v; return nil },
	"MAS_RUN_TOPOLOGY":    func(c *Config, v string) error { c.Run.DefaultTopology = v; return nil },
	"MAS_RUN_MODE":        func(c *Config, v string) error { c.Run.DefaultMode = v; return nil },
	"MAS_RUN_LANGUAGE":    func(c *Config, v string) error { c.Run.Language = v; return nil },
	"MAS_SOURCE_CACHE_DIR": func(c *Config, v string) error {
		c.Source.CacheDir = v
		return nil
	},
	"MAS_KNOWLEDGE_PACK_DIRS": func(c *Config, v string) error {
		c.Knowledge.PackDirs = splitList(v)
		return nil
	},
	"MAS_METRICS_URL": func(c *Config, v string) error {
		ensureMetricsSource(c)
		c.Telemetry.Metrics[0].URL = v
		return nil
	},
	"MAS_LOGS_URL": func(c *Config, v string) error {
		ensureLogsSource(c)
		c.Telemetry.Logs[0].URL = v
		return nil
	},
	"MAS_RUN_MAX_STEPS": func(c *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return errs.New("MAS-1003", "MAS_RUN_MAX_STEPS", v+" is not an integer")
		}
		c.Run.Budget.MaxSteps = n
		return nil
	},
	"MAS_RUN_MAX_WALL": func(c *Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil {
			return errs.New("MAS-1003", "MAS_RUN_MAX_WALL", v+" is not a duration")
		}
		c.Run.Budget.MaxWall = Duration(d)
		return nil
	},
}

// EnvKeys lists every recognised environment override, for documentation and
// `mas doctor`.
func EnvKeys() []string {
	out := make([]string, 0, len(envBindings))
	for k := range envBindings {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func applyEnv(cfg *Config, env []string) error {
	if env == nil {
		env = os.Environ()
	}
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k, v := kv[:i], kv[i+1:]
		if !strings.HasPrefix(k, "MAS_") || v == "" {
			continue
		}
		set, ok := envBindings[k]
		if !ok {
			continue // unknown MAS_* variables are ignored, not fatal
		}
		if err := set(cfg, v); err != nil {
			return err
		}
	}
	return nil
}

// applyOverrides applies dotted-path overrides, the form command-line flags use.
func applyOverrides(cfg *Config, overrides map[string]string) error {
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		v := overrides[k]
		if v == "" {
			continue
		}
		if err := setPath(cfg, k, v); err != nil {
			return err
		}
	}
	return nil
}

func setPath(cfg *Config, path, v string) error {
	switch path {
	case "log.level":
		cfg.Log.Level = v
	case "log.format":
		cfg.Log.Format = v
	case "llm.provider":
		cfg.LLM.Provider = v
	case "llm.model":
		cfg.LLM.Model = v
	case "llm.base_url":
		cfg.LLM.BaseURL = v
	case "llm.mock_script":
		cfg.LLM.MockScript = v
	case "store.type":
		cfg.Store.Type = v
	case "store.dir":
		cfg.Store.Dir = v
	case "server.addr":
		cfg.Server.Addr = v
	case "run.default_topology":
		cfg.Run.DefaultTopology = v
	case "run.default_mode":
		cfg.Run.DefaultMode = v
	case "run.language":
		cfg.Run.Language = v
	case "telemetry.metrics.url":
		ensureMetricsSource(cfg)
		cfg.Telemetry.Metrics[0].URL = v
	case "telemetry.logs.url":
		ensureLogsSource(cfg)
		cfg.Telemetry.Logs[0].URL = v
	default:
		return errs.New("MAS-1002", path)
	}
	return nil
}

func ensureMetricsSource(c *Config) {
	if len(c.Telemetry.Metrics) == 0 {
		c.Telemetry.Metrics = []MetricsSource{{Name: "primary", Type: "prometheus"}}
	}
}

func ensureLogsSource(c *Config) {
	if len(c.Telemetry.Logs) == 0 {
		c.Telemetry.Logs = []LogsSource{{Name: "primary", Type: "loki"}}
	}
}

// normalise fills in derived defaults that depend on other fields.
func normalise(c *Config) {
	for i := range c.Telemetry.Metrics {
		m := &c.Telemetry.Metrics[i]
		if m.Name == "" {
			m.Name = fmt.Sprintf("metrics-%d", i)
		}
		if m.Type == "" {
			m.Type = "prometheus"
		}
		if m.Timeout == 0 {
			m.Timeout = Duration(15 * time.Second)
		}
		if m.MaxSamples == 0 {
			m.MaxSamples = 11000
		}
		m.URL = strings.TrimRight(m.URL, "/")
	}
	for i := range c.Telemetry.Logs {
		l := &c.Telemetry.Logs[i]
		if l.Name == "" {
			l.Name = fmt.Sprintf("logs-%d", i)
		}
		if l.Type == "" {
			l.Type = "loki"
		}
		if l.Timeout == 0 {
			l.Timeout = Duration(20 * time.Second)
		}
		if l.MaxLines == 0 {
			l.MaxLines = 1000
		}
		l.URL = strings.TrimRight(l.URL, "/")
	}
	for name, e := range c.Envs {
		if e.Timeout == 0 {
			e.Timeout = Duration(20 * time.Second)
		}
		if e.Type == "" {
			e.Type = "kubernetes"
		}
		c.Envs[name] = e
	}
	if c.Source.CacheDir == "" {
		c.Source.CacheDir = filepath.Join(os.TempDir(), "mas-src")
	}
	if c.Run.Language == "" {
		c.Run.Language = "en"
	}
	if c.Run.MaxConcurrency <= 0 {
		c.Run.MaxConcurrency = 4
	}
}

// Target resolves a target by id.
func (c *Config) Target(id string) (TargetConfig, error) {
	for _, t := range c.Targets {
		if t.ID == id {
			return t, nil
		}
	}
	return TargetConfig{}, errs.New("MAS-1005", id)
}

// TargetIDs lists configured target identifiers.
func (c *Config) TargetIDs() []string {
	out := make([]string, 0, len(c.Targets))
	for _, t := range c.Targets {
		out = append(out, t.ID)
	}
	return out
}

// MetricsSourceFor returns the named source, or the first configured one when
// name is empty.
func (c *Config) MetricsSourceFor(name string) (MetricsSource, error) {
	if name == "" {
		if len(c.Telemetry.Metrics) == 0 {
			return MetricsSource{}, errs.New("MAS-1012", "(none configured)")
		}
		return c.Telemetry.Metrics[0], nil
	}
	for _, m := range c.Telemetry.Metrics {
		if m.Name == name {
			return m, nil
		}
	}
	return MetricsSource{}, errs.New("MAS-1012", name)
}

// LogsSourceFor returns the named log source, or the first configured one.
func (c *Config) LogsSourceFor(name string) (LogsSource, error) {
	if name == "" {
		if len(c.Telemetry.Logs) == 0 {
			return LogsSource{}, errs.New("MAS-1012", "(none configured)")
		}
		return c.Telemetry.Logs[0], nil
	}
	for _, l := range c.Telemetry.Logs {
		if l.Name == name {
			return l, nil
		}
	}
	return LogsSource{}, errs.New("MAS-1012", name)
}

// Dump renders the configuration as YAML with every secret redacted. It is safe
// to print, log and attach to a bug report.
func (c *Config) Dump() ([]byte, error) { return yaml.Marshal(c) }

func splitList(v string) []string {
	parts := strings.Split(v, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
