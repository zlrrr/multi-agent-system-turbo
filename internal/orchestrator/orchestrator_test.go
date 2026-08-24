package orchestrator_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm/mock"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

type echoTool struct {
	name   string
	domain tool.Domain
}

func (e *echoTool) Name() string         { return e.name }
func (e *echoTool) Description() string  { return "echo " + e.name }
func (e *echoTool) Domain() tool.Domain  { return e.domain }
func (e *echoTool) Safety() safety.Class { return safety.ClassReadOnly }
func (e *echoTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"query":   {Type: tool.TypeString, Description: "anything"},
		"limit":   {Type: tool.TypeInteger, Description: "count", Default: 10},
		"type":    {Type: tool.TypeString, Description: "kind"},
		"pattern": {Type: tool.TypeString, Description: "regex"},
	})
}
func (e *echoTool) Plan(map[string]any) (safety.Call, error) {
	return safety.Call{Class: safety.ClassReadOnly,
		HTTP: &safety.HTTPEffect{Method: "GET", URL: "http://prom:9090/api/v1/query"}}, nil
}
func (e *echoTool) Invoke(_ context.Context, args map[string]any) (core.Evidence, error) {
	return core.Evidence{
		Kind: core.EvidenceNote, Source: e.name, Query: tool.Str(args, "query", ""),
		Payload: map[string]any{"echo": args}, Summary: "echo from " + e.name,
	}, nil
}

