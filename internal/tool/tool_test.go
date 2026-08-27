package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// fakeTool is a controllable Tool for exercising the Invoker contract.
type fakeTool struct {
	name     string
	domain   Domain
	schema   Schema
	class    safety.Class
	plan     safety.Call
	planErr  error
	invoke   func(ctx context.Context, args map[string]any) (core.Evidence, error)
	needMode core.Mode
}

func (f *fakeTool) Name() string            { return f.name }
func (f *fakeTool) Description() string     { return "fake " + f.name }
func (f *fakeTool) Domain() Domain          { return f.domain }
func (f *fakeTool) ArgsSchema() Schema      { return f.schema }
func (f *fakeTool) Safety() safety.Class    { return f.class }
func (f *fakeTool) RequiredMode() core.Mode { return f.needMode }
func (f *fakeTool) Plan(map[string]any) (safety.Call, error) {
	return f.plan, f.planErr
}
func (f *fakeTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	return f.invoke(ctx, args)
}

type sliceSink struct{ steps []core.Step }

func (s *sliceSink) AppendStep(_ context.Context, step core.Step) { s.steps = append(s.steps, step) }

func okTool(name string) *fakeTool {
	return &fakeTool{
		name: name, domain: DomainMetrics, class: safety.ClassReadOnly,
		schema: NewSchema(map[string]Property{
			"query": {Type: TypeString, Description: "PromQL"},
			"limit": {Type: TypeInteger, Description: "cap", Default: 10, Minimum: Float(1), Maximum: Float(100)},
		}, "query"),
		plan: safety.Call{Class: safety.ClassReadOnly,
			HTTP: &safety.HTTPEffect{Method: "GET", URL: "http://prom:9090/api/v1/query?query=up"}},
		invoke: func(context.Context, map[string]any) (core.Evidence, error) {
			return core.Evidence{Kind: core.EvidenceMetricSeries, Source: "promql", Summary: "up = 1"}, nil
		},
	}
}

