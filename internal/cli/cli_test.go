package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/cli"
)

// stubTelemetry stands up a metrics and a log endpoint so CLI smoke tests
// exercise a real end-to-end path without a network.
func stubTelemetry(t *testing.T) (promURL, lokiURL string) {
	t.Helper()
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		q := r.Form.Get("query")
		value := "1"
		switch {
		case strings.Contains(q, "redis_memory_used_bytes"):
			value = "990"
		case strings.Contains(q, "redis_memory_max_bytes"):
			value = "1000"
		case strings.Contains(q, "evicted"):
			value = "9"
		}
		if strings.Contains(r.URL.Path, "query_range") {
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[
			  {"metric":{"instance":"redis-0"},"values":[[1724400000,%q],[1724400060,%q]]}]}}`, value, value)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[
		  {"metric":{"instance":"redis-0"},"value":[1724400000,%q]}]}}`, value)
	}))
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "label") {
			_, _ = w.Write([]byte(`{"status":"success","data":["job"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
		  {"stream":{"job":"redis"},"values":[["1724400000000000000","OOM command not allowed"]]}]}}`))
	}))
	t.Cleanup(func() { prom.Close(); loki.Close() })
	return prom.URL, loki.URL
}

// harness writes a working configuration and runs commands against it.
type harness struct {
	t          *testing.T
	configPath string
	storeDir   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	promURL, lokiURL := stubTelemetry(t)
	cfg := fmt.Sprintf(`
version: "1"
log: { level: error, format: text }
llm: { provider: mock, model: mock-1 }
telemetry:
  metrics:
    - { name: primary, type: prometheus, url: %s, timeout: 5s, max_samples: 100 }
  logs:
    - { name: primary, type: loki, url: %s, timeout: 5s, max_lines: 100 }
targets:
  - id: redis-prod
    kind: redis
    version: "7.2.4"
    labels: { instance: redis-0 }
run:
  default_topology: supervisor
  default_mode: offline
  deterministic_short_circuit: 0.85
source: { enabled: false }
store: { type: fs, dir: %s }
`, promURL, lokiURL, filepath.Join(dir, "runs"))
	p := filepath.Join(dir, "mas.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, configPath: p, storeDir: filepath.Join(dir, "runs")}
}

func (h *harness) run(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	var out, errOut bytes.Buffer
	full := append([]string{"--config", h.configPath}, args...)
	code = cli.Execute(full, &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestVersionCommand(t *testing.T) {
	h := newHarness(t)
	out, _, code := h.run("version")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "mas ") {
		t.Fatalf("out = %q", out)
	}
	jsonOut, _, _ := h.run("version", "--json")
	var info map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &info); err != nil {
		t.Fatalf("--json is not JSON: %v", err)
	}
	if info["go_version"] == "" {
		t.Errorf("version JSON = %v", info)
	}
}

func TestDiagnoseSmoke(t *testing.T) {
	h := newHarness(t)
	out, errOut, code := h.run("diagnose", "--target", "redis-prod",
		"--symptom", "p99 latency spike with evictions", "--since", "1h")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{"# Diagnostic report", "## Summary", "## Hypotheses",
		"## Gaps in the evidence", "## Recommended next steps", "read-only"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q", want)
		}
	}
}

func TestDiagnoseFormats(t *testing.T) {
	h := newHarness(t)
	for _, format := range []string{"markdown", "json", "text"} {
		out, errOut, code := h.run("diagnose", "--target", "redis-prod",
			"--symptom", "latency", "--since", "30m", "--format", format)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", format, code, errOut)
		}
		if format == "json" {
			var rep map[string]any
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("json format is not JSON: %v", err)
			}
			if rep["schema"] != "report/v1" {
				t.Errorf("schema = %v", rep["schema"])
			}
		}
	}
	if _, _, code := h.run("diagnose", "--target", "redis-prod",
		"--symptom", "x", "--format", "yaml"); code == 0 {
		t.Fatal("an unknown format was accepted")
	}
}

func TestDiagnoseWritesToFile(t *testing.T) {
	h := newHarness(t)
	out := filepath.Join(t.TempDir(), "nested", "report.md")
	stdout, errOut, code := h.run("diagnose", "--target", "redis-prod",
		"--symptom", "latency", "--since", "1h", "--output", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(stdout, "report written to") {
		t.Errorf("stdout = %q", stdout)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Diagnostic report") {
		t.Fatal("the written file is not a report")
	}
}

func TestDiagnoseChinese(t *testing.T) {
	h := newHarness(t)
	out, _, code := h.run("--lang", "zh", "diagnose", "--target", "redis-prod",
		"--symptom", "内存告警与驱逐", "--since", "1h")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"诊断报告", "结论摘要", "建议的后续动作"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Chinese report is missing %q", want)
		}
	}
}

