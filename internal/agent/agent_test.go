package agent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm/mock"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// echoTool is a trivial capability so agents have something real to call.
type echoTool struct {
	name   string
	domain tool.Domain
	calls  int
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
	e.calls++
	return core.Evidence{
		Kind: core.EvidenceNote, Source: e.name, Query: tool.Str(args, "query", ""),
		Payload: map[string]any{"echo": args}, Summary: "echo from " + e.name,
	}, nil
}

func newState(t *testing.T, provider llm.Provider, tools ...tool.Tool) *agent.State {
	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	for _, tl := range tools {
		if err := reg.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := tool.NewInvoker(reg, tool.InvokerOptions{Guard: g, Mode: core.ModeOnline, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := knowledge.LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := lib.For(core.KindRedis, "7.2.4")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
	s := agent.NewState()
	s.Run = &core.RunRecord{ID: "run-test"}
	s.Request = core.DiagnoseRequest{
		Target: "redis-prod", Symptom: "p99 latency spike with evictions",
		Window: core.Window{From: now.Add(-time.Hour), To: now}, Mode: core.ModeOnline,
	}
	s.Target = core.Target{ID: "redis-prod", Kind: core.KindRedis, Version: "7.2.4",
		Env: core.EnvBinding{Name: "prod", Type: "kubernetes", Namespace: "middleware"}}
	s.Pack = pack
	s.Prior = []core.Finding{{
		ID: "f-1", Origin: "rule:redis.memory-pressure/eval-pressure", Severity: core.SeverityCritical,
		Statement: "Used memory is above 90% of the configured maxmemory.", Confidence: 0.9,
		Evidence: []string{"ev-1"},
	}}
	s.Tools = inv
	s.Provider = provider
	s.LLMConfig = config.LLMConfig{Provider: "mock", Model: "mock-1"}
	s.Language = "en"
	s.Budget = agent.Budget{MaxSteps: 24, MaxToolCalls: 40, MaxTokens: 100000, MaxWall: 30 * time.Second}
	s.Start()
	return s
}

func TestPlannerProducesAPlan(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	out, err := (agent.Planner{}).Step(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Done || strings.TrimSpace(out.Message) == "" {
		t.Fatalf("outcome = %+v", out)
	}
	notes := s.Notes()
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "Investigation plan") {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestInvestigatorCollectsEvidenceInItsDomain(t *testing.T) {
	metrics := &echoTool{name: "promql.range", domain: tool.DomainMetrics}
	logs := &echoTool{name: "loki.query", domain: tool.DomainLogs}
	s := newState(t, mock.New(mock.DefaultScript()), metrics, logs)

	if _, err := (agent.Investigator{Domain: tool.DomainMetrics}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if metrics.calls == 0 {
		t.Fatal("the metrics investigator called no metrics tool")
	}
	if logs.calls != 0 {
		t.Fatal("the metrics investigator reached outside its domain")
	}
	if len(s.Evidence()) == 0 {
		t.Fatal("no evidence was recorded")
	}
	if len(s.Notes()) == 0 {
		t.Fatal("the investigator recorded no findings note")
	}
}

func TestInvestigatorWithNoToolsRecordsAGap(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	if _, err := (agent.Investigator{Domain: tool.DomainCluster}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	gaps := s.Gaps()
	if len(gaps) != 1 || gaps[0].Reason != core.GapNotConfigured {
		t.Fatalf("gaps = %+v", gaps)
	}
	if gaps[0].Impact == "" {
		t.Error("the gap does not say what was left uninvestigated")
	}
}

func TestCorrelatorProducesRankedHypotheses(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	if _, err := (agent.Correlator{}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	hyps := s.Hypotheses()
	if len(hyps) < 2 {
		t.Fatalf("expected several hypotheses including a rejected alternative, got %d", len(hyps))
	}
	for _, h := range hyps {
		if h.ID == "" {
			t.Error("hypothesis has no id")
		}
		if h.Confidence < 0 || h.Confidence > 1 {
			t.Errorf("confidence %v out of range", h.Confidence)
		}
		if strings.TrimSpace(h.Rationale) == "" {
			t.Errorf("hypothesis %s has no rationale", h.ID)
		}
	}
}

func TestCriticAdjustsStatusAndConfidence(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	if _, err := (agent.Correlator{}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	before := s.Hypotheses()
	if _, err := (agent.Critic{}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	after := s.Hypotheses()

	supported, refuted := 0, 0
	for _, h := range after {
		switch h.Status {
		case core.HypothesisSupported:
			supported++
		case core.HypothesisRefuted:
			refuted++
		}
	}
	if supported == 0 {
		t.Error("the critic supported nothing")
	}
	if refuted == 0 {
		t.Error("the critic refuted nothing; an unfalsifiable critique is not a critique")
	}
	changed := false
	for i := range after {
		if after[i].Confidence != before[i].Confidence || after[i].Status != before[i].Status {
			changed = true
		}
	}
	if !changed {
		t.Error("critique left every hypothesis untouched")
	}
}

func TestCriticWithNoHypotheses(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	out, err := (agent.Critic{}).Step(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Done {
		t.Fatal("the critic should complete cleanly with nothing to challenge")
	}
	if s.Steps() != 0 {
		t.Fatal("the critic spent a model call with nothing to challenge")
	}
}

func TestReporterProducesSummaryAndAdvisoryRecommendations(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	if _, err := (agent.Reporter{}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(s.Summary()) == "" {
		t.Fatal("no summary was produced")
	}
	recs := s.Recommendations()
	if len(recs) == 0 {
		t.Fatal("no recommendations were produced")
	}
	sawRisk := map[core.Risk]bool{}
	for _, r := range recs {
		if !r.Advisory {
			t.Errorf("recommendation %q is not advisory — CON-003 violated", r.Statement)
		}
		if r.Statement == "" {
			t.Error("empty recommendation")
		}
		sawRisk[r.Risk] = true
	}
	if len(sawRisk) < 2 {
		t.Error("every recommendation carries the same risk level; the grading is not doing any work")
	}
}

func TestBudgetEnforced(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()), &echoTool{name: "promql.range", domain: tool.DomainMetrics})
	s.Budget = agent.Budget{MaxSteps: 1}

	if _, err := (agent.Investigator{Domain: tool.DomainMetrics}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	truncated, reason := s.Truncated()
	if !truncated {
		t.Fatal("the step budget was not enforced")
	}
	if !strings.Contains(reason, "step budget") {
		t.Fatalf("reason = %q", reason)
	}
	found := false
	for _, g := range s.Gaps() {
		if g.Code == "MAS-3005" {
			found = true
		}
	}
	if !found {
		t.Fatalf("truncation was not reported with MAS-3005: %+v", s.Gaps())
	}
}

func TestWallClockBudgetEnforced(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	s.Budget = agent.Budget{MaxSteps: 100, MaxWall: time.Nanosecond}
	time.Sleep(2 * time.Millisecond)
	if s.ConsumeStep() {
		t.Fatal("a spent wall-clock budget still granted a step")
	}
	truncated, reason := s.Truncated()
	if !truncated || !strings.Contains(reason, "wall-clock") {
		t.Fatalf("truncated=%v reason=%q", truncated, reason)
	}
}

func TestTokenBudgetEnforced(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	s.Budget = agent.Budget{MaxSteps: 100, MaxTokens: 10}
	s.AddUsage(core.Usage{PromptTokens: 20})
	if s.ConsumeStep() {
		t.Fatal("a spent token budget still granted a step")
	}
	_, reason := s.Truncated()
	if !strings.Contains(reason, "token budget") {
		t.Fatalf("reason = %q", reason)
	}
}

// TestProviderFailureDegrades proves a model outage does not end a run: the
// deterministic findings still reach the operator.
func TestProviderFailureDegrades(t *testing.T) {
	s := newState(t, failingProvider{})
	out, err := (agent.Correlator{}).Step(context.Background(), s)
	if err != nil {
		t.Fatalf("a provider failure must not fail the run: %v", err)
	}
	if !out.Done {
		t.Fatal("the step did not complete")
	}
	found := false
	for _, g := range s.Gaps() {
		if g.Code == "MAS-2001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the provider failure was not recorded as a gap: %+v", s.Gaps())
	}
}

type failingProvider struct{}

func (failingProvider) Name() string { return "failing" }
func (failingProvider) Close() error { return nil }
func (failingProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errs.New("MAS-2001", "failing")
}

// repeatingProvider models the RSK-002 failure: a weaker model that keeps
// emitting the same unusable tool call instead of correcting itself.
type repeatingProvider struct{ calls int }

func (p *repeatingProvider) Name() string { return "repeating" }
func (p *repeatingProvider) Close() error { return nil }
func (p *repeatingProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	p.calls++
	return llm.Response{
		StopReason: llm.StopToolUse,
		ToolCalls: []llm.ToolCall{{
			ID: "c", Name: "promql.range", Args: map[string]any{"unknown_arg": "x"},
		}},
		Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5},
	}, nil
}

func TestInvalidToolCallRepairThenGap(t *testing.T) {
	tl := &echoTool{name: "promql.range", domain: tool.DomainMetrics}
	provider := &repeatingProvider{}
	s := newState(t, provider, tl)

	if _, err := (agent.Investigator{Domain: tool.DomainMetrics}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if tl.calls != 0 {
		t.Fatal("a schema-invalid call reached the tool")
	}
	sawRefusal, sawGiveUp := false, false
	for _, g := range s.Gaps() {
		if g.Code == "MAS-8005" {
			sawRefusal = true
		}
		if g.Code == "MAS-2004" {
			sawGiveUp = true
		}
	}
	if !sawRefusal {
		t.Errorf("the invalid argument was not refused: %+v", s.Gaps())
	}
	if !sawGiveUp {
		t.Errorf("the loop did not give up after repair attempts: %+v", s.Gaps())
	}
	// Giving up must be bounded: a model that never corrects itself cannot burn
	// the whole step budget on one domain (RSK-002).
	if provider.calls > 5 {
		t.Errorf("the loop retried %d times before giving up", provider.calls)
	}
}

func TestEvidenceDeduplicatedByDigest(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	ev := core.Evidence{ID: "ev-1", Kind: core.EvidenceNote, Summary: "x", Digest: "same"}
	s.AddEvidence(ev)
	ev2 := ev
	ev2.ID = "ev-2"
	s.AddEvidence(ev2)
	if got := len(s.Evidence()); got != 1 {
		t.Fatalf("two investigators asking the same question produced %d evidence entries", got)
	}
}

func TestSortNotesIsDeterministic(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	s.AddNote("Logs findings: b")
	s.AddNote("Metrics findings: a")
	s.AddNote("Investigation plan: p")
	s.SortNotes([]string{"Investigation plan", "Metrics", "Logs"})
	notes := s.Notes()
	if !strings.HasPrefix(notes[0], "Investigation plan") ||
		!strings.HasPrefix(notes[1], "Metrics") ||
		!strings.HasPrefix(notes[2], "Logs") {
		t.Fatalf("notes not ordered: %v", notes)
	}
}

func TestPromptContextGroundsTheModel(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	p := mock.New(mock.DefaultScript())
	s.Provider = p
	if _, err := (agent.Planner{}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	calls := p.Calls()
	if len(calls) == 0 {
		t.Fatal("no request reached the provider")
	}
	prompt := calls[0].System + calls[0].Messages[0].Content
	for _, want := range []string{
		"redis-prod", "p99 latency spike", "memory-pressure",
		"Used memory is above 90%", "read-only",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt is missing %q", want)
		}
	}
	// The preamble must forbid claiming action, or the model may report having
	// fixed something (CON-003).
	if !strings.Contains(calls[0].System, "Never describe an action as done") {
		t.Error("the system prompt does not forbid claiming an action was taken")
	}
}

func TestLanguageInstructionSwitches(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	s.Language = "zh"
	p := mock.New(mock.DefaultScript())
	s.Provider = p
	if _, err := (agent.Planner{}).Step(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Calls()[0].System, "Simplified Chinese") {
		t.Fatal("the Chinese language instruction was not applied")
	}
}

// TestFabricatedCitationsAreDroppedAndRecorded pins a correctness rule the
// topology conformance contract exposed: a model that cites evidence it was
// never given is guessing, and a report that reprints the citation launders the
// guess into provenance. The reference is dropped and the fabrication is
// recorded, because an operator who sees "supported by ev-7" has no way to know
// ev-7 does not exist.
func TestFabricatedCitationsAreDroppedAndRecorded(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))

	id := s.AddHypothesis(core.Hypothesis{
		Statement:     "memory pressure",
		Supporting:    []string{"f-1", "ev-404", "  "},
		Contradicting: []string{"ev-999"},
	})

	var got core.Hypothesis
	for _, h := range s.Hypotheses() {
		if h.ID == id {
			got = h
		}
	}
	if want := []string{"f-1"}; len(got.Supporting) != 1 || got.Supporting[0] != want[0] {
		t.Errorf("supporting = %v, want %v: only references this run produced may survive",
			got.Supporting, want)
	}
	if len(got.Contradicting) != 0 {
		t.Errorf("contradicting = %v, want none", got.Contradicting)
	}

	found := false
	for _, g := range s.Gaps() {
		if g.Code == "MAS-2010" {
			found = true
			if !strings.Contains(g.Detail, "ev-404") || !strings.Contains(g.Detail, "ev-999") {
				t.Errorf("the gap does not name the fabricated references: %s", g.Detail)
			}
			if g.Impact == "" {
				t.Error("the gap does not say what the operator loses")
			}
		}
	}
	if !found {
		t.Errorf("fabricated citations were dropped silently; gaps: %+v", s.Gaps())
	}
}

// TestRealCitationsSurvive is the other half: the check must not quietly strip
// the provenance a correct run depends on.
func TestRealCitationsSurvive(t *testing.T) {
	s := newState(t, mock.New(mock.DefaultScript()))
	s.AddEvidence(core.Evidence{ID: "ev-1", Kind: core.EvidenceNote, Source: "test", Summary: "x"})

	id := s.AddHypothesis(core.Hypothesis{
		Statement: "memory pressure", Supporting: []string{"ev-1", "f-1"},
	})
	for _, h := range s.Hypotheses() {
		if h.ID == id && len(h.Supporting) != 2 {
			t.Errorf("supporting = %v, want both references kept", h.Supporting)
		}
	}
	for _, g := range s.Gaps() {
		if g.Code == "MAS-2010" {
			t.Errorf("valid citations were reported as fabricated: %s", g.Detail)
		}
	}
}
