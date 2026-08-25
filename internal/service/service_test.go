package service_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
	"github.com/zlrrr/multi-agent-system-turbo/internal/store"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// stubs stands up a fake observability stack so an end-to-end run needs no
// network, no cluster and no model (NFR-006).
type stubs struct {
	prom    *httptest.Server
	loki    *httptest.Server
	promHit int
	lokiHit int
}

func newStubs(t *testing.T, memoryRatio float64, evicting bool) *stubs {
	t.Helper()
	s := &stubs{}
	s.prom = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.promHit++
		_ = r.ParseForm()
		q := r.Form.Get("query")
		value := "1"
		switch {
		case strings.Contains(q, "redis_memory_used_bytes"):
			value = fmt.Sprintf("%d", int(1000*memoryRatio))
		case strings.Contains(q, "redis_memory_max_bytes"):
			value = "1000"
		case strings.Contains(q, "evicted"):
			if evicting {
				value = "12"
			} else {
				value = "0"
			}
		case strings.Contains(q, "redis_up"):
			value = "1"
		case strings.Contains(q, "mem_fragmentation"):
			value = "1.1"
		case strings.Contains(q, "latest_fork_usec"):
			value = "1000"
		case strings.Contains(q, "cpu"):
			value = "0.2"
		case strings.Contains(q, "keyspace_hits"):
			value = "0.95"
		}
		if strings.Contains(r.URL.Path, "query_range") {
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[
			  {"metric":{"instance":"redis-0"},"values":[[1724400000,%q],[1724400060,%q]]}]}}`, value, value)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[
		  {"metric":{"instance":"redis-0"},"value":[1724400000,%q]}]}}`, value)
	}))
	s.loki = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lokiHit++
		if strings.Contains(r.URL.Path, "labels") {
			_, _ = w.Write([]byte(`{"status":"success","data":["job","pod"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
		  {"stream":{"job":"redis"},"values":[["1724400000000000000","OOM command not allowed when used memory > 'maxmemory'"]]}]}}`))
	}))
	t.Cleanup(func() { s.prom.Close(); s.loki.Close() })
	return s
}

func newService(t *testing.T, st *stubs, mutate func(*config.Config)) *service.Service {
	t.Helper()
	cfg := config.Default()
	cfg.Store = config.StoreConfig{Type: "memory"}
	cfg.Log.Level = "error"
	cfg.Source.Enabled = false
	cfg.LLM = config.LLMConfig{Provider: "mock", Model: "mock-1", MaxTokens: 1024}
	cfg.Run.DefaultTopology = "supervisor"
	cfg.Run.DefaultMode = "offline"
	cfg.Run.DeterministicShortCircuit = 0 // exercise the agent phase by default
	if st != nil {
		cfg.Telemetry.Metrics = []config.MetricsSource{{
			Name: "primary", Type: "prometheus", URL: st.prom.URL,
			Timeout: config.Duration(2 * time.Second), MaxSamples: 100,
		}}
		cfg.Telemetry.Logs = []config.LogsSource{{
			Name: "primary", Type: "loki", URL: st.loki.URL,
			Timeout: config.Duration(2 * time.Second), MaxLines: 100,
		}}
	}
	cfg.Targets = []config.TargetConfig{{
		ID: "redis-prod", Kind: "redis", Version: "7.2.4",
		Labels: map[string]string{"instance": "redis-0"},
	}}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Config: cfg, Store: store.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func request() core.DiagnoseRequest {
	at := time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
	return core.DiagnoseRequest{
		Target: "redis-prod", Symptom: "p99 latency spike with evictions and oom errors",
		Window: core.Window{From: at.Add(-time.Hour), To: at},
	}
}

func TestEndToEndProducesAUsableReport(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), nil)
	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Validate(); err != nil {
		t.Fatalf("the report failed its own invariants: %v", err)
	}
	if strings.TrimSpace(rep.Summary) == "" {
		t.Fatal("no summary")
	}
	if len(rep.Hypotheses) == 0 {
		t.Fatal("no hypotheses")
	}
	if len(rep.Findings) == 0 {
		t.Fatal("the deterministic phase produced no findings on a clearly unhealthy instance")
	}
	if len(rep.Evidence) == 0 {
		t.Fatal("no evidence")
	}
	if len(rep.Recommendations) == 0 {
		t.Fatal("no recommendations")
	}
	for _, r := range rep.Recommendations {
		if !r.Advisory {
			t.Errorf("recommendation %q is not advisory", r.Statement)
		}
	}
	if rep.Usage.ToolCalls == 0 {
		t.Error("tool calls were not accounted")
	}
	if rep.Usage.LLMCalls == 0 {
		t.Error("model calls were not accounted")
	}
	if rep.Topology != "supervisor" {
		t.Errorf("topology = %s", rep.Topology)
	}
	// Every hypothesis must be traceable, or the report cannot be checked.
	for _, h := range rep.Hypotheses {
		if h.Rank == 0 {
			t.Errorf("hypothesis %s was not ranked", h.ID)
		}
	}
}

