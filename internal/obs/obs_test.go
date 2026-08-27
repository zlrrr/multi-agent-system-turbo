package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
)

func captureLogger(t *testing.T, level string, literals []string) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	r := safety.NewRedactor(nil, literals)
	return Setup(config.LogConfig{Level: level, Format: "json"}, r, buf), buf
}

func TestRunIDPropagates(t *testing.T) {
	logger, buf := captureLogger(t, "info", nil)
	ctx := WithRun(context.Background(), &RunContext{RunID: "run-abc", Logger: logger, Metrics: NewMetrics()})

	Log(ctx).Info("collecting metrics", "tool", "promql.instant")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buf.String())
	}
	if rec["run_id"] != "run-abc" {
		t.Fatalf("run_id missing: %v", rec)
	}
	if rec["tool"] != "promql.instant" {
		t.Fatalf("attribute lost: %v", rec)
	}
	if RunID(ctx) != "run-abc" {
		t.Fatal("RunID helper does not read the context")
	}
}

func TestLogNeverReturnsNil(t *testing.T) {
	if Log(context.Background()) == nil {
		t.Fatal("Log returned nil outside a run context")
	}
	if Log(WithRun(context.Background(), &RunContext{RunID: "x"})) == nil {
		t.Fatal("Log returned nil for a run context without a logger")
	}
}

// TestHandlerRedacts is the FR-016 boundary test for logging.
func TestHandlerRedacts(t *testing.T) {
	const secret = "sk-ant-live-abcdefghijklmnop"
	logger, buf := captureLogger(t, "debug", []string{secret})
	ctx := WithRun(context.Background(), &RunContext{RunID: "r", Logger: logger})

	Log(ctx).Info("calling provider with "+secret,
		"api_key", secret,
		"authorization", "Bearer "+secret,
		"url", "https://api.example.com/v1?token=abcdef123456",
		"nested", map[string]any{"password": "hunter2hunter2", "ok": "keep-me"},
		"harmless", "redis_memory_used_bytes",
	)

	out := buf.String()
	for _, leaked := range []string{secret, "hunter2hunter2", "abcdef123456"} {
		if strings.Contains(out, leaked) {
			t.Errorf("log leaked %q: %s", leaked, out)
		}
	}
	for _, kept := range []string{"keep-me", "redis_memory_used_bytes", "api.example.com"} {
		if !strings.Contains(out, kept) {
			t.Errorf("log destroyed harmless value %q: %s", kept, out)
		}
	}
}

func TestRedactionSurvivesWithAttrs(t *testing.T) {
	const secret = "literal-secret-1234"
	logger, buf := captureLogger(t, "info", []string{secret})
	child := logger.With("api_key", secret, "component", "promql")
	child.Info("hello")
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("WithAttrs bypassed redaction: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "promql") {
		t.Fatalf("WithAttrs lost the harmless attribute: %s", buf.String())
	}
}

func TestLevelFiltering(t *testing.T) {
	logger, buf := captureLogger(t, "warn", nil)
	logger.Info("should not appear")
	logger.Warn("should appear")
	out := buf.String()
	if strings.Contains(out, "should not appear") {
		t.Error("info logged at warn level")
	}
	if !strings.Contains(out, "should appear") {
		t.Error("warn suppressed at warn level")
	}
}

func TestTextFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	l := Setup(config.LogConfig{Level: "info", Format: "text"}, safety.NewRedactor(nil, nil), buf)
	l.Info("plain", "k", "v")
	if !strings.Contains(buf.String(), "k=v") {
		t.Fatalf("text handler not used: %s", buf.String())
	}
}

func TestNextStepIDIsMonotonic(t *testing.T) {
	rc := &RunContext{RunID: "r"}
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		id := rc.NextStepID()
		if seen[id] {
			t.Fatalf("duplicate step id %s", id)
		}
		seen[id] = true
	}
	if rc.NextStepID() != "step-6" {
		t.Fatal("step ids are not sequential")
	}
}

func TestPromExposition(t *testing.T) {
	m := NewMetrics()
	m.IncCounter("mas_runs_total", map[string]string{"topology": "supervisor", "mode": "offline"})
	m.IncCounter("mas_runs_total", map[string]string{"topology": "supervisor", "mode": "offline"})
	m.IncCounter("mas_tool_calls_total", map[string]string{"tool": "promql.instant", "outcome": "ok"})
	m.SetGauge("mas_packs_loaded", 2, nil)
	m.Observe("mas_tool_duration_seconds", 0.3, map[string]string{"tool": "promql.instant"})
	m.Observe("mas_tool_duration_seconds", 7, map[string]string{"tool": "promql.instant"})

	var buf bytes.Buffer
	if err := m.WriteProm(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		`# TYPE mas_runs_total counter`,
		`mas_runs_total{mode="offline",topology="supervisor"} 2`,
		`# TYPE mas_packs_loaded gauge`,
		`mas_packs_loaded 2`,
		`# TYPE mas_tool_duration_seconds histogram`,
		`mas_tool_duration_seconds_count{tool="promql.instant"} 2`,
		`mas_tool_duration_seconds_sum{tool="promql.instant"} 7.3`,
		`mas_tool_duration_seconds_bucket{le="+Inf",tool="promql.instant"} 2`,
		`mas_tool_duration_seconds_bucket{le="0.5",tool="promql.instant"} 1`,
		`# HELP mas_runs_total`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition missing %q\n---\n%s", want, out)
		}
	}
	if m.CounterValue("mas_runs_total", map[string]string{"topology": "supervisor", "mode": "offline"}) != 2 {
		t.Error("CounterValue disagrees with the exposition")
	}
}

func TestPromExpositionIsDeterministic(t *testing.T) {
	build := func() string {
		m := NewMetrics()
		for i := 0; i < 20; i++ {
			m.IncCounter("mas_tool_calls_total", map[string]string{"tool": "t" + string(rune('a'+i%7)), "outcome": "ok"})
		}
		var b bytes.Buffer
		_ = m.WriteProm(&b)
		return b.String()
	}
	first, second := build(), build()
	if first != second {
		t.Fatalf("exposition order is not stable across runs:\n%s\n---\n%s", first, second)
	}
}

func TestNilMetricsIsSafe(t *testing.T) {
	var m *Metrics
	m.IncCounter("x", nil)
	m.SetGauge("x", 1, nil)
	m.Observe("x", 1, nil)
}

func TestMetricsOfFallsBackToDefault(t *testing.T) {
	if MetricsOf(context.Background()) == nil {
		t.Fatal("MetricsOf returned nil outside a run")
	}
}
