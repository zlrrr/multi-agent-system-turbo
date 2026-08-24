package rules

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/promql"
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// metricStub answers promql tool calls from a script keyed by query substring,
// so a playbook can be driven through any scenario without a metrics backend.
type metricStub struct {
	name     string
	series   map[string][]float64
	fail     map[string]bool
	llmCalls int
	queries  []string
}

func (m *metricStub) Name() string         { return m.name }
func (m *metricStub) Description() string  { return "stub metrics" }
func (m *metricStub) Domain() tool.Domain  { return tool.DomainMetrics }
func (m *metricStub) Safety() safety.Class { return safety.ClassReadOnly }
func (m *metricStub) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"query": {Type: tool.TypeString, Description: "PromQL"},
		"from":  {Type: tool.TypeString, Description: "start"},
		"to":    {Type: tool.TypeString, Description: "end"},
		"step":  {Type: tool.TypeString, Description: "step"},
		"at":    {Type: tool.TypeString, Description: "instant"},
	}, "query")
}
func (m *metricStub) Plan(map[string]any) (safety.Call, error) {
	return safety.Call{
		Class: safety.ClassReadOnly,
		HTTP:  &safety.HTTPEffect{Method: "POST", URL: "http://prom:9090/api/v1/query"},
	}, nil
}
func (m *metricStub) Invoke(_ context.Context, args map[string]any) (core.Evidence, error) {
	q := tool.Str(args, "query", "")
	m.queries = append(m.queries, q)
	for key := range m.fail {
		if strings.Contains(q, key) {
			return core.Evidence{}, errs.New("MAS-4001", "primary", "connection refused")
		}
	}
	for key, values := range m.series {
		if !strings.Contains(q, key) {
			continue
		}
		var pts []promql.Sample
		base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
		s := promql.Series{Metric: map[string]string{"instance": "redis-0"}}
		for i, v := range values {
			pts = append(pts, promql.Sample{At: base.Add(time.Duration(i) * time.Minute), Value: v})
		}
		s.Points = pts
		s.Count = len(values)
		if len(values) > 0 {
			s.Last = values[len(values)-1]
			s.Min, s.Max = values[0], values[0]
			sum := 0.0
			for _, v := range values {
				if v < s.Min {
					s.Min = v
				}
				if v > s.Max {
					s.Max = v
				}
				sum += v
			}
			s.Avg = sum / float64(len(values))
		}
		res := promql.Result{Query: q, ResultType: "matrix", Series: []promql.Series{s}}
		return core.Evidence{
			Kind: core.EvidenceMetricSeries, Source: "promql:stub", Query: q,
			Payload: res, Summary: res.Summary(),
		}, nil
	}
	res := promql.Result{Query: q, ResultType: "matrix"}
	return core.Evidence{
		Kind: core.EvidenceMetricSeries, Source: "promql:stub", Query: q,
		Payload: res, Summary: res.Summary(),
	}, nil
}