// TestEndToEndUnder5s is the NFR-001 gate.
func TestEndToEndUnder5s(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), nil)
	start := time.Now()
	if _, err := svc.Diagnose(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("end-to-end diagnosis took %s, above the 5s budget", elapsed)
	}
}

// TestDeterminism is the NFR-010 gate.
func TestDeterminism(t *testing.T) {
	render := func() string {
		svc := newService(t, newStubs(t, 0.99, true), nil)
		rep, err := svc.Diagnose(context.Background(), request())
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.WriteString(rep.Summary)
		for _, h := range rep.Hypotheses {
			fmt.Fprintf(&b, "|%d:%s:%s:%.2f", h.Rank, h.Statement, h.Status, h.Confidence)
		}
		for _, f := range rep.Findings {
			fmt.Fprintf(&b, "|%s:%s", f.Origin, f.Statement)
		}
		for _, r := range rep.Recommendations {
			fmt.Fprintf(&b, "|%s:%s", r.Risk, r.Statement)
		}
		return b.String()
	}
	if a, b := render(), render(); a != b {
		t.Fatalf("two identical runs produced different reports:\n%s\n---\n%s", a, b)
	}
}

// TestShortCircuit proves the deterministic layer answers routine cases for
// free (project goal G9.1).
func TestShortCircuit(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), func(c *config.Config) {
		c.Run.DeterministicShortCircuit = 0.85
	})
	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Usage.LLMCalls != 0 {
		t.Fatalf("a short-circuited run made %d model calls", rep.Usage.LLMCalls)
	}
	if len(rep.Hypotheses) == 0 {
		t.Fatal("a short-circuited run must still read like a diagnosis")
	}
	if len(rep.Recommendations) == 0 {
		t.Fatal("a short-circuited run produced no recommendations; the pack advice was lost")
	}
	if strings.TrimSpace(rep.Summary) == "" {
		t.Fatal("a short-circuited run has no summary")
	}
}

func TestForceAgentsOverridesShortCircuit(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), func(c *config.Config) {
		c.Run.DeterministicShortCircuit = 0.85
	})
	req := request()
	req.Options = map[string]string{"force_agents": "true"}
	rep, err := svc.Diagnose(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Usage.LLMCalls == 0 {
		t.Fatal("force_agents did not override the short circuit")
	}
}

func TestHealthyInstanceReportsNoFault(t *testing.T) {
	svc := newService(t, newStubs(t, 0.2, false), nil)
	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Severity == core.SeverityCritical {
			t.Errorf("a healthy instance produced a critical finding: %s", f.Statement)
		}
	}
	if len(rep.ChecksPassed) == 0 {
		t.Error("no checks were reported as passed; the operator cannot see what was ruled out")
	}
}

// TestAllSourcesDownStillCompletes is the FR-013 gate: degradation, not failure.
func TestAllSourcesDownStillCompletes(t *testing.T) {
	st := newStubs(t, 0.99, true)
	svc := newService(t, st, nil)
	st.prom.Close()
	st.loki.Close()

	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatalf("a total telemetry outage must degrade, not fail: %v", err)
	}
	if len(rep.Gaps) == 0 {
		t.Fatal("no gaps were recorded despite every source being down")
	}
	sawUnavailable := false
	for _, g := range rep.Gaps {
		if g.Reason == core.GapUnavailable {
			sawUnavailable = true
			if g.Impact == "" {
				t.Errorf("gap %q does not state its effect on the analysis", g.Intent)
			}
		}
	}
	if !sawUnavailable {
		t.Fatalf("the outage was not recorded as unavailable: %+v", rep.Gaps)
	}
	if strings.TrimSpace(rep.Summary) == "" {
		t.Fatal("a degraded run produced no summary at all")
	}
}

func TestNoEvidenceSourceIsRefusedAtAdmission(t *testing.T) {
	svc := newService(t, nil, nil)
	_, err := svc.Diagnose(context.Background(), request())
	if err == nil {
		t.Fatal("a run with no evidence source at all should be refused, not produce an empty report")
	}
	if errs.CodeOf(err) != "MAS-1007" {
		t.Fatalf("got %v, want MAS-1007", err)
	}
}

