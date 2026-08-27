package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func sampleReport() *Report {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	ev := Evidence{
		ID: "ev-1", Kind: EvidenceMetricSeries, Source: "promql",
		Query: "redis_memory_used_bytes", CollectedAt: now,
		Payload: map[string]any{"last": 1024.0}, Summary: "used memory 1 KiB",
	}
	ev.ComputeDigest()
	return &Report{
		Schema: ReportSchema, RunID: "run-1", GeneratedAt: now,
		Target:   Target{ID: "redis-prod", Kind: KindRedis, Env: EnvBinding{Name: "prod", Type: "kubernetes"}},
		Request:  DiagnoseRequest{Target: "redis-prod", Symptom: "latency", Window: Window{From: now.Add(-time.Hour), To: now}, Mode: ModeOffline},
		Topology: "supervisor", Summary: "memory pressure",
		Hypotheses: []Hypothesis{
			{ID: "h-1", Statement: "eviction storm", Status: HypothesisSupported, Confidence: 0.8, Supporting: []string{"ev-1"}},
			{ID: "h-2", Statement: "network", Status: HypothesisRefuted, Confidence: 0.9},
			{ID: "h-3", Statement: "fork latency", Status: HypothesisProposed, Confidence: 0.5},
		},
		Findings: []Finding{
			{ID: "f-1", Origin: "rule:redis.memory-pressure/eval", Severity: SeverityMinor, Statement: "minor", Confidence: 0.4},
			{ID: "f-2", Origin: "agent:correlator", Severity: SeverityCritical, Statement: "critical", Confidence: 0.9},
		},
		ChecksPassed:    []string{"connection churn within norms"},
		Gaps:            []Gap{{ID: "gap-1", Intent: "loki query", Reason: GapUnavailable, Code: "MAS-4101"}},
		Recommendations: []Recommendation{NewRecommendation("raise maxmemory", RiskLow, "headroom", "ev-1")},
		Evidence:        []Evidence{ev},
		Usage:           Usage{LLMCalls: 3, ToolCalls: 7},
	}
}

func TestReportRoundTrip(t *testing.T) {
	in := sampleReport()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Report
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Schema != ReportSchema || out.RunID != in.RunID {
		t.Fatalf("identity lost: %+v", out)
	}
	if len(out.Hypotheses) != 3 || len(out.Evidence) != 1 || len(out.Recommendations) != 1 {
		t.Fatalf("collections lost: %d/%d/%d", len(out.Hypotheses), len(out.Evidence), len(out.Recommendations))
	}
	if !out.Recommendations[0].Advisory {
		t.Fatal("advisory flag lost across the wire — CON-003 violated")
	}
}

func TestValidateAcceptsWellFormed(t *testing.T) {
	if err := sampleReport().Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
}

func TestValidateRejectsInvariantBreaches(t *testing.T) {
	cases := map[string]func(*Report){
		"bad schema":            func(r *Report) { r.Schema = "report/v0" },
		"empty run id":          func(r *Report) { r.RunID = "" },
		"duplicate evidence id": func(r *Report) { r.Evidence = append(r.Evidence, r.Evidence[0]) },
		"confidence too high":   func(r *Report) { r.Hypotheses[0].Confidence = 1.5 },
		"confidence negative":   func(r *Report) { r.Findings[0].Confidence = -0.1 },
		"non-advisory":          func(r *Report) { r.Recommendations[0].Advisory = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := sampleReport()
			mutate(r)
			err := r.Validate()
			if err == nil {
				t.Fatal("invariant breach accepted")
			}
			if errs.CodeOf(err) != "MAS-9001" {
				t.Fatalf("uncoded or wrong code: %v", err)
			}
		})
	}
}

func TestSortHypothesesIsDeterministicAndRanks(t *testing.T) {
	r := sampleReport()
	r.SortHypotheses()
	if r.Hypotheses[0].ID != "h-1" {
		t.Fatalf("highest-confidence non-refuted should lead, got %s", r.Hypotheses[0].ID)
	}
	if r.Hypotheses[2].Status != HypothesisRefuted {
		t.Fatalf("refuted hypothesis should sort last, got %s", r.Hypotheses[2].ID)
	}
	for i, h := range r.Hypotheses {
		if h.Rank != i+1 {
			t.Fatalf("rank %d at position %d", h.Rank, i)
		}
	}
	// Sorting twice must not change the order (NFR-010).
	before := r.Hypotheses[0].ID + r.Hypotheses[1].ID + r.Hypotheses[2].ID
	r.SortHypotheses()
	after := r.Hypotheses[0].ID + r.Hypotheses[1].ID + r.Hypotheses[2].ID
	if before != after {
		t.Fatalf("sort is not idempotent: %s then %s", before, after)
	}
}

func TestSortFindingsBySeverity(t *testing.T) {
	r := sampleReport()
	r.SortFindings()
	if r.Findings[0].Severity != SeverityCritical {
		t.Fatalf("critical finding must lead, got %s", r.Findings[0].Severity)
	}
}