func newEngine(t *testing.T, stub *metricStub) (*Engine, *knowledge.Library) {
	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	for _, name := range []string{"promql.instant", "promql.range", "promql.series"} {
		clone := *stub
		clone.name = name
		s := stub
		reg.MustRegister(&namedStub{Tool: s, name: name})
	}
	inv, err := tool.NewInvoker(reg, tool.InvokerOptions{Guard: g, Mode: core.ModeOffline, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := knowledge.LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(inv, lib), lib
}

// namedStub lets one backing stub serve several tool names.
type namedStub struct {
	tool.Tool
	name string
}

func (n *namedStub) Name() string { return n.name }

func redisPack(t *testing.T, lib *knowledge.Library) *knowledge.Pack {
	t.Helper()
	p, err := lib.For(core.KindRedis, "7.2.4")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlaybookHappyPath(t *testing.T) {
	stub := &metricStub{series: map[string][]float64{
		"redis_memory_used_bytes": {900, 950, 980},
		"redis_memory_max_bytes":  {1000},
		"redis_evicted_keys":      {0, 5, 12},
		"keyspace_hits":           {0.95, 0.9, 0.5},
	}}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)
	pb, ok := pack.Playbook("redis.memory-pressure")
	if !ok {
		t.Fatal("playbook missing")
	}

	out, err := e.Run(context.Background(), pack, pb, Input{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) == 0 {
		t.Fatal("980/1000 of maxmemory produced no finding")
	}
	var sawPressure, sawEviction bool
	for _, f := range out.Findings {
		if strings.Contains(f.Statement, "90%") {
			sawPressure = true
		}
		if strings.Contains(strings.ToLower(f.Statement), "evict") {
			sawEviction = true
		}
		if len(f.Evidence) == 0 {
			t.Errorf("finding %q cites no evidence", f.Statement)
		}
		if f.Origin == "" || !strings.HasPrefix(f.Origin, "rule:") {
			t.Errorf("finding %q has origin %q; it must be traceable to its rule", f.Statement, f.Origin)
		}
	}
	if !sawPressure {
		t.Error("memory pressure was not detected")
	}
	if !sawEviction {
		t.Error("eviction was not detected")
	}
	if len(out.Conclusions) == 0 {
		t.Error("no failure mode was concluded")
	}
}

func TestPlaybookHealthySystemProducesPasses(t *testing.T) {
	stub := &metricStub{series: map[string][]float64{
		"redis_memory_used_bytes": {100, 110, 120},
		"redis_memory_max_bytes":  {1000},
		"redis_evicted_keys":      {0, 0, 0},
		"keyspace_hits":           {0.99, 0.99, 0.99},
	}}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)
	pb, _ := pack.Playbook("redis.memory-pressure")

	out, err := e.Run(context.Background(), pack, pb, Input{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 0 {
		t.Fatalf("a healthy system produced findings: %+v", out.Findings)
	}
	if len(out.ChecksPassed) < 2 {
		t.Fatalf("checks that passed must be reported so the operator knows what was ruled out: %v", out.ChecksPassed)
	}
	if len(out.Conclusions) != 0 {
		t.Fatalf("a healthy system concluded a failure mode: %v", out.Conclusions)
	}
}

// TestZeroLLMCalls is the FR-008 test: the deterministic layer must cost nothing.
func TestZeroLLMCalls(t *testing.T) {
	stub := &metricStub{series: map[string][]float64{"redis_memory_used_bytes": {1}}}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)

	out := e.RunAll(context.Background(), pack, "latency spike with evictions", Input{Language: "en"})
	if out.LLMCalls != 0 || stub.llmCalls != 0 {
		t.Fatalf("the deterministic phase made %d model calls", out.LLMCalls+stub.llmCalls)
	}
	if len(stub.queries) == 0 {
		t.Fatal("no metrics were queried at all")
	}
}

// TestUnder2Seconds is the NFR-002 gate.
func TestUnder2Seconds(t *testing.T) {
	stub := &metricStub{series: map[string][]float64{
		"redis_memory_used_bytes": {900, 980},
		"redis_memory_max_bytes":  {1000},
	}}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)

	start := time.Now()
	e.RunAll(context.Background(), pack, "latency spike with evictions and oom", Input{Language: "en"})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("deterministic phase took %s, above the 2s budget", elapsed)
	}
}

// TestMissingEvidenceSkips proves FR-013 at the rule layer: an unavailable
// source must never be mistaken for a healthy measurement.
func TestMissingEvidenceSkips(t *testing.T) {
	stub := &metricStub{
		series: map[string][]float64{"redis_memory_max_bytes": {1000}},
		fail:   map[string]bool{"redis_memory_used_bytes": true},
	}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)
	pb, _ := pack.Playbook("redis.memory-pressure")

	out, err := e.Run(context.Background(), pack, pb, Input{Language: "en"})
	if err != nil {
		t.Fatalf("a failed collection must not fail the playbook: %v", err)
	}
	if len(out.Gaps) == 0 {
		t.Fatal("the failed collection was not recorded as a gap")
	}
	sawCollectionGap, sawSkipGap := false, false
	for _, g := range out.Gaps {
		if g.Code == "MAS-4001" {
			sawCollectionGap = true
		}
		if strings.Contains(g.Detail, "skipped") {
			sawSkipGap = true
			if g.Impact == "" {
				t.Error("a skipped check must say what it leaves unknown")
			}
		}
	}
	if !sawCollectionGap {
		t.Error("the collection failure code was lost")
	}
	if !sawSkipGap {
		t.Error("the dependent check was not recorded as skipped")
	}
	for _, f := range out.Findings {
		if strings.Contains(f.Statement, "90%") {
			t.Fatal("a check whose input was missing produced a positive finding")
		}
	}
	for _, c := range out.ChecksPassed {
		if strings.Contains(strings.ToLower(c), "below maxmemory") {
			t.Fatal("a missing measurement was reported as a passed check")
		}
	}
}