func TestProviderOutageKeepsDeterministicFindings(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), func(c *config.Config) {
		c.LLM.Provider = "openai"
		c.LLM.BaseURL = "http://127.0.0.1:1" // nothing listens here
		c.LLM.Timeout = config.Duration(200 * time.Millisecond)
	})
	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatalf("a model outage must not fail the run: %v", err)
	}
	if len(rep.Findings) == 0 {
		t.Fatal("the deterministic findings were lost when the model was unavailable")
	}
	if strings.TrimSpace(rep.Summary) == "" {
		t.Fatal("no summary was produced without a model")
	}
}

func TestAdmissionCodes(t *testing.T) {
	svc := newService(t, newStubs(t, 0.5, false), nil)
	at := time.Now().UTC()
	cases := map[string]struct {
		req  core.DiagnoseRequest
		want string
	}{
		"no target":        {core.DiagnoseRequest{Symptom: "x"}, "MAS-1007"},
		"no symptom":       {core.DiagnoseRequest{Target: "redis-prod"}, "MAS-1007"},
		"unknown target":   {core.DiagnoseRequest{Target: "ghost", Symptom: "x"}, "MAS-1005"},
		"bad mode":         {core.DiagnoseRequest{Target: "redis-prod", Symptom: "x", Mode: "hybrid"}, "MAS-1011"},
		"unknown topology": {core.DiagnoseRequest{Target: "redis-prod", Symptom: "x", Topology: "no-such-topology"}, "MAS-3001"},
		// A near-miss on a real name must still be refused: "debate " with a
		// stray space, or a plausible-looking alias, are the ways this fails in
		// practice.
		"near-miss topology": {core.DiagnoseRequest{Target: "redis-prod", Symptom: "x", Topology: "debates"}, "MAS-3001"},
		"bad language":       {core.DiagnoseRequest{Target: "redis-prod", Symptom: "x", Language: "fr"}, "MAS-1007"},
		"inverted window": {core.DiagnoseRequest{
			Target: "redis-prod", Symptom: "x",
			Window: core.Window{From: at, To: at.Add(-time.Hour)},
		}, "MAS-1010"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Admit(tc.req); errs.CodeOf(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, errs.CodeOf(err), tc.want)
			}
		})
	}
}