func TestEvidenceDigestIsStableAndContentSensitive(t *testing.T) {
	a := Evidence{Kind: EvidenceMetricSeries, Query: "q", Payload: map[string]any{"v": 1}}
	b := a
	a.ComputeDigest()
	b.ComputeDigest()
	if a.Digest != b.Digest {
		t.Fatal("identical evidence produced different digests")
	}
	c := a
	c.Payload = map[string]any{"v": 2}
	c.ComputeDigest()
	if c.Digest == a.Digest {
		t.Fatal("different payloads produced the same digest")
	}
}

func TestWindowValidate(t *testing.T) {
	now := time.Now()
	if err := (Window{From: now.Add(-time.Hour), To: now}).Validate(); err != nil {
		t.Fatalf("valid window rejected: %v", err)
	}
	for name, w := range map[string]Window{
		"zero from": {To: now},
		"zero to":   {From: now},
		"inverted":  {From: now, To: now.Add(-time.Hour)},
		"empty":     {From: now, To: now},
	} {
		if err := w.Validate(); errs.CodeOf(err) != "MAS-1010" {
			t.Errorf("%s: got %v, want MAS-1010", name, err)
		}
	}
}

func TestUsageAdd(t *testing.T) {
	u := Usage{LLMCalls: 1, ToolCalls: 2, PromptTokens: 10, Cost: KnownCost(0.2)}
	u.Add(Usage{LLMCalls: 2, ToolCalls: 3, CompletionTokens: 5, Cost: KnownCost(0.1)})
	if u.LLMCalls != 3 || u.ToolCalls != 5 || u.PromptTokens != 10 || u.CompletionTokens != 5 {
		t.Fatalf("accumulation wrong: %+v", u)
	}
	if !u.Cost.Known || u.Cost.USD < 0.29 || u.Cost.USD > 0.31 {
		t.Fatalf("cost = %+v, want a known 0.30", u.Cost)
	}
}

// TestCostAddIsUnknownIfEitherIs is where an understatement would hide. A run
// that priced two models and not a third does not know its total, and reporting
// the priced part as though it were the whole would quietly understate it.
func TestCostAddIsUnknownIfEitherIs(t *testing.T) {
	known := KnownCost(0.5)
	unpriced := UnpricedCost("claude-opus-5")

	sum := known.Add(unpriced)
	if sum.Known {
		t.Error("a sum containing an unpriced model was reported as known")
	}
	if sum.USD != 0.5 {
		t.Errorf("USD = %v; the priced part must survive so it can be reported", sum.USD)
	}
	if len(sum.Unpriced) != 1 || sum.Unpriced[0] != "claude-opus-5" {
		t.Errorf("unpriced = %v; the caller must be able to name what is missing", sum.Unpriced)
	}
	if !sum.Partial() {
		t.Error("a partly priced run must be distinguishable from a wholly unpriced one")
	}

	// Order must not matter, and the names must not duplicate.
	other := unpriced.Add(known).Add(UnpricedCost("claude-opus-5"))
	if len(other.Unpriced) != 1 {
		t.Errorf("unpriced = %v, want the names deduplicated", other.Unpriced)
	}

	// Two known costs stay known, including when both are zero: a self-hosted
	// model really is free, and saying so is not the same as saying nothing.
	free := KnownCost(0).Add(KnownCost(0))
	if !free.Known || free.Partial() {
		t.Errorf("a measured zero = %+v, want known and not partial", free)
	}
}

// TestCostNeverRendersUnknownAsANumber is the property the type exists for.
func TestCostNeverRendersUnknownAsANumber(t *testing.T) {
	for name, c := range map[string]Cost{
		"nothing priced":     {},
		"one model unpriced": UnpricedCost("claude-opus-5"),
		"partly priced":      KnownCost(0.5).Add(UnpricedCost("qwen2.5")),
	} {
		t.Run(name, func(t *testing.T) {
			got := c.String()
			if !strings.Contains(got, "not priced") {
				t.Errorf("%q does not say the cost is unknown", got)
			}
			if got == "$0.0000" {
				t.Errorf("an unknown cost rendered as %q, which an operator would believe", got)
			}
		})
	}
	if got := KnownCost(0).String(); got != "$0.0000" {
		t.Errorf("a measured zero rendered as %q, want $0.0000", got)
	}
}

func TestNewRecommendationSetsAdvisory(t *testing.T) {
	if !NewRecommendation("x", RiskHigh, "y").Advisory {
		t.Fatal("NewRecommendation must set Advisory — CON-003")
	}
}

func TestRunRecordSummarise(t *testing.T) {
	rec := &RunRecord{ID: "run-1", Status: RunCompleted, Request: DiagnoseRequest{Target: "t", Symptom: "s", Topology: "single"}, Report: sampleReport()}
	s := rec.Summarise()
	if s.ID != "run-1" || s.Target != "t" || s.Hypotheses != 3 {
		t.Fatalf("bad summary: %+v", s)
	}
}