func newState(t *testing.T) *agent.State {
	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	reg.MustRegister(
		&echoTool{name: "promql.range", domain: tool.DomainMetrics},
		&echoTool{name: "promql.instant", domain: tool.DomainMetrics},
		&echoTool{name: "loki.query", domain: tool.DomainLogs},
		&echoTool{name: "kube.events", domain: tool.DomainCluster},
		&echoTool{name: "source.search", domain: tool.DomainSource},
	)
	inv, err := tool.NewInvoker(reg, tool.InvokerOptions{Guard: g, Mode: core.ModeOnline, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	lib, _ := knowledge.LoadDefault(nil)
	pack, err := lib.For(core.KindRedis, "7.2.4")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
	s := agent.NewState()
	s.Run = &core.RunRecord{ID: "run-test"}
	s.Request = core.DiagnoseRequest{
		Target: "redis-prod", Symptom: "p99 latency spike with evictions and oom errors",
		Window: core.Window{From: now.Add(-time.Hour), To: now}, Mode: core.ModeOnline,
	}
	s.Target = core.Target{ID: "redis-prod", Kind: core.KindRedis, Version: "7.2.4",
		Env: core.EnvBinding{Name: "prod", Type: "kubernetes", Namespace: "middleware"}}
	s.Pack = pack
	s.Prior = []core.Finding{{
		ID: "f-1", Origin: "rule:redis.memory-pressure/eval-pressure", Severity: core.SeverityCritical,
		Statement: "Used memory is above 90% of the configured maxmemory.", Confidence: 0.9,
	}}
	s.Tools = inv
	s.Provider = mock.New(mock.DefaultScript())
	s.LLMConfig = config.LLMConfig{Provider: "mock", Model: "mock-1"}
	s.Language = "en"
	s.Budget = agent.Budget{MaxSteps: 40, MaxToolCalls: 60, MaxTokens: 500000, MaxWall: 30 * time.Second}
	s.MaxConcurrency = 4
	s.Start()
	return s
}

func TestRegistryListsBothTopologies(t *testing.T) {
	names := orchestrator.Names()
	if len(names) < 2 {
		t.Fatalf("topologies = %v", names)
	}
	for _, want := range []string{"single", "supervisor"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("topology %s is not registered", want)
		}
	}
	for name, desc := range orchestrator.Descriptions() {
		if strings.TrimSpace(desc) == "" {
			t.Errorf("topology %s has no description; it appears in `mas topologies`", name)
		}
	}
}

func TestUnknownTopologyIsCoded(t *testing.T) {
	if _, err := orchestrator.Open("debate"); errs.CodeOf(err) != "MAS-3001" {
		t.Fatalf("got %v, want MAS-3001", err)
	}
}

func TestSingleProducesReportMaterial(t *testing.T) {
	o, err := orchestrator.Open("single")
	if err != nil {
		t.Fatal(err)
	}
	s := newState(t)
	if err := o.Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(s.Summary()) == "" {
		t.Fatal("no summary")
	}
	if len(s.Hypotheses()) == 0 {
		t.Fatal("no hypotheses")
	}
	if len(s.Recommendations()) == 0 {
		t.Fatal("no recommendations")
	}
	if s.Usage().LLMCalls == 0 {
		t.Fatal("usage was not accounted")
	}
}

func TestSupervisorProducesReportMaterial(t *testing.T) {
	o, err := orchestrator.Open("supervisor")
	if err != nil {
		t.Fatal(err)
	}
	s := newState(t)
	if err := o.Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(s.Summary()) == "" {
		t.Fatal("no summary")
	}
	hyps := s.Hypotheses()
	if len(hyps) < 2 {
		t.Fatalf("expected several hypotheses, got %d", len(hyps))
	}
	assessed := false
	for _, h := range hyps {
		if h.Status == core.HypothesisSupported || h.Status == core.HypothesisRefuted {
			assessed = true
		}
	}
	if !assessed {
		t.Error("the critic step did not run: no hypothesis was assessed")
	}
	if len(s.Evidence()) == 0 {
		t.Fatal("the investigators collected no evidence")
	}
	if len(s.Notes()) < 2 {
		t.Fatalf("expected a plan and investigator notes, got %d", len(s.Notes()))
	}
	if len(s.Recommendations()) == 0 {
		t.Fatal("no recommendations")
	}
}

// TestBothTopologiesRunTheSameCase is the G7.3 property: the same request,
// state and tools go through two different topologies without code changes.
func TestBothTopologiesRunTheSameCase(t *testing.T) {
	results := map[string]int{}
	for _, name := range []string{"single", "supervisor"} {
		o, err := orchestrator.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		s := newState(t)
		if err := o.Run(context.Background(), s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.TrimSpace(s.Summary()) == "" {
			t.Fatalf("%s produced no summary", name)
		}
		results[name] = s.Usage().LLMCalls
	}
	// The comparison only means anything if the topologies actually differ in
	// how much reasoning they do.
	if results["supervisor"] <= results["single"] {
		t.Errorf("supervisor used %d model calls and single used %d; the topologies are not distinguishable",
			results["supervisor"], results["single"])
	}
}

// TestSupervisorIsDeterministic guards NFR-010 against the concurrency the
// supervisor introduces.
func TestSupervisorIsDeterministic(t *testing.T) {
	render := func() string {
		o, _ := orchestrator.Open("supervisor")
		s := newState(t)
		if err := o.Run(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.WriteString(s.Summary())
		for _, h := range s.Hypotheses() {
			b.WriteString("|" + h.ID + h.Statement + string(h.Status))
		}
		for _, n := range s.Notes() {
			b.WriteString("|" + n)
		}
		return b.String()
	}
	if a, b := render(), render(); a != b {
		t.Fatal("two identical supervisor runs produced different results")
	}
}

func TestSupervisorSkipsDomainsWithNoTools(t *testing.T) {
	g, _ := safety.NewGuard(config.Default().Safety)
	reg := tool.NewRegistry()
	reg.MustRegister(&echoTool{name: "promql.range", domain: tool.DomainMetrics})
	inv, _ := tool.NewInvoker(reg, tool.InvokerOptions{Guard: g, Mode: core.ModeOffline, Timeout: time.Second})

	s := newState(t)
	s.Tools = inv

	o, _ := orchestrator.Open("supervisor")
	if err := o.Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for _, gap := range s.Gaps() {
		if strings.Contains(gap.Intent, "investigator (cluster)") {
			t.Error("the supervisor spent a step on a domain with no tools")
		}
	}
}