func TestAdmissionFillsDefaults(t *testing.T) {
	svc := newService(t, newStubs(t, 0.5, false), nil)
	got, err := svc.Admit(core.DiagnoseRequest{Target: "redis-prod", Symptom: "latency"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != core.ModeOffline || got.Topology != "supervisor" || got.Language != "en" {
		t.Fatalf("defaults not applied: %+v", got)
	}
	if got.Window.From.IsZero() || got.Window.To.IsZero() || got.Window.Duration() <= 0 {
		t.Fatalf("window not defaulted: %+v", got.Window)
	}
	if got.Budget.MaxSteps == 0 || got.Budget.MaxWall == 0 {
		t.Fatalf("budget not defaulted: %+v", got.Budget)
	}
}

// TestReplayWithoutNetwork is the FR-012 gate.
func TestReplayWithoutNetwork(t *testing.T) {
	st := newStubs(t, 0.99, true)
	svc := newService(t, st, nil)
	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}

	// Take everything away: no telemetry, no model.
	st.prom.Close()
	st.loki.Close()
	promBefore, lokiBefore := st.promHit, st.lokiHit

	replayed, err := svc.Replay(context.Background(), rep.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.RunID != rep.RunID || replayed.Summary != rep.Summary {
		t.Fatal("the replayed report differs from the original")
	}
	if len(replayed.Hypotheses) != len(rep.Hypotheses) {
		t.Fatal("hypotheses were lost in the round trip")
	}
	if st.promHit != promBefore || st.lokiHit != lokiBefore {
		t.Fatal("replay contacted a telemetry source; a stored run must be reproducible offline")
	}
}

func TestReplayUnknownRunIsCoded(t *testing.T) {
	svc := newService(t, newStubs(t, 0.5, false), nil)
	if _, err := svc.Replay(context.Background(), "run-nope"); errs.CodeOf(err) != "MAS-6001" {
		t.Fatalf("got %v, want MAS-6001", err)
	}
}

func TestRunRecordIsAuditable(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), nil)
	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Run(context.Background(), rep.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != core.RunCompleted {
		t.Fatalf("status = %s", rec.Status)
	}
	var toolSteps, llmSteps int
	for _, s := range rec.Steps {
		switch s.Kind {
		case core.StepToolCall:
			toolSteps++
		case core.StepLLMCall:
			llmSteps++
		}
	}
	if toolSteps == 0 {
		t.Error("no tool call was recorded in the audit trail")
	}
	if llmSteps == 0 {
		t.Error("no model exchange was recorded in the audit trail")
	}
	if rec.Versions["binary"] == "" || rec.Versions["topology"] == "" {
		t.Errorf("the run does not record what produced it: %+v", rec.Versions)
	}
	summaries, err := svc.Runs(context.Background(), 10)
	if err != nil || len(summaries) == 0 {
		t.Fatalf("listing runs: %v %+v", err, summaries)
	}
}

// TestRunRecordRedactsSecrets is the FR-016 gate at the persistence boundary.
func TestRunRecordRedactsSecrets(t *testing.T) {
	const secret = "sk-super-secret-token-value"
	t.Setenv("MAS_TEST_PROM_TOKEN", secret)
	stub := newStubs(t, 0.99, true)
	svc := newService(t, stub, func(c *config.Config) {
		c.Telemetry.Metrics[0].Auth = config.AuthConfig{
			Type: "bearer", Token: config.Secret("${env:MAS_TEST_PROM_TOKEN}"),
		}
		c.Log.Redact = []string{secret}
	})
	rep, err := svc.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Run(context.Background(), rep.RunID)
	if err != nil {
		t.Fatal(err)
	}
	blob := fmt.Sprintf("%+v", rec)
	if strings.Contains(blob, secret) {
		t.Fatal("the run record contains a credential")
	}
}

func TestZHLanguageProducesChineseReport(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), func(c *config.Config) {
		c.Run.DeterministicShortCircuit = 0.85
	})
	req := request()
	req.Language = "zh"
	rep, err := svc.Diagnose(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	joined := rep.Summary
	for _, f := range rep.Findings {
		joined += f.Statement
	}
	if !containsHan(joined) {
		t.Fatalf("a zh run produced no Chinese text: %q", joined)
	}
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func TestTopologySelectablePerRun(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), nil)
	for _, topology := range []string{"single", "supervisor"} {
		req := request()
		req.Topology = topology
		rep, err := svc.Diagnose(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", topology, err)
		}
		if rep.Topology != topology {
			t.Fatalf("report says %s but %s was requested", rep.Topology, topology)
		}
	}
}

func TestDoctorAgainstStubs(t *testing.T) {
	st := newStubs(t, 0.5, false)
	svc := newService(t, st, nil)
	results := svc.Doctor(context.Background())
	if len(results) == 0 {
		t.Fatal("doctor reported nothing")
	}
	byName := map[string]service.CheckResult{}
	for _, r := range results {
		byName[r.Name] = r
	}
	for _, want := range []string{"configuration", "knowledge packs", "safety guard",
		"metrics: primary", "logs: primary", "llm provider", "run store", "topologies"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("doctor did not check %q", want)
		}
	}
	if byName["metrics: primary"].Status != service.CheckOK {
		t.Errorf("a reachable metrics source was not reported OK: %+v", byName["metrics: primary"])
	}
	if !service.DoctorOK(results) {
		for _, r := range results {
			if r.Status == service.CheckFail {
				t.Errorf("unexpected failure: %+v", r)
			}
		}
	}
}

func TestDoctorReportsUnreachableSources(t *testing.T) {
	st := newStubs(t, 0.5, false)
	svc := newService(t, st, nil)
	st.prom.Close()

	results := svc.Doctor(context.Background())
	found := false
	for _, r := range results {
		if r.Name == "metrics: primary" {
			found = true
			if r.Status != service.CheckFail {
				t.Errorf("an unreachable source was reported as %s", r.Status)
			}
			if r.Code == "" {
				t.Error("the failure carries no error code")
			}
			if r.Remedy == "" {
				t.Error("the failure carries no remedy; an operator is left guessing")
			}
		}
	}
	if !found {
		t.Fatal("doctor did not check the metrics source")
	}
	if service.DoctorOK(results) {
		t.Error("DoctorOK reported success despite a failed check")
	}
}