func TestExpressionErrorsCoded(t *testing.T) {
	pack := &knowledge.Pack{
		APIVersion: knowledge.APIVersion, Kind: knowledge.Kind,
		Metadata: knowledge.Metadata{Middleware: "testware", Name: "t", Version: "1.0.0"},
		Signals: []knowledge.Signal{{ID: "up", PromQL: "testware_up", Unit: "bool",
			Description: knowledge.Text{EN: "up", ZH: "在线"}}},
		Playbooks: []knowledge.Playbook{{
			ID: "t.bad", Title: knowledge.Text{EN: "bad", ZH: "坏"},
			Steps: []knowledge.Step{
				{ID: "collect", Collect: &knowledge.Collect{
					Tool: "promql.instant", Args: map[string]any{"query": "{{signal:up}}"}, As: "up"}},
				{ID: "eval", Evaluate: "up.latest +++ 1"},
			},
		}},
	}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	stub := &metricStub{series: map[string][]float64{"testware_up": {1}}}
	e, _ := newEngine(t, stub)

	_, err := e.Run(context.Background(), pack, &pack.Playbooks[0], Input{Language: "en"})
	if errs.CodeOf(err) != "MAS-5010" {
		t.Fatalf("got %v (%s), want MAS-5010", err, errs.CodeOf(err))
	}
}

func TestNonBooleanExpressionIsCoded(t *testing.T) {
	pack := &knowledge.Pack{
		APIVersion: knowledge.APIVersion, Kind: knowledge.Kind,
		Metadata: knowledge.Metadata{Middleware: "testware", Name: "t", Version: "1.0.0"},
		Signals: []knowledge.Signal{{ID: "up", PromQL: "testware_up", Unit: "bool",
			Description: knowledge.Text{EN: "up", ZH: "在线"}}},
		Playbooks: []knowledge.Playbook{{
			ID: "t.bad", Title: knowledge.Text{EN: "bad", ZH: "坏"},
			Steps: []knowledge.Step{
				{ID: "collect", Collect: &knowledge.Collect{
					Tool: "promql.instant", Args: map[string]any{"query": "{{signal:up}}"}, As: "up"}},
				{ID: "eval", Evaluate: "up.latest + 1"},
			},
		}},
	}
	stub := &metricStub{series: map[string][]float64{"testware_up": {1}}}
	e, _ := newEngine(t, stub)
	_, err := e.Run(context.Background(), pack, &pack.Playbooks[0], Input{Language: "en"})
	if code := errs.CodeOf(err); code != "MAS-5010" && code != "MAS-5011" {
		t.Fatalf("got %v (%s), want a MAS-501x expression error", err, code)
	}
}

func TestUnknownToolBecomesGapNotFailure(t *testing.T) {
	pack := &knowledge.Pack{
		APIVersion: knowledge.APIVersion, Kind: knowledge.Kind,
		Metadata: knowledge.Metadata{Middleware: "testware", Name: "t", Version: "1.0.0"},
		Playbooks: []knowledge.Playbook{{
			ID: "t.pb", Title: knowledge.Text{EN: "x", ZH: "x"},
			Steps: []knowledge.Step{
				{ID: "collect", Collect: &knowledge.Collect{
					Tool: "does.not.exist", Args: map[string]any{}, As: "v"}},
			},
		}},
	}
	e, _ := newEngine(t, &metricStub{})
	out, err := e.Run(context.Background(), pack, &pack.Playbooks[0], Input{})
	if err != nil {
		t.Fatalf("an unknown tool must degrade, not fail: %v", err)
	}
	if len(out.Gaps) != 1 || out.Gaps[0].Code != "MAS-8006" {
		t.Fatalf("gaps = %+v, want one MAS-8006", out.Gaps)
	}
}

func TestStepBudgetTruncates(t *testing.T) {
	stub := &metricStub{series: map[string][]float64{"redis_": {1}}}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)
	pb, _ := pack.Playbook("redis.memory-pressure")

	out, err := e.Run(context.Background(), pack, pb, Input{MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Fatal("the step budget did not truncate the playbook")
	}
	found := false
	for _, g := range out.Gaps {
		if g.Code == "MAS-5013" {
			found = true
		}
	}
	if !found {
		t.Fatalf("truncation was not reported with MAS-5013: %+v", out.Gaps)
	}
}

func TestRunAllMergesAndStampsIDs(t *testing.T) {
	stub := &metricStub{series: map[string][]float64{
		"redis_memory_used_bytes": {990},
		"redis_memory_max_bytes":  {1000},
		"redis_up":                {1},
		"redis_evicted_keys":      {3},
	}}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)

	out := e.RunAll(context.Background(), pack, "oom and eviction", Input{Language: "en"})
	if len(out.Findings) < 2 {
		t.Fatalf("expected findings from several playbooks, got %d", len(out.Findings))
	}
	seen := map[string]bool{}
	for _, f := range out.Findings {
		if f.ID == "" {
			t.Error("a finding has no id")
		}
		if seen[f.ID] {
			t.Errorf("duplicate finding id %s", f.ID)
		}
		seen[f.ID] = true
	}
	if out.TopConfidence() <= 0 {
		t.Error("TopConfidence returned nothing for a run with findings")
	}
}

