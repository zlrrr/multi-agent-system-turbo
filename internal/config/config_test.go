package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mas.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimalYAML = `
version: "1"
log:
  level: warn
llm:
  provider: openai
  model: gpt-4o
  base_url: https://api.example.com/v1
telemetry:
  metrics:
    - name: primary
      url: http://prom:9090
envs:
  prod:
    type: kubernetes
    namespace: middleware
targets:
  - id: redis-prod
    kind: redis
    env: prod
    selector: app=redis
`

// TestPrecedence proves defaults → file → env → overrides, each stage winning
// over the last (HLD §7.4).
func TestPrecedence(t *testing.T) {
	path := writeFile(t, minimalYAML)

	base, err := Load(nil, []string{}, nil)
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if base.Log.Level != "info" || base.LLM.Provider != "mock" {
		t.Fatalf("defaults wrong: %+v", base.Log)
	}

	fromFile, err := Load([]string{path}, []string{}, nil)
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if fromFile.Log.Level != "warn" || fromFile.LLM.Provider != "openai" {
		t.Fatalf("file did not override defaults: %+v", fromFile.LLM)
	}
	if fromFile.LLM.MaxTokens != 4096 {
		t.Fatalf("unset field lost its default: %d", fromFile.LLM.MaxTokens)
	}

	withEnv, err := Load([]string{path}, []string{"MAS_LOG_LEVEL=debug", "MAS_LLM_MODEL=env-model"}, nil)
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	if withEnv.Log.Level != "debug" || withEnv.LLM.Model != "env-model" {
		t.Fatalf("env did not override file: %+v", withEnv.LLM)
	}

	withFlags, err := Load([]string{path},
		[]string{"MAS_LOG_LEVEL=debug", "MAS_LLM_MODEL=env-model"},
		map[string]string{"log.level": "error", "llm.model": "flag-model"})
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if withFlags.Log.Level != "error" || withFlags.LLM.Model != "flag-model" {
		t.Fatalf("flags did not override env: %+v", withFlags.LLM)
	}
}

func TestUnknownFieldIsCoded(t *testing.T) {
	path := writeFile(t, "version: \"1\"\nnot_a_field: 3\n")
	_, err := Load([]string{path}, []string{}, nil)
	if errs.CodeOf(err) != "MAS-1002" {
		t.Fatalf("got %v (code %s), want MAS-1002", err, errs.CodeOf(err))
	}
}

func TestMissingFileIsCoded(t *testing.T) {
	_, err := Load([]string{"/definitely/not/here.yaml"}, []string{}, nil)
	if errs.CodeOf(err) != "MAS-1004" {
		t.Fatalf("got %v, want MAS-1004", err)
	}
}

func TestMalformedYAMLIsCoded(t *testing.T) {
	path := writeFile(t, "version: \"1\"\n  bad indent: [\n")
	_, err := Load([]string{path}, []string{}, nil)
	if errs.CodeOf(err) != "MAS-1001" {
		t.Fatalf("got %v, want MAS-1001", err)
	}
}

func TestUnknownOverridePathIsCoded(t *testing.T) {
	_, err := Load(nil, []string{}, map[string]string{"nope.nope": "x"})
	if errs.CodeOf(err) != "MAS-1002" {
		t.Fatalf("got %v, want MAS-1002", err)
	}
}

