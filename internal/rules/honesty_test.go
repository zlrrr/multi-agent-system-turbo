package rules

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
)

// syntheticEngine runs one hand-written playbook against the given stubs, with
// no shipped pack involved: these tests are about the engine, not the knowledge.
func syntheticEngine(t *testing.T, metrics tool.Tool, logs tool.Tool) *Engine {
	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	for _, name := range []string{"promql.instant", "promql.range"} {
		reg.MustRegister(&namedStub{Tool: metrics, name: name})
	}
	if logs != nil {
		reg.MustRegister(logs)
	}
	inv, err := tool.NewInvoker(reg, tool.InvokerOptions{
		Guard: g, Mode: core.ModeOffline, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(inv, nil)
}

// TestRegexLiteralsAreNotSlotReferences pins the fix for a bug that silently
// disabled every log-pattern check in every pack: the words inside a regular
// expression passed to countMatching were read as slot names, none of which had
// been collected, so the engine skipped the check and recorded a gap instead of
// running it. A skipped check looks harmless in a report — which is exactly why
// this needs a test rather than a comment.
func TestRegexLiteralsAreNotSlotReferences(t *testing.T) {
	pack := &knowledge.Pack{
		APIVersion: knowledge.APIVersion, Kind: knowledge.Kind,
		Metadata: knowledge.Metadata{Middleware: "testware", Name: "t", Version: "1.0.0"},
		Playbooks: []knowledge.Playbook{{
			ID: "t.logs", Title: knowledge.Text{EN: "logs", ZH: "日志"},
			Steps: []knowledge.Step{
				{ID: "collect", Collect: &knowledge.Collect{
					Tool: "loki.query", Args: map[string]any{"query": `{job="t"}`}, As: "logs"}},
				{ID: "eval",
					Evaluate: "not logs.empty and countMatching(logs.lines, 'NotEnoughBookies|Not enough non-faulty bookies') > 0",
					OnTrue: &knowledge.Branch{Finding: &knowledge.StepFinding{
						Severity: "critical", Confidence: 0.9,
						Statement: knowledge.Text{EN: "bookie shortage", ZH: "bookie 不足"},
					}},
				},
			},
		}},
	}
	logs := &logStub{lines: []string{"10:00 ERROR NotEnoughBookiesException ensemble=3"}}
	e := syntheticEngine(t, &signalStub{}, logs)

	out, err := e.Run(context.Background(), pack, &pack.Playbooks[0], Input{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range out.Gaps {
		if strings.Contains(g.Detail, "no data for") {
			t.Fatalf("the words inside the regex were read as slot names: %s", g.Detail)
		}
	}
	if len(out.Findings) != 1 {
		t.Fatalf("the log check did not run: findings=%+v gaps=%+v", out.Findings, out.Gaps)
	}
}

// TestIdentifiersIgnoreQuotedText is the unit-level companion: whatever quoting
// a pack author uses, the contents are data.
func TestIdentifiersIgnoreQuotedText(t *testing.T) {
	cases := map[string][]string{
		`countMatching(logs.lines, 'insufficient memory|OutOfMemory') > 0`: {"logs", "countMatching"},
		`contains(a.summary, "some words here")`:                           {"a", "contains"},
		`b.max > 1 and matches(c.text, 'x y z')`:                           {"b", "and", "matches", "c"},
	}
	for expression, want := range cases {
		got := map[string]bool{}
		for _, id := range identifiers(expression) {
			got[id] = true
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("%q: identifier %q was not found, got %v", expression, w, sortedKeys(got))
			}
		}
		for id := range got {
			if strings.Contains(id, " ") {
				continue
			}
			ok := false
			for _, w := range want {
				if id == w {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%q: %q was read as an identifier but it is inside a string literal",
					expression, id)
			}
		}
	}
}

// TestEmptyMetricIsNotReportedAsPassed is the honesty gate (Constitution Art. V,
// FR-013). A metric that returned no series compares as zero, which reads as
// healthy; a check that came out false only because its input was never
// measured must be recorded as a gap, never as a check that passed.
func TestEmptyMetricIsNotReportedAsPassed(t *testing.T) {
	pack := &knowledge.Pack{
		APIVersion: knowledge.APIVersion, Kind: knowledge.Kind,
		Metadata: knowledge.Metadata{Middleware: "testware", Name: "t", Version: "1.0.0"},
		Signals: []knowledge.Signal{{ID: "usage", PromQL: "testware_usage_ratio", Unit: "ratio",
			Description: knowledge.Text{EN: "usage", ZH: "使用率"}}},
		Playbooks: []knowledge.Playbook{{
			ID: "t.usage", Title: knowledge.Text{EN: "usage", ZH: "使用率"},
			Steps: []knowledge.Step{
				{ID: "collect", Collect: &knowledge.Collect{
					Tool: "promql.range", Args: map[string]any{"query": "{{signal:usage}}"}, As: "usage"}},
				{ID: "eval", Evaluate: "not usage.empty and usage.max > 0.85",
					OnTrue: &knowledge.Branch{Finding: &knowledge.StepFinding{
						Severity: "critical", Confidence: 0.9,
						Statement: knowledge.Text{EN: "usage over 85%", ZH: "使用率超过 85%"},
					}},
					OnFalse: &knowledge.Branch{Pass: knowledge.Text{
						EN: "usage is within normal bounds", ZH: "使用率处于正常范围"}},
				},
				{ID: "conclude", Conclude: &knowledge.Conclude{
					FailureMode: "saturation", When: "not usage.empty and usage.max > 0.85"}},
			},
		}},
		FailureModes: []knowledge.FailureMode{{
			ID: "saturation", Severity: "major",
			Title:       knowledge.Text{EN: "saturation", ZH: "饱和"},
			Explanation: knowledge.Text{EN: "at the limit", ZH: "已达上限"},
			Indicators:  []string{"usage high"},
			Recommendations: []knowledge.Advice{{Risk: "low",
				Statement: knowledge.Text{EN: "check the limit", ZH: "检查上限"}}},
		}},
	}

	// The stub knows no query, so every collection succeeds and returns no series.
	e := syntheticEngine(t, &signalStub{byQuery: map[string][]float64{}}, nil)
	out, err := e.Run(context.Background(), pack, &pack.Playbooks[0], Input{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range out.ChecksPassed {
		t.Errorf("a measurement that returned no series was reported as a passed check: %q", c)
	}
	if len(out.Findings) != 0 {
		t.Errorf("an unmeasured check produced findings: %+v", out.Findings)
	}
	if len(out.Conclusions) != 0 {
		t.Errorf("an unmeasured check concluded a failure mode: %v", out.Conclusions)
	}
	sawGap := false
	for _, g := range out.Gaps {
		if g.Code == "MAS-5015" {
			sawGap = true
			if g.Impact == "" {
				t.Error("a skipped check must state what it leaves unknown")
			}
		}
	}
	if !sawGap {
		t.Errorf("the unperformed check was not recorded as a gap: %+v", out.Gaps)
	}
}

// TestDeliberateEmptyReadingStillFires guards the other direction: an author who
// writes `up.empty or up.latest < 1` means an absent metric to read as "down",
// and that reading must survive the honesty gate above.
func TestDeliberateEmptyReadingStillFires(t *testing.T) {
	pack := &knowledge.Pack{
		APIVersion: knowledge.APIVersion, Kind: knowledge.Kind,
		Metadata: knowledge.Metadata{Middleware: "testware", Name: "t", Version: "1.0.0"},
		Signals: []knowledge.Signal{{ID: "up", PromQL: "testware_up", Unit: "bool",
			Description: knowledge.Text{EN: "up", ZH: "在线"}}},
		Playbooks: []knowledge.Playbook{{
			ID: "t.up", Title: knowledge.Text{EN: "up", ZH: "在线"},
			Steps: []knowledge.Step{
				{ID: "collect", Collect: &knowledge.Collect{
					Tool: "promql.range", Args: map[string]any{"query": "{{signal:up}}"}, As: "up"}},
				{ID: "eval", Evaluate: "up.empty or up.latest < 1",
					OnTrue: &knowledge.Branch{Finding: &knowledge.StepFinding{
						Severity: "critical", Confidence: 0.9,
						Statement: knowledge.Text{EN: "could not be scraped", ZH: "无法抓取"},
					}},
					OnFalse: &knowledge.Branch{Pass: knowledge.Text{EN: "reachable", ZH: "可达"}},
				},
			},
		}},
	}
	e := syntheticEngine(t, &signalStub{byQuery: map[string][]float64{}}, nil)
	out, err := e.Run(context.Background(), pack, &pack.Playbooks[0], Input{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("an absent `up` must still be read as down: findings=%+v gaps=%+v",
			out.Findings, out.Gaps)
	}
}
