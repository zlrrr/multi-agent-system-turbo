package report_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/report"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func fixture() *core.Report {
	at := time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
	ev1 := core.Evidence{
		ID: "ev-1", Kind: core.EvidenceMetricSeries, Source: "promql:primary",
		Query: "redis_memory_used_bytes{instance=\"redis-0\"}", CollectedAt: at,
		Summary: "redis_memory_used_bytes → last=1020 min=900 max=1020 avg=970 over 12 points",
		Digest:  "d1",
	}
	ev2 := core.Evidence{
		ID: "ev-2", Kind: core.EvidenceLogLines, Source: "loki:primary",
		Query: `{job="redis"} |= "OOM"`, CollectedAt: at,
		Summary: `{job="redis"} |= "OOM" → 41 lines across 1 streams; newest at 2026-08-23T10:58:00Z: OOM command not allowed`,
		Digest:  "d2",
	}
	r := &core.Report{
		Schema: core.ReportSchema, RunID: "run-20260823T110000-abcd1234", GeneratedAt: at,
		Target: core.Target{
			ID: "redis-prod", Kind: core.KindRedis, Version: "7.2.4",
			Env: core.EnvBinding{Name: "prod", Type: "kubernetes", Namespace: "middleware"},
		},
		Request: core.DiagnoseRequest{
			Target: "redis-prod", Symptom: "p99 latency spike with evictions",
			Window: core.Window{From: at.Add(-time.Hour), To: at},
			Mode:   core.ModeOnline, Topology: "supervisor", Language: "en",
		},
		Topology: "supervisor",
		Summary: "Redis is at its configured memory ceiling. Eviction began before latency rose and the log " +
			"shows write refusals in the same window, so memory pressure is the cause rather than a consequence.",
		Hypotheses: []core.Hypothesis{
			{ID: "h-1", Statement: "Redis reached maxmemory; eviction could not free space fast enough.",
				Status: core.HypothesisSupported, Confidence: 0.85,
				Supporting: []string{"ev-1", "ev-2"},
				Rationale:  "Used memory is above 90% of maxmemory and eviction precedes the latency rise."},
			{ID: "h-2", Statement: "A single slow command blocked the event loop.",
				Status: core.HypothesisRefuted, Confidence: 0.05,
				Contradicting: []string{"ev-1"},
				Rationale:     "CPU stayed below saturation and no long fork pause was observed."},
		},
		Findings: []core.Finding{
			{ID: "f-1", Origin: "rule:redis.memory-pressure/eval-pressure", Severity: core.SeverityCritical,
				Statement: "Used memory is above 90% of the configured maxmemory.",
				Detail:    "At this point Redis either evicts keys or refuses writes, depending on maxmemory-policy.",
				Evidence:  []string{"ev-1"}, Confidence: 0.9},
			{ID: "f-2", Origin: "rule:redis.memory-pressure/eval-eviction", Severity: core.SeverityMajor,
				Statement: "Keys are being evicted.", Evidence: []string{"ev-1"}, Confidence: 0.85},
		},
		ChecksPassed: []string{
			"The instance was reachable throughout the window.",
			"No long fork pause was observed.",
		},
		Gaps: []core.Gap{
			{ID: "gap-1", Intent: "kube.nodes()", Reason: core.GapRefused, Code: "MAS-4201",
				Impact: "node-level memory pressure could not be ruled out"},
		},
		Recommendations: []core.Recommendation{
			core.NewRecommendation("Confirm the eviction policy with CONFIG GET maxmemory-policy.",
				core.RiskLow, "The policy decides whether clients see errors or silent data loss.", "ev-1"),
			core.NewRecommendation("Raise maxmemory only if the host has headroom.",
				core.RiskMedium, "Without headroom the kernel OOM killer replaces a degraded Redis with a dead one."),
		},
		Evidence: []core.Evidence{ev1, ev2},
		Usage: core.Usage{
			LLMCalls: 9, PromptTokens: 12000, CompletionTokens: 1800, ToolCalls: 7, WallMillis: 4200,
		},
	}
	r.SortHypotheses()
	r.SortFindings()
	return r
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file missing; run `go test ./internal/report -update`: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the golden file.\n--- got ---\n%s", name, got)
	}
}