func TestRunAllIsDeterministic(t *testing.T) {
	run := func() []string {
		stub := &metricStub{series: map[string][]float64{
			"redis_memory_used_bytes": {990},
			"redis_memory_max_bytes":  {1000},
			"redis_evicted_keys":      {3},
		}}
		e, lib := newEngine(t, stub)
		out := e.RunAll(context.Background(), redisPack(t, lib), "oom eviction latency", Input{Language: "en"})
		var got []string
		for _, f := range out.Findings {
			got = append(got, f.ID+"|"+f.Origin+"|"+f.Statement)
		}
		return got
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("run lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("run %d differs:\n  %s\n  %s", i, a[i], b[i])
		}
	}
}

func TestLanguageSelectsFindingText(t *testing.T) {
	stub := &metricStub{series: map[string][]float64{
		"redis_memory_used_bytes": {990}, "redis_memory_max_bytes": {1000},
	}}
	e, lib := newEngine(t, stub)
	pack := redisPack(t, lib)
	pb, _ := pack.Playbook("redis.memory-pressure")

	en, _ := e.Run(context.Background(), pack, pb, Input{Language: "en"})
	zh, _ := e.Run(context.Background(), pack, pb, Input{Language: "zh"})
	if len(en.Findings) == 0 || len(zh.Findings) == 0 {
		t.Fatal("no findings to compare")
	}
	if en.Findings[0].Statement == zh.Findings[0].Statement {
		t.Fatal("the Chinese finding is identical to the English one")
	}
}

func TestMetricViewAggregates(t *testing.T) {
	res := promql.Result{Series: []promql.Series{
		{Metric: map[string]string{"instance": "a"}, Last: 5, Min: 1, Max: 9, Avg: 4, Count: 3,
			Points: []promql.Sample{{Value: 1}, {Value: 9}, {Value: 5}}},
		{Metric: map[string]string{"instance": "b"}, Last: 2, Min: 2, Max: 2, Avg: 2, Count: 1,
			Points: []promql.Sample{{Value: 2}}},
	}}
	v := metricView(res, "summary")
	if v.Latest != 5 {
		t.Errorf("Latest = %v; it must be the maximum across series so 'any instance over the line' works", v.Latest)
	}
	if v.LatestMin != 2 {
		t.Errorf("LatestMin = %v", v.LatestMin)
	}
	if v.Min != 1 || v.Max != 9 {
		t.Errorf("Min/Max = %v/%v", v.Min, v.Max)
	}
	if v.Series != 2 || v.Count != 4 {
		t.Errorf("Series/Count = %d/%d", v.Series, v.Count)
	}
	if v.ByLabel["a"] != 5 || v.ByLabel["b"] != 2 {
		t.Errorf("ByLabel = %v", v.ByLabel)
	}
	if v.Empty {
		t.Error("a populated result reported Empty")
	}
}

func TestMetricViewEmpty(t *testing.T) {
	v := metricView(promql.Result{}, "none")
	if !v.Empty || v.Latest != 0 || v.Min != 0 || v.Max != 0 {
		t.Fatalf("an empty result must present as zeroed, not infinite: %+v", v)
	}
}

func TestIdentifiersFindsSlotNames(t *testing.T) {
	got := identifiers("used.latest / maxmem.latest > 0.9 and not evicted.empty")
	want := map[string]bool{"used": true, "maxmem": true, "evicted": true}
	for _, id := range got {
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("identifiers missed %v (found %v)", want, got)
	}
	for _, id := range got {
		if id == "latest" || id == "empty" {
			t.Fatalf("identifiers returned a field name %q rather than a slot name", id)
		}
	}
}

func TestSelectOrdersMostSpecificFirst(t *testing.T) {
	e, lib := newEngine(t, &metricStub{})
	pack := redisPack(t, lib)
	got := e.Select(pack, "oom eviction memory")
	if len(got) == 0 {
		t.Fatal("no playbook selected")
	}
	if got[0].ID != "redis.memory-pressure" {
		t.Fatalf("first = %s, want redis.memory-pressure", got[0].ID)
	}
}

func TestHelpersAreSandboxed(t *testing.T) {
	h := helpers()
	for _, forbidden := range []string{"exec", "open", "read", "http", "env", "os", "file"} {
		if _, present := h[forbidden]; present {
			t.Errorf("the expression environment exposes %q", forbidden)
		}
	}
	if !h["contains"].(func(string, string) bool)("OOM command not allowed", "oom") {
		t.Error("contains should be case-insensitive")
	}
	if h["ratio"].(func(float64, float64) float64)(1, 0) != 0 {
		t.Error("ratio must not divide by zero")
	}
}