func TestDiagnoseErrorsCarryCodesAndRemedies(t *testing.T) {
	h := newHarness(t)
	cases := map[string]struct {
		args []string
		code string
		exit int
	}{
		"unknown target":   {[]string{"diagnose", "--target", "ghost", "--symptom", "x"}, "MAS-1005", 2},
		"unknown topology": {[]string{"diagnose", "--target", "redis-prod", "--symptom", "x", "--topology", "no-such-topology"}, "MAS-3001", 5},
		"bad mode":         {[]string{"diagnose", "--target", "redis-prod", "--symptom", "x", "--mode", "hybrid"}, "MAS-1011", 2},
		"bad since":        {[]string{"diagnose", "--target", "redis-prod", "--symptom", "x", "--since", "soon"}, "MAS-1010", 2},
		"half window":      {[]string{"diagnose", "--target", "redis-prod", "--symptom", "x", "--from", "2026-01-01T00:00:00Z"}, "MAS-1010", 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, errOut, code := h.run(tc.args...)
			if !strings.Contains(errOut, tc.code) {
				t.Errorf("stderr does not carry %s: %q", tc.code, errOut)
			}
			if code != tc.exit {
				t.Errorf("exit = %d, want %d", code, tc.exit)
			}
			// An operator needs to know what to do, not only what broke.
			lines := strings.Split(strings.TrimSpace(errOut), "\n")
			if len(lines) < 2 {
				t.Errorf("no remedy was printed: %q", errOut)
			}
		})
	}
}

func TestMissingRequiredFlags(t *testing.T) {
	h := newHarness(t)
	if _, _, code := h.run("diagnose", "--target", "redis-prod"); code == 0 {
		t.Fatal("a missing --symptom was accepted")
	}
	if _, _, code := h.run("diagnose", "--symptom", "x"); code == 0 {
		t.Fatal("a missing --target was accepted")
	}
}

func TestDoctorSmoke(t *testing.T) {
	h := newHarness(t)
	out, _, code := h.run("doctor")
	if code != 0 {
		t.Fatalf("doctor failed against healthy stubs: exit %d\n%s", code, out)
	}
	for _, want := range []string{"configuration", "knowledge packs", "safety guard", "metrics: primary"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor did not report %q", want)
		}
	}
	jsonOut, _, _ := h.run("doctor", "--json")
	var results []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &results); err != nil {
		t.Fatalf("--json is not JSON: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("doctor --json reported nothing")
	}
}

func TestRunsAndReplaySmoke(t *testing.T) {
	h := newHarness(t)
	if _, errOut, code := h.run("diagnose", "--target", "redis-prod",
		"--symptom", "latency", "--since", "1h", "--format", "text"); code != 0 {
		t.Fatalf("diagnose failed: %s", errOut)
	}

	out, _, code := h.run("runs")
	if code != 0 {
		t.Fatalf("runs exit %d", code)
	}
	if !strings.Contains(out, "redis-prod") {
		t.Fatalf("runs output = %q", out)
	}

	jsonOut, _, _ := h.run("runs", "--json")
	var runs []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatal("no runs listed")
	}
	runID, _ := runs[0]["id"].(string)

	replayed, _, code := h.run("replay", runID)
	if code != 0 {
		t.Fatalf("replay exit %d", code)
	}
	if !strings.Contains(replayed, "Diagnostic report") {
		t.Fatalf("replay output = %q", replayed)
	}

	steps, _, code := h.run("replay", runID, "--steps")
	if code != 0 {
		t.Fatalf("replay --steps exit %d", code)
	}
	if !strings.Contains(steps, `"steps"`) {
		t.Fatal("replay --steps did not print the run record")
	}

	if _, errOut, code := h.run("replay", "run-nope"); code == 0 || !strings.Contains(errOut, "MAS-6001") {
		t.Fatalf("replaying an unknown run: exit %d, stderr %q", code, errOut)
	}
}

func TestListingCommands(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"targets"}, "redis-prod"},
		{[]string{"topologies"}, "supervisor"},
		{[]string{"packs"}, "redis"},
		{[]string{"packs", "--show", "redis"}, "memory-pressure"},
		{[]string{"tools"}, "redis-cli"},
		{[]string{"errcodes"}, "MAS-8001"},
		{[]string{"config"}, "targets"},
	} {
		out, errOut, code := h.run(tc.args...)
		if code != 0 {
			t.Errorf("%v: exit %d: %s", tc.args, code, errOut)
			continue
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v: output is missing %q", tc.args, tc.want)
		}
	}
}