func newInvoker(t *testing.T, sink StepSink, mode core.Mode, tools ...Tool) *Invoker {
	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	for _, tool := range tools {
		if err := r.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	in, err := NewInvoker(r, InvokerOptions{Guard: g, Sink: sink, Mode: mode, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func runCtx() context.Context {
	return obs.WithRun(context.Background(), &obs.RunContext{RunID: "run-test", Metrics: obs.NewMetrics()})
}

func TestInvokerHappyPath(t *testing.T) {
	sink := &sliceSink{}
	in := newInvoker(t, sink, core.ModeOffline, okTool("promql.instant"))

	ev, gap := in.Invoke(runCtx(), "promql.instant", map[string]any{"query": "up"})
	if gap != nil {
		t.Fatalf("unexpected gap: %+v", gap)
	}
	if ev.ID == "" || ev.Digest == "" || ev.CollectedAt.IsZero() {
		t.Fatalf("evidence not stamped: %+v", ev)
	}
	if len(sink.steps) != 1 || sink.steps[0].Kind != core.StepToolCall {
		t.Fatalf("step not recorded: %+v", sink.steps)
	}
	if in.Calls() != 1 {
		t.Fatalf("call count = %d", in.Calls())
	}
}

func TestInvokerAppliesSchemaDefaults(t *testing.T) {
	var seen map[string]any
	tl := okTool("t")
	tl.invoke = func(_ context.Context, args map[string]any) (core.Evidence, error) {
		seen = args
		return core.Evidence{Summary: "ok"}, nil
	}
	in := newInvoker(t, nil, core.ModeOffline, tl)
	if _, gap := in.Invoke(runCtx(), "t", map[string]any{"query": "up"}); gap != nil {
		t.Fatalf("gap: %+v", gap)
	}
	if seen["limit"] != 10 {
		t.Fatalf("default not applied: %+v", seen)
	}
}

func TestInvokerValidatesArgs(t *testing.T) {
	in := newInvoker(t, nil, core.ModeOffline, okTool("t"))
	ctx := runCtx()

	for name, args := range map[string]map[string]any{
		"missing required": {},
		"unknown argument": {"query": "up", "nope": 1},
		"below minimum":    {"query": "up", "limit": 0},
		"above maximum":    {"query": "up", "limit": 1000},
		"not an integer":   {"query": "up", "limit": "many"},
	} {
		t.Run(name, func(t *testing.T) {
			_, gap := in.Invoke(ctx, "t", args)
			if gap == nil {
				t.Fatal("invalid arguments accepted")
			}
			if gap.Code != "MAS-8005" || gap.Reason != core.GapRefused {
				t.Fatalf("got %s/%s, want MAS-8005/refused", gap.Code, gap.Reason)
			}
		})
	}
}

func TestInvokerCoercesModelStyleArguments(t *testing.T) {
	var seen map[string]any
	tl := okTool("t")
	tl.invoke = func(_ context.Context, args map[string]any) (core.Evidence, error) {
		seen = args
		return core.Evidence{Summary: "ok"}, nil
	}
	in := newInvoker(t, nil, core.ModeOffline, tl)
	// Models routinely emit numbers as strings; the schema coerces rather than refuses.
	if _, gap := in.Invoke(runCtx(), "t", map[string]any{"query": "up", "limit": "25"}); gap != nil {
		t.Fatalf("string integer refused: %+v", gap)
	}
	if seen["limit"] != 25 {
		t.Fatalf("coercion failed: %+v", seen)
	}
}

func TestUnknownToolBecomesGap(t *testing.T) {
	in := newInvoker(t, nil, core.ModeOffline, okTool("t"))
	_, gap := in.Invoke(runCtx(), "does.not.exist", nil)
	if gap == nil || gap.Code != "MAS-8006" {
		t.Fatalf("got %+v, want MAS-8006", gap)
	}
}

// TestGuardRefusalBecomesGap proves a refusal degrades the run rather than
// ending it, and that the refusal code survives into the report.
func TestGuardRefusalBecomesGap(t *testing.T) {
	tl := okTool("dangerous")
	tl.plan = safety.Call{Class: safety.ClassReadOnly,
		Command: &safety.CommandEffect{Binary: "redis-cli", Args: []string{"FLUSHALL"}}}
	in := newInvoker(t, nil, core.ModeOffline, tl)

	ev, gap := in.Invoke(runCtx(), "dangerous", map[string]any{"query": "x"})
	if gap == nil {
		t.Fatal("a mutating command reached Invoke — NFR-003 violated")
	}
	if gap.Code != "MAS-8001" || gap.Reason != core.GapRefused {
		t.Fatalf("got %s/%s, want MAS-8001/refused", gap.Code, gap.Reason)
	}
	if ev.ID != "" {
		t.Fatal("refused call still produced evidence")
	}
}

func TestPlanErrorBecomesGap(t *testing.T) {
	tl := okTool("t")
	tl.planErr = errs.New("MAS-8005", "query", "unsupported")
	in := newInvoker(t, nil, core.ModeOffline, tl)
	_, gap := in.Invoke(runCtx(), "t", map[string]any{"query": "x"})
	if gap == nil || gap.Code != "MAS-8005" {
		t.Fatalf("got %+v, want MAS-8005", gap)
	}
}

func TestInvokeErrorBecomesUnavailableGap(t *testing.T) {
	tl := okTool("t")
	tl.invoke = func(context.Context, map[string]any) (core.Evidence, error) {
		return core.Evidence{}, errs.New("MAS-4001", "primary", "connection refused")
	}
	in := newInvoker(t, nil, core.ModeOffline, tl)
	_, gap := in.Invoke(runCtx(), "t", map[string]any{"query": "x"})
	if gap == nil || gap.Code != "MAS-4001" || gap.Reason != core.GapUnavailable {
		t.Fatalf("got %+v, want MAS-4001/unavailable", gap)
	}
	if gap.Impact == "" {
		t.Error("gap has no stated impact; the report cannot explain the omission")
	}
}

func TestTimeoutBecomesCeilingCode(t *testing.T) {
	tl := okTool("slow")
	tl.invoke = func(ctx context.Context, _ map[string]any) (core.Evidence, error) {
		<-ctx.Done()
		return core.Evidence{}, ctx.Err()
	}
	g, _ := safety.NewGuard(config.Default().Safety)
	r := NewRegistry()
	_ = r.Register(tl)
	in, err := NewInvoker(r, InvokerOptions{Guard: g, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, gap := in.Invoke(runCtx(), "slow", map[string]any{"query": "x"})
	if gap == nil || gap.Code != "MAS-8010" {
		t.Fatalf("got %+v, want MAS-8010", gap)
	}
}

func TestToolCallBudgetTruncates(t *testing.T) {
	g, _ := safety.NewGuard(config.Default().Safety)
	r := NewRegistry()
	_ = r.Register(okTool("t"))
	in, _ := NewInvoker(r, InvokerOptions{Guard: g, MaxToolCalls: 2})
	ctx := runCtx()

	for i := 0; i < 2; i++ {
		if _, gap := in.Invoke(ctx, "t", map[string]any{"query": "up"}); gap != nil {
			t.Fatalf("call %d refused early: %+v", i, gap)
		}
	}
	_, gap := in.Invoke(ctx, "t", map[string]any{"query": "up"})
	if gap == nil || gap.Code != "MAS-3007" || gap.Reason != core.GapTruncated {
		t.Fatalf("got %+v, want MAS-3007/truncated", gap)
	}
}

func TestOnlineToolRefusedInOfflineMode(t *testing.T) {
	tl := okTool("cluster.pods")
	tl.needMode = core.ModeOnline
	in := newInvoker(t, nil, core.ModeOffline, tl)
	_, gap := in.Invoke(runCtx(), "cluster.pods", map[string]any{"query": "x"})
	if gap == nil || gap.Reason != core.GapNotConfigured {
		t.Fatalf("got %+v, want a not_configured gap", gap)
	}

	online := newInvoker(t, nil, core.ModeOnline, okTool("cluster.pods2"))
	if _, gap := online.Invoke(runCtx(), "cluster.pods2", map[string]any{"query": "x"}); gap != nil {
		t.Fatalf("online mode still refused: %+v", gap)
	}
}

func TestRegistryRejectsDuplicatesAndMutatingTools(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(okTool("t")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(okTool("t")); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	mut := okTool("m")
	mut.class = safety.ClassMutating
	if err := r.Register(mut); !errors.Is(err, err) || errs.CodeOf(r.Register(mut)) != "MAS-8001" {
		t.Fatal("a mutating tool was accepted into the registry")
	}
}

func TestRegistryDomainsAndDefinitions(t *testing.T) {
	r := NewRegistry()
	m := okTool("promql.instant")
	l := okTool("loki.query")
	l.domain = DomainLogs
	_ = r.Register(m)
	_ = r.Register(l)

	if names := r.Names(); len(names) != 2 || names[0] != "loki.query" {
		t.Fatalf("Names not sorted: %v", names)
	}
	if got := r.InDomains(DomainLogs); len(got) != 1 || got[0].Name() != "loki.query" {
		t.Fatalf("InDomains wrong: %v", got)
	}
	defs := r.Definitions()
	if len(defs) != 2 || defs[0].Schema.Type != TypeObject {
		t.Fatalf("Definitions wrong: %+v", defs)
	}
	if got := r.DefinitionsFor([]string{"promql.instant"}); len(got) != 1 {
		t.Fatalf("DefinitionsFor wrong: %+v", got)
	}
}

func TestInvokerRequiresGuard(t *testing.T) {
	if _, err := NewInvoker(NewRegistry(), InvokerOptions{}); err == nil {
		t.Fatal("an invoker was constructed without a guard — Art. IV.1 requires no such path")
	}
}

func TestInvokerRedactsEvidence(t *testing.T) {
	tl := okTool("t")
	tl.invoke = func(context.Context, map[string]any) (core.Evidence, error) {
		return core.Evidence{
			Summary: "connected with password=hunter2hunter2",
			Query:   "http://u:secretpassword@host/api",
			Payload: map[string]any{"api_key": "sk-abcdefghijklmnop"},
		}, nil
	}
	in := newInvoker(t, nil, core.ModeOffline, tl)
	ev, gap := in.Invoke(runCtx(), "t", map[string]any{"query": "x"})
	if gap != nil {
		t.Fatal(gap)
	}
	blob := ev.Summary + ev.Query
	if payload, ok := ev.Payload.(map[string]any); ok {
		blob += payload["api_key"].(string)
	}
	for _, leaked := range []string{"hunter2hunter2", "secretpassword", "sk-abcdefghijklmnop"} {
		if contains(blob, leaked) {
			t.Errorf("evidence leaked %q: %s", leaked, blob)
		}
	}
}

func contains(hay, needle string) bool {
	return len(needle) > 0 && len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