// TestValidateCodes walks every validation branch that an operator can trip.
func TestValidateCodes(t *testing.T) {
	cases := map[string]func(*Config){
		"bad log level":       func(c *Config) { c.Log.Level = "chatty" },
		"bad log format":      func(c *Config) { c.Log.Format = "xml" },
		"bad provider":        func(c *Config) { c.LLM.Provider = "gemini" },
		"empty provider":      func(c *Config) { c.LLM.Provider = "" },
		"bad base url":        func(c *Config) { c.LLM.BaseURL = "not-a-url" },
		"metrics without url": func(c *Config) { c.Telemetry.Metrics = []MetricsSource{{Name: "a"}} },
		"metrics bad url":     func(c *Config) { c.Telemetry.Metrics = []MetricsSource{{Name: "a", URL: "ftp://x"}} },
		"duplicate metrics": func(c *Config) {
			c.Telemetry.Metrics = []MetricsSource{{Name: "a", URL: "http://x"}, {Name: "a", URL: "http://y"}}
		},
		"logs bad type": func(c *Config) {
			c.Telemetry.Logs = []LogsSource{{Name: "l", URL: "http://x", Type: "splunk"}}
		},
		"bearer without token": func(c *Config) {
			c.Telemetry.Metrics = []MetricsSource{{Name: "a", URL: "http://x", Auth: AuthConfig{Type: "bearer"}}}
		},
		"bad auth type": func(c *Config) {
			c.Telemetry.Metrics = []MetricsSource{{Name: "a", URL: "http://x", Auth: AuthConfig{Type: "magic"}}}
		},
		"env bad type":        func(c *Config) { c.Envs = map[string]EnvConfig{"e": {Type: "vm"}} },
		"target without id":   func(c *Config) { c.Targets = []TargetConfig{{Kind: "redis"}} },
		"target without kind": func(c *Config) { c.Targets = []TargetConfig{{ID: "t"}} },
		"duplicate target": func(c *Config) {
			c.Targets = []TargetConfig{{ID: "t", Kind: "redis"}, {ID: "t", Kind: "kafka"}}
		},
		"unknown env ref":      func(c *Config) { c.Targets = []TargetConfig{{ID: "t", Kind: "redis", Env: "ghost"}} },
		"unknown metrics ref":  func(c *Config) { c.Targets = []TargetConfig{{ID: "t", Kind: "redis", MetricsSource: "ghost"}} },
		"bad port":             func(c *Config) { c.Targets = []TargetConfig{{ID: "t", Kind: "redis", Port: 70000}} },
		"bad mode":             func(c *Config) { c.Run.DefaultMode = "hybrid" },
		"bad short circuit":    func(c *Config) { c.Run.DeterministicShortCircuit = 2 },
		"bad language":         func(c *Config) { c.Run.Language = "fr" },
		"negative budget":      func(c *Config) { c.Run.Budget.MaxSteps = -1 },
		"bad store type":       func(c *Config) { c.Store.Type = "s3" },
		"fs store without dir": func(c *Config) { c.Store.Type = "fs"; c.Store.Dir = "" },
		"negative ceiling":     func(c *Config) { c.Safety.MaxResponseBytes = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default()
			mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if errs.CodeOf(err) != "MAS-1003" {
				t.Fatalf("got %v (code %s), want MAS-1003", err, errs.CodeOf(err))
			}
		})
	}
}

func TestValidateAcceptsRealisticConfig(t *testing.T) {
	c, err := Load([]string{writeFile(t, minimalYAML)}, []string{}, nil)
	if err != nil {
		t.Fatalf("realistic config rejected: %v", err)
	}
	if got := c.Telemetry.Metrics[0].MaxSamples; got != 11000 {
		t.Errorf("normalise did not apply max_samples default: %d", got)
	}
	if got := c.Telemetry.Metrics[0].Timeout.D(); got != 15*time.Second {
		t.Errorf("normalise did not apply timeout default: %s", got)
	}
}

// TestSecretNeverSerialises is the FR-016 boundary test for configuration.
func TestSecretNeverSerialises(t *testing.T) {
	const plaintext = "sk-super-secret-value"
	c := Default()
	c.LLM.APIKey = Secret(plaintext)
	c.Telemetry.Metrics = []MetricsSource{{
		Name: "a", URL: "http://x", Auth: AuthConfig{Type: "bearer", Token: Secret(plaintext)},
	}}

	jb, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	yb, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	dump, err := c.Dump()
	if err != nil {
		t.Fatal(err)
	}
	for name, blob := range map[string]string{
		"json": string(jb), "yaml": string(yb), "dump": string(dump),
		"fmt %v": fmt.Sprintf("%v", c.LLM.APIKey),
		// Deliberately exercising the %s path rather than calling String():
		// the point is that formatting a Secret cannot leak it.
		"fmt %s":     fmt.Sprintf("%s", c.LLM.APIKey), //nolint:staticcheck // S1025: the fmt path is what is under test
		"fmt %#v":    fmt.Sprintf("%#v", c.LLM.APIKey),
		"struct %+v": fmt.Sprintf("%+v", c.LLM),
	} {
		if strings.Contains(blob, plaintext) {
			t.Errorf("%s leaked the secret: %s", name, blob)
		}
	}
}

