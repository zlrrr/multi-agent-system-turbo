package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// knownKinds is the set of middleware kinds recognised without a knowledge pack
// present. Unknown kinds are allowed (G2.1 — a pack may introduce one) but are
// reported by `mas doctor` when no pack matches.
var knownKinds = map[string]bool{
	"redis": true, "kafka": true, "mongodb": true, "pulsar": true,
	"milvus": true, "oceanbase": true, "elasticsearch": true, "etcd": true,
	"rabbitmq": true, "postgresql": true, "mysql": true, "clickhouse": true,
	"nacos": true, "zookeeper": true, "minio": true,
}

// Validate checks the whole configuration and reports every problem it finds,
// each path-qualified so an operator can fix it without guessing (LLD §2.3).
func (c *Config) Validate() error {
	var problems []string
	bad := func(path, msg string) { problems = append(problems, path+": "+msg) }

	seenToken := map[string]bool{}
	for i, t := range c.Server.Auth.Tokens {
		path := fmt.Sprintf("server.auth.tokens[%d]", i)
		switch {
		case strings.TrimSpace(t.Name) == "":
			bad(path+".name", "must be set: it is the principal an audit line names")
		case seenToken[t.Name]:
			bad(path+".name", fmt.Sprintf("duplicate token name %q", t.Name))
		}
		seenToken[t.Name] = true
		if t.Token.IsZero() {
			bad(path+".token", "must be set")
		}
		if len(t.Scopes) == 0 {
			problems = append(problems, errs.New("MAS-7013", t.Name,
				"declares no scopes, so it can do nothing").Error())
		}
		for _, sc := range t.Scopes {
			if !APIScopes[sc] {
				problems = append(problems, errs.New("MAS-7013", t.Name,
					fmt.Sprintf("scope %q is not one of read, diagnose", sc)).Error())
			}
		}
	}
	if (c.Server.TLS.CertFile == "") != (c.Server.TLS.KeyFile == "") {
		bad("server.tls", "cert_file and key_file must be set together")
	}

	switch c.Log.Level {
	case "", "debug", "info", "warn", "error":
	default:
		bad("log.level", fmt.Sprintf("%q is not one of debug, info, warn, error", c.Log.Level))
	}
	switch c.Log.Format {
	case "", "json", "text":
	default:
		bad("log.format", fmt.Sprintf("%q is not one of json, text", c.Log.Format))
	}

	switch c.LLM.Provider {
	case "mock", "anthropic", "openai":
	case "":
		bad("llm.provider", "must be set")
	default:
		bad("llm.provider", fmt.Sprintf("%q is not one of mock, anthropic, openai", c.LLM.Provider))
	}
	if c.LLM.MaxTokens < 0 {
		bad("llm.max_tokens", "must not be negative")
	}
	if c.LLM.BaseURL != "" {
		if err := checkURL(c.LLM.BaseURL); err != nil {
			bad("llm.base_url", err.Error())
		}
	}

	metricNames := map[string]bool{}
	for i, m := range c.Telemetry.Metrics {
		p := fmt.Sprintf("telemetry.metrics[%d]", i)
		if m.URL == "" {
			bad(p+".url", "must be set")
		} else if err := checkURL(m.URL); err != nil {
			bad(p+".url", err.Error())
		}
		switch m.Type {
		case "", "prometheus", "victoriametrics", "thanos", "mimir":
		default:
			bad(p+".type", fmt.Sprintf("%q is not a Prometheus-compatible type", m.Type))
		}
		if metricNames[m.Name] {
			bad(p+".name", fmt.Sprintf("duplicate metrics source name %q", m.Name))
		}
		metricNames[m.Name] = true
		checkAuth(p+".auth", m.Auth, bad)
	}

	logNames := map[string]bool{}
	for i, l := range c.Telemetry.Logs {
		p := fmt.Sprintf("telemetry.logs[%d]", i)
		if l.URL == "" {
			bad(p+".url", "must be set")
		} else if err := checkURL(l.URL); err != nil {
			bad(p+".url", err.Error())
		}
		if l.Type != "" && l.Type != "loki" {
			bad(p+".type", fmt.Sprintf("%q is not supported; only loki", l.Type))
		}
		if logNames[l.Name] {
			bad(p+".name", fmt.Sprintf("duplicate log source name %q", l.Name))
		}
		logNames[l.Name] = true
		checkAuth(p+".auth", l.Auth, bad)
	}

	for name, e := range c.Envs {
		p := fmt.Sprintf("envs.%s", name)
		switch e.Type {
		case "kubernetes", "local":
		case "":
			bad(p+".type", "must be kubernetes or local")
		default:
			bad(p+".type", fmt.Sprintf("%q is not one of kubernetes, local", e.Type))
		}
		if e.APIServer != "" {
			if err := checkURL(e.APIServer); err != nil {
				bad(p+".api_server", err.Error())
			}
		}
	}

	ids := map[string]bool{}
	for i, t := range c.Targets {
		p := fmt.Sprintf("targets[%d]", i)
		if t.ID == "" {
			bad(p+".id", "must be set")
		}
		if ids[t.ID] {
			bad(p+".id", fmt.Sprintf("duplicate target id %q", t.ID))
		}
		ids[t.ID] = true
		if t.Kind == "" {
			bad(p+".kind", "must be set")
		} else if !knownKinds[t.Kind] && strings.ContainsAny(t.Kind, " /\\") {
			bad(p+".kind", fmt.Sprintf("%q is not a valid middleware kind", t.Kind))
		}
		if t.Env != "" {
			if _, ok := c.Envs[t.Env]; !ok {
				problems = append(problems, fmt.Sprintf("%s.env: %s", p,
					errs.New("MAS-1008", t.ID, t.Env).Message("en")))
			}
		}
		if t.MetricsSource != "" && !metricNames[t.MetricsSource] {
			bad(p+".metrics_source", fmt.Sprintf("unknown metrics source %q", t.MetricsSource))
		}
		if t.LogsSource != "" && !logNames[t.LogsSource] {
			bad(p+".logs_source", fmt.Sprintf("unknown log source %q", t.LogsSource))
		}
		if t.Port < 0 || t.Port > 65535 {
			bad(p+".port", "must be within 0..65535")
		}
	}

	switch c.Run.DefaultMode {
	case "", "offline", "online":
	default:
		bad("run.default_mode", fmt.Sprintf("%q is not one of offline, online", c.Run.DefaultMode))
	}
	if c.Run.DeterministicShortCircuit < 0 || c.Run.DeterministicShortCircuit > 1 {
		bad("run.deterministic_short_circuit", "must be within 0..1")
	}
	switch c.Run.Language {
	case "", "en", "zh":
	default:
		bad("run.language", fmt.Sprintf("%q is not one of en, zh", c.Run.Language))
	}
	if c.Run.Budget.MaxSteps < 0 || c.Run.Budget.MaxToolCalls < 0 || c.Run.Budget.MaxTokens < 0 {
		bad("run.budget", "budget values must not be negative")
	}
	if c.Run.Budget.MaxWall < 0 {
		bad("run.budget.max_wall", "must not be negative")
	}

	switch c.Store.Type {
	case "", "fs", "memory":
	default:
		bad("store.type", fmt.Sprintf("%q is not one of fs, memory", c.Store.Type))
	}
	if c.Store.Type == "fs" && c.Store.Dir == "" {
		bad("store.dir", "must be set when store.type is fs")
	}

	if c.Safety.MaxResponseBytes < 0 {
		bad("safety.max_response_bytes", "must not be negative")
	}
	if c.Safety.MaxTimeout < 0 {
		bad("safety.max_timeout", "must not be negative")
	}

	for kind, repo := range c.Source.Repos {
		if err := checkURL(repo); err != nil && !strings.HasPrefix(repo, "/") {
			bad("source.repos."+kind, err.Error())
		}
	}

	if len(problems) == 0 {
		return nil
	}
	first := problems[0]
	i := strings.Index(first, ": ")
	path, msg := first[:i], first[i+2:]
	if len(problems) > 1 {
		msg = fmt.Sprintf("%s (and %d more: %s)", msg, len(problems)-1, strings.Join(problems[1:], "; "))
	}
	return errs.New("MAS-1003", path, msg)
}

func checkAuth(path string, a AuthConfig, bad func(string, string)) {
	switch a.Type {
	case "", "none":
	case "bearer":
		if a.Token.IsZero() {
			bad(path+".token", "must be set when auth.type is bearer")
		}
	case "basic":
		if a.Username == "" {
			bad(path+".username", "must be set when auth.type is basic")
		}
	case "header":
		if a.Header == "" {
			bad(path+".header", "must be set when auth.type is header")
		}
	default:
		bad(path+".type", fmt.Sprintf("%q is not one of none, bearer, basic, header", a.Type))
	}
}

func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a valid URL", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q must use http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	return nil
}