func TestMarkdownGoldenEN(t *testing.T) {
	got, err := report.Markdown(fixture(), "en")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "report.en.md", got)
}

func TestMarkdownGoldenZH(t *testing.T) {
	got, err := report.Markdown(fixture(), "zh")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "report.zh.md", got)
}

func TestJSONGolden(t *testing.T) {
	got, err := report.JSON(fixture())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "report.json", got)
}

func TestTextGolden(t *testing.T) {
	got, err := report.Text(fixture(), "en")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "report.txt", got)
}

func TestJSONRoundTripsAsReportV1(t *testing.T) {
	b, err := report.JSON(fixture())
	if err != nil {
		t.Fatal(err)
	}
	var back core.Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Schema != core.ReportSchema {
		t.Fatalf("schema = %q", back.Schema)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("a rendered report failed its own invariants: %v", err)
	}
}

// TestMarkdownIsBilingual guards Art. III at the rendering boundary: a Chinese
// reader must get a Chinese report, not English chrome around translated text.
func TestMarkdownIsBilingual(t *testing.T) {
	en, _ := report.Markdown(fixture(), "en")
	zh, _ := report.Markdown(fixture(), "zh")
	if string(en) == string(zh) {
		t.Fatal("the Chinese report is identical to the English one")
	}
	for _, want := range []string{"诊断报告", "结论摘要", "假设", "证据缺口", "建议的后续动作", "运行统计"} {
		if !strings.Contains(string(zh), want) {
			t.Errorf("the Chinese report is missing the section %q", want)
		}
	}
	for _, want := range []string{"Diagnostic report", "Summary", "Hypotheses", "Gaps in the evidence"} {
		if !strings.Contains(string(en), want) {
			t.Errorf("the English report is missing the section %q", want)
		}
	}
}

// TestAdvisoryNoticeIsAlwaysPresent is a rendering-level check on CON-003: a
// reader must never be able to mistake a recommendation for an action taken.
func TestAdvisoryNoticeIsAlwaysPresent(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		out, err := report.Markdown(fixture(), lang)
		if err != nil {
			t.Fatal(err)
		}
		body := string(out)
		marker := "read-only"
		if lang == "zh" {
			marker = "只读"
		}
		if !strings.Contains(body, marker) {
			t.Errorf("%s report does not state that the system is read-only", lang)
		}
	}
}

func TestGapsAppearBeforeRecommendations(t *testing.T) {
	out, _ := report.Markdown(fixture(), "en")
	body := string(out)
	gapsAt := strings.Index(body, "## Gaps in the evidence")
	recsAt := strings.Index(body, "## Recommended next steps")
	if gapsAt < 0 || recsAt < 0 {
		t.Fatal("expected both sections")
	}
	if gapsAt > recsAt {
		t.Fatal("recommendations precede the gaps; a reader could act without seeing the limits")
	}
}

func TestEmptyReportRendersCleanly(t *testing.T) {
	empty := &core.Report{
		Schema: core.ReportSchema, RunID: "run-empty", GeneratedAt: time.Now().UTC(),
		Target:  core.Target{ID: "t", Kind: core.KindRedis},
		Request: core.DiagnoseRequest{Target: "t", Symptom: "s"},
	}
	for _, lang := range []string{"en", "zh"} {
		out, err := report.Markdown(empty, lang)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if len(out) == 0 || strings.Contains(string(out), "%!") {
			t.Fatalf("%s: bad rendering: %s", lang, out)
		}
	}
	if _, err := report.Text(empty, "en"); err != nil {
		t.Fatal(err)
	}
}

func TestTruncationIsVisible(t *testing.T) {
	r := fixture()
	r.Truncated = true
	out, _ := report.Markdown(r, "en")
	if !strings.Contains(string(out), "truncated") {
		t.Fatal("a truncated run does not say so in the report")
	}
}