func TestResolveRefs(t *testing.T) {
	t.Setenv("MAS_TEST_SECRET", "from-env")
	if got, err := Secret("${env:MAS_TEST_SECRET}").Reveal(); err != nil || got != "from-env" {
		t.Fatalf("env ref: got %q, %v", got, err)
	}

	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Secret("${file:" + p + "}").Reveal(); err != nil || got != "from-file" {
		t.Fatalf("file ref: got %q, %v", got, err)
	}

	if got, err := Secret("literal").Reveal(); err != nil || got != "literal" {
		t.Fatalf("literal: got %q, %v", got, err)
	}

	if _, err := Secret("${env:MAS_NOT_SET_ANYWHERE}").Reveal(); errs.CodeOf(err) != "MAS-1006" {
		t.Fatalf("missing env: got %v, want MAS-1006", err)
	}
	if _, err := Secret("${file:/no/such/file}").Reveal(); errs.CodeOf(err) != "MAS-1006" {
		t.Fatalf("missing file: got %v, want MAS-1006", err)
	}
}

func TestTargetLookup(t *testing.T) {
	c, err := Load([]string{writeFile(t, minimalYAML)}, []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Target("redis-prod"); err != nil {
		t.Fatalf("known target not found: %v", err)
	}
	if _, err := c.Target("ghost"); errs.CodeOf(err) != "MAS-1005" {
		t.Fatalf("got %v, want MAS-1005", err)
	}
}

func TestSourceLookup(t *testing.T) {
	c, err := Load([]string{writeFile(t, minimalYAML)}, []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := c.MetricsSourceFor(""); err != nil || m.Name != "primary" {
		t.Fatalf("default metrics source: %+v %v", m, err)
	}
	if _, err := c.MetricsSourceFor("ghost"); errs.CodeOf(err) != "MAS-1012" {
		t.Fatalf("got %v, want MAS-1012", err)
	}
	if _, err := c.LogsSourceFor(""); errs.CodeOf(err) != "MAS-1012" {
		t.Fatalf("no logs configured should be MAS-1012, got %v", err)
	}
}

func TestDurationParsing(t *testing.T) {
	path := writeFile(t, "version: \"1\"\nllm:\n  timeout: 90s\nrun:\n  budget:\n    max_wall: 3m\n")
	c, err := Load([]string{path}, []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.LLM.Timeout.D() != 90*time.Second {
		t.Errorf("llm.timeout = %s", c.LLM.Timeout)
	}
	if c.Run.Budget.MaxWall.D() != 3*time.Minute {
		t.Errorf("max_wall = %s", c.Run.Budget.MaxWall)
	}
}

func TestEnvDurationAndIntOverrides(t *testing.T) {
	c, err := Load(nil, []string{"MAS_RUN_MAX_STEPS=7", "MAS_RUN_MAX_WALL=90s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Run.Budget.MaxSteps != 7 || c.Run.Budget.MaxWall.D() != 90*time.Second {
		t.Fatalf("env budget overrides not applied: %+v", c.Run.Budget)
	}
	if _, err := Load(nil, []string{"MAS_RUN_MAX_STEPS=lots"}, nil); errs.CodeOf(err) != "MAS-1003" {
		t.Fatalf("bad int should be MAS-1003, got %v", err)
	}
}

func TestEnvKeysAreSortedAndComplete(t *testing.T) {
	keys := EnvKeys()
	if len(keys) != len(envBindings) {
		t.Fatalf("EnvKeys returned %d, want %d", len(keys), len(envBindings))
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("EnvKeys not sorted at %d", i)
		}
	}
}