func TestFilesystemStoreEndToEnd(t *testing.T) {
	dir := t.TempDir()
	svc := newService(t, newStubs(t, 0.99, true), func(c *config.Config) {
		c.Store = config.StoreConfig{Type: "fs", Dir: dir}
	})
	// Replace the memory store the helper injected with a real filesystem one.
	fs, err := store.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := svc.Config()
	svc2, err := service.New(service.Options{Config: cfg, Store: fs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc2.Close() }()

	rep, err := svc2.Diagnose(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && strings.Contains(e.Name(), strings.TrimPrefix(rep.RunID, "")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the run was not persisted to %s: %v", dir, entries)
	}
	// A fresh service over the same directory must read the run back.
	svc3, err := service.New(service.Options{Config: cfg, Store: mustFS(t, dir)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc3.Close() }()
	if _, err := svc3.Replay(context.Background(), rep.RunID); err != nil {
		t.Fatalf("a persisted run was not replayable from a new process: %v", err)
	}
}

func mustFS(t *testing.T, dir string) *store.FS {
	t.Helper()
	fs, err := store.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// TestEveryRegisteredTopologyIsAdmitted is the other side of MAS-3001 (FR-012):
// the admission check must accept every topology this build ships, or a
// registered architecture would be unreachable from the CLI and the API.
func TestEveryRegisteredTopologyIsAdmitted(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), nil)
	for _, name := range orchestrator.Names() {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Diagnose(context.Background(), core.DiagnoseRequest{
				Target: "redis-prod", Symptom: "p99 latency spike with evictions",
				Topology: name, Mode: core.ModeOffline,
			})
			if errs.CodeOf(err) == "MAS-3001" {
				t.Errorf("topology %q is registered but rejected at admission", name)
			}
			if err != nil {
				t.Errorf("registered topology %q failed: %v", name, err)
			}
		})
	}
}

// TestRunRecordCarriesTopologyAccounting is FR-011. Comparing topologies means
// comparing what each one cost, so the record has to name the topology and its
// usage — otherwise a comparison is a memory of two runs, not a measurement.
func TestRunRecordCarriesTopologyAccounting(t *testing.T) {
	svc := newService(t, newStubs(t, 0.99, true), nil)
	for _, name := range orchestrator.Names() {
		t.Run(name, func(t *testing.T) {
			rep, err := svc.Diagnose(context.Background(), core.DiagnoseRequest{
				Target: "redis-prod", Symptom: "p99 latency spike with evictions",
				Topology: name, Mode: core.ModeOffline,
			})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Topology != name {
				t.Errorf("report topology = %q, want %q", rep.Topology, name)
			}
			if rep.Usage.WallMillis <= 0 {
				t.Error("the report records no wall-clock cost")
			}

			rec, err := svc.Run(context.Background(), rep.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Request.Topology != name {
				t.Errorf("run record topology = %q, want %q", rec.Request.Topology, name)
			}
			if rec.Summarise().Topology != name {
				t.Errorf("run summary topology = %q, want %q", rec.Summarise().Topology, name)
			}
			// Every model exchange must be attributable, which is what makes
			// the cost assignable to a role rather than only to the run.
			for _, s := range rec.Steps {
				if s.Kind == core.StepLLMCall && strings.TrimSpace(s.Actor) == "" {
					t.Errorf("a recorded model exchange names no role: %+v", s)
				}
			}
		})
	}
}

// TestDoctorReportsExecAvailability is FR-011. "No exec tool" has two very
// different causes — a policy decision and a missing capability — and an
// operator who cannot tell them apart goes looking for the wrong problem.
func TestDoctorReportsExecAvailability(t *testing.T) {
	for name, tc := range map[string]struct {
		exec       *bool
		wantStatus service.CheckStatus
		wantText   string
	}{
		"enabled by default": {nil, service.CheckOK, "available"},
		"explicitly enabled": {boolPtr(true), service.CheckOK, "available"},
		"disabled by policy": {boolPtr(false), service.CheckSkip, "MAS-4210"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newService(t, newStubs(t, 0.5, false), func(c *config.Config) {
				c.Envs = map[string]config.EnvConfig{
					"kube": {Type: "kubernetes", APIServer: "https://127.0.0.1:1", Exec: tc.exec},
				}
			})
			rep := svc.Doctor(context.Background())
			found := false
			for _, c := range rep {
				if !strings.Contains(c.Name, "exec") {
					continue
				}
				found = true
				if c.Status != tc.wantStatus {
					t.Errorf("status = %q, want %q (%s)", c.Status, tc.wantStatus, c.Detail)
				}
				if !strings.Contains(c.Detail, tc.wantText) {
					t.Errorf("detail %q does not mention %q", c.Detail, tc.wantText)
				}
			}
			if !found {
				t.Error("doctor said nothing about in-container execution")
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