func TestNilReportIsCoded(t *testing.T) {
	if _, err := report.Markdown(nil, "en"); errs.CodeOf(err) != "MAS-9001" {
		t.Fatalf("got %v, want MAS-9001", err)
	}
}

// TestNoEnglishChromeInChineseReport is the parity check that caught the
// hard-coded "Run" and "Symptom" labels: a Chinese report must not carry
// English structural labels.
func TestNoEnglishChromeInChineseReport(t *testing.T) {
	out, err := report.Markdown(fixture(), "zh")
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"| Run |", "| Symptom |", "| Cost |", "**status**", "**confidence**"} {
		if strings.Contains(string(out), leaked) {
			t.Errorf("the Chinese report contains untranslated chrome: %q", leaked)
		}
	}
}

// TestUnpricedRunSaysSoInBothLanguages is FR-006 and CON-001. Omitting the cost
// row leaves the reader to assume; printing a figure they were never given is
// worse, because they will believe it.
func TestUnpricedRunSaysSoInBothLanguages(t *testing.T) {
	for lang, want := range map[string]string{
		"en": "not priced",
		"zh": "未定价",
	} {
		r := fixture()
		r.Usage.Cost = core.UnpricedCost("claude-opus-5")
		out, err := report.Markdown(r, lang)
		if err != nil {
			t.Fatal(err)
		}
		body := string(out)
		if !strings.Contains(body, want) {
			t.Errorf("%s report does not say the cost is unknown:\n%s", lang, body)
		}
		if !strings.Contains(body, "claude-opus-5") {
			t.Errorf("%s report does not name the unpriced model", lang)
		}
		if strings.Contains(body, "$0.0000") {
			t.Errorf("%s report shows $0.0000 for a run that was never priced", lang)
		}
	}
}

// TestNoRenderedReportContainsBareZeroCost is the regression this feature's
// whole type change exists to prevent, checked across every rendering path.
func TestNoRenderedReportContainsBareZeroCost(t *testing.T) {
	for name, cost := range map[string]core.Cost{
		"never set":     {},
		"one unpriced":  core.UnpricedCost("some-model"),
		"partly priced": core.KnownCost(0.25).Add(core.UnpricedCost("some-model")),
	} {
		t.Run(name, func(t *testing.T) {
			r := fixture()
			r.Usage.Cost = cost
			for _, lang := range []string{"en", "zh"} {
				out, err := report.Markdown(r, lang)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(out), "| $0.0000 |") {
					t.Errorf("%s/%s renders an unknown cost as $0.0000", name, lang)
				}
			}
			j, err := report.JSON(r)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(j), `"known": false`) {
				t.Errorf("%s: JSON does not mark the cost unknown:\n%s", name, j)
			}
		})
	}

	// A measured zero must still render as a figure: a run that really cost
	// nothing should say so.
	r := fixture()
	r.Usage.Cost = core.KnownCost(0)
	out, _ := report.Markdown(r, "en")
	if !strings.Contains(string(out), "$0.0000") {
		t.Error("a measured zero was not rendered as $0.0000")
	}
}

// TestReportCarriesPerRoleBreakdown is FR-008: the table has to reach the
// reader, and it has to add up to the figure above it.
func TestReportCarriesPerRoleBreakdown(t *testing.T) {
	r := fixture()
	r.Usage.Cost = core.KnownCost(0.30)
	r.RoleUsage = []core.RoleUsage{
		{Role: "advocate", Provider: "anthropic", Model: "opus", Calls: 3,
			PromptTokens: 900, CompletionTokens: 300, WallMillis: 2100, Cost: core.KnownCost(0.20)},
		{Role: "investigator", Provider: "openai", Model: "local", Calls: 4,
			PromptTokens: 400, CompletionTokens: 100, WallMillis: 800, Cost: core.KnownCost(0.10)},
	}
	for _, lang := range []string{"en", "zh"} {
		out, err := report.Markdown(r, lang)
		if err != nil {
			t.Fatal(err)
		}
		body := string(out)
		for _, want := range []string{"advocate", "investigator", "$0.2000", "$0.1000"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s report omits %q", lang, want)
			}
		}
	}
}