func TestConfigNeverPrintsSecrets(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-plaintext-secret-in-config"
	cfg := fmt.Sprintf(`
version: "1"
llm: { provider: openai, model: gpt-4o, api_key: %q }
targets: [{ id: t, kind: redis }]
store: { type: memory }
`, secret)
	p := filepath.Join(dir, "mas.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := cli.Execute([]string{"--config", p, "config"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), secret) {
		t.Fatalf("`mas config` printed a plaintext secret:\n%s", out.String())
	}
}

func TestErrCodesMarkdownIsBilingual(t *testing.T) {
	h := newHarness(t)
	en, _, code := h.run("errcodes", "--format", "markdown", "--lang", "en")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	zh, _, _ := h.run("errcodes", "--format", "markdown", "--lang", "zh")
	if en == zh {
		t.Fatal("the Chinese error-code reference is identical to the English one")
	}
	for _, want := range []string{"# Error-code reference", "MAS-8001", "| Code | Severity |"} {
		if !strings.Contains(en, want) {
			t.Errorf("English reference is missing %q", want)
		}
	}
	for _, want := range []string{"# 错误码参考", "MAS-8001", "| 错误码 | 严重级别 |"} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese reference is missing %q", want)
		}
	}
}

func TestErrCodesFilterAndJSON(t *testing.T) {
	h := newHarness(t)
	out, _, _ := h.run("errcodes", "--filter", "8001")
	if !strings.Contains(out, "MAS-8001") || strings.Contains(out, "MAS-1001") {
		t.Fatalf("filter did not narrow the output: %q", out)
	}
	jsonOut, _, _ := h.run("errcodes", "--format", "json")
	var defs []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &defs); err != nil {
		t.Fatal(err)
	}
	if len(defs) < 50 {
		t.Fatalf("only %d codes in the registry", len(defs))
	}
}

func TestMissingConfigIsCoded(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cli.Execute([]string{"--config", "/no/such/mas.yaml", "targets"}, &out, &errOut)
	if code == 0 {
		t.Fatal("a missing configuration file was accepted")
	}
	if !strings.Contains(errOut.String(), "MAS-1004") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestHelpIsAvailableForEverySubcommand(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{
		"diagnose", "serve", "doctor", "replay", "runs", "targets",
		"topologies", "packs", "tools", "errcodes", "config", "version",
	} {
		out, _, code := h.run(name, "--help")
		if code != 0 {
			t.Errorf("%s --help: exit %d", name, code)
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%s --help printed no usage", name)
		}
	}
}

// TestTopologiesCommandDescribesEveryTopologyBilingually is FR-010. An operator
// choosing between five architectures needs each one's cost and, decisively,
// when *not* to pick it — in the language they configured.
func TestTopologiesCommandDescribesEveryTopologyBilingually(t *testing.T) {
	h := newHarness(t)
	out, errOut, code := h.run("topologies")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, name := range []string{"single", "supervisor", "plan-execute", "debate", "blackboard"} {
		if !strings.Contains(out, name) {
			t.Errorf("`mas topologies` does not list %q:\n%s", name, out)
		}
	}
	for _, phrase := range []string{"Cost:", "Choose it when:", "Avoid it when:"} {
		if !strings.Contains(out, phrase) {
			t.Errorf("`mas topologies` omits %q, so the operator cannot compare:\n%s", phrase, out)
		}
	}

	// The JSON form carries both languages, because the caller may not be the
	// operator.
	jsonOut, _, code := h.run("topologies", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var details map[string]map[string]map[string]string
	if err := json.Unmarshal([]byte(jsonOut), &details); err != nil {
		t.Fatalf("--json is not valid JSON: %v\n%s", err, jsonOut)
	}
	for _, name := range []string{"single", "supervisor", "plan-execute", "debate", "blackboard"} {
		d, ok := details[name]
		if !ok {
			t.Errorf("--json omits %q", name)
			continue
		}
		for _, field := range []string{"Summary", "Cost", "Choose", "Avoid"} {
			for _, lang := range []string{"en", "zh"} {
				if strings.TrimSpace(d[field][lang]) == "" {
					t.Errorf("%s.%s has no %s text", name, field, lang)
				}
			}
		}
	}
}
