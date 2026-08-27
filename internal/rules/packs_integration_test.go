package rules

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/promql"
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
)

// signalStub answers promql calls by exact query, from a table the test builds
// out of the pack's own signal definitions. Matching on the expanded query
// rather than on a substring means a scenario cannot silently drift: renaming a
// signal, or reading one the scenario never supplied, shows up as an unresolved
// query rather than as an empty result that reads like a healthy system.
type signalStub struct {
	name       string
	byQuery    map[string][]float64
	unresolved []string
	queries    []string
}

func (s *signalStub) Name() string         { return s.name }
func (s *signalStub) Description() string  { return "stub metrics keyed by signal" }
func (s *signalStub) Domain() tool.Domain  { return tool.DomainMetrics }
func (s *signalStub) Safety() safety.Class { return safety.ClassReadOnly }
func (s *signalStub) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"query": {Type: tool.TypeString, Description: "PromQL"},
		"from":  {Type: tool.TypeString, Description: "start"},
		"to":    {Type: tool.TypeString, Description: "end"},
		"step":  {Type: tool.TypeString, Description: "step"},
		"at":    {Type: tool.TypeString, Description: "instant"},
	}, "query")
}

func (s *signalStub) Plan(map[string]any) (safety.Call, error) {
	return safety.Call{
		Class: safety.ClassReadOnly,
		HTTP:  &safety.HTTPEffect{Method: "POST", URL: "http://prom:9090/api/v1/query"},
	}, nil
}

func (s *signalStub) Invoke(_ context.Context, args map[string]any) (core.Evidence, error) {
	q := tool.Str(args, "query", "")
	s.queries = append(s.queries, q)
	values, ok := s.byQuery[q]
	if !ok {
		s.unresolved = append(s.unresolved, q)
	}

	base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	series := promql.Series{Metric: map[string]string{"instance": "node-0"}}
	for i, v := range values {
		series.Points = append(series.Points, promql.Sample{
			At: base.Add(time.Duration(i) * time.Minute), Value: v,
		})
	}
	series.Count = len(values)
	if len(values) > 0 {
		series.Last = values[len(values)-1]
		series.Min, series.Max = values[0], values[0]
		sum := 0.0
		for _, v := range values {
			if v < series.Min {
				series.Min = v
			}
			if v > series.Max {
				series.Max = v
			}
			sum += v
		}
		series.Avg = sum / float64(len(values))
	}

	res := promql.Result{Query: q, ResultType: "matrix"}
	if len(values) > 0 {
		res.Series = []promql.Series{series}
	}
	return core.Evidence{
		Kind: core.EvidenceMetricSeries, Source: "promql:stub", Query: q,
		Payload: res, Summary: res.Summary(),
	}, nil
}

// logStub answers loki.query from a fixed set of lines, in the payload shape the
// rules layer binds through its capability interfaces — so the test exercises
// the real binding path without importing a collector.
type logStub struct{ lines []string }

func (l *logStub) Name() string         { return "loki.query" }
func (l *logStub) Description() string  { return "stub logs" }
func (l *logStub) Domain() tool.Domain  { return tool.DomainLogs }
func (l *logStub) Safety() safety.Class { return safety.ClassReadOnly }
func (l *logStub) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"query": {Type: tool.TypeString, Description: "LogQL"},
		"limit": {Type: tool.TypeInteger, Description: "max lines"},
		"from":  {Type: tool.TypeString, Description: "start"},
		"to":    {Type: tool.TypeString, Description: "end"},
	}, "query")
}

func (l *logStub) Plan(map[string]any) (safety.Call, error) {
	return safety.Call{
		Class: safety.ClassReadOnly,
		HTTP:  &safety.HTTPEffect{Method: "GET", URL: "http://loki:3100/loki/api/v1/query_range"},
	}, nil
}

func (l *logStub) Invoke(_ context.Context, args map[string]any) (core.Evidence, error) {
	return core.Evidence{
		Kind: core.EvidenceLogLines, Source: "loki:stub", Query: tool.Str(args, "query", ""),
		Payload: map[string]any{"lines": l.lines},
		Summary: "stub: " + strings.Join(l.lines, " | "),
	}, nil
}

const stubSelector = `{job="stub"}`

// scenario drives one pack through one incident and, with the same playbooks,
// through a healthy system. Series are keyed by signal id, so the scenario is
// written in the pack's own vocabulary rather than in PromQL.
type scenario struct {
	middleware string
	symptom    string
	incident   map[string][]float64
	healthy    map[string][]float64
	logs       []string
	healthyLog []string
	wantModes  []string // failure modes the incident evidence must reach
	wantText   []string // substrings that must appear in some finding
}

var packScenarios = []scenario{
	{
		middleware: "mongodb",
		symptom:    "replication lag and slow queries",
		incident: map[string][]float64{
			"up": {1, 1, 1}, "repl_member_health": {1, 1, 1}, "election_count": {0},
			"repl_lag": {5, 45, 180}, "oplog_window": {3600},
			"scan_ratio": {50, 200, 600}, "queued_readers": {1, 2, 3},
			"op_latency_read": {20000, 80000, 150000},
		},
		healthy: map[string][]float64{
			"up": {1, 1, 1}, "repl_member_health": {1, 1, 1}, "election_count": {0},
			"repl_lag": {1, 2, 1}, "oplog_window": {7200},
			"scan_ratio": {2, 3, 2}, "queued_readers": {0, 1, 0},
			"op_latency_read": {500, 600, 700},
		},
		logs: []string{
			"2026-08-23T10:04:00 I COMMAND [conn42] query planSummary: COLLSCAN 4211ms",
		},
		wantModes: []string{"replication-lag", "slow-queries"},
		wantText:  []string{"behind the primary", "scanning"},
	},
	{
		middleware: "pulsar",
		symptom:    "backlog growing on a subscription",
		incident: map[string][]float64{
			"up": {1, 1, 1}, "bookie_writable": {1, 1, 1}, "topics_count": {12, 12, 12},
			"backlog_total": {5000, 40000, 250000},
			"msg_rate_in":   {1000, 1000, 1000}, "msg_rate_out": {900, 100, 0},
			"consumers_count": {3, 1, 0},
		},
		healthy: map[string][]float64{
			"up": {1, 1, 1}, "bookie_writable": {1, 1, 1}, "topics_count": {12, 12, 12},
			"backlog_total": {100, 90, 80},
			"msg_rate_in":   {1000, 1000, 1000}, "msg_rate_out": {1000, 1000, 1000},
			"consumers_count": {3, 3, 3},
		},
		logs:      []string{"10:05:00 INFO Closed consumer Consumer{subscription=sub-a}"},
		wantModes: []string{"subscription-backlog", "consumer-stall"},
		wantText:  []string{"no connected consumers"},
	},
	{
		middleware: "milvus",
		symptom:    "search latency spike and memory pressure",
		incident: map[string][]float64{
			"up": {1, 1, 1}, "meta_op_failures": {0, 0, 0}, "storage_latency_p99": {50, 60, 70},
			"search_latency_p99": {200, 800, 2400}, "querynode_latency_p99": {100, 300, 900},
			"querynode_queue_latency_p99": {10, 400, 1800}, "search_qps": {500, 500, 500},
			"index_task_failures":    {0, 0, 0},
			"querynode_memory_ratio": {0.5, 0.8, 0.94}, "loaded_entities": {1e6, 1e6, 1e6},
		},
		healthy: map[string][]float64{
			"up": {1, 1, 1}, "meta_op_failures": {0, 0, 0}, "storage_latency_p99": {40, 50, 45},
			"search_latency_p99": {100, 120, 110}, "querynode_latency_p99": {40, 50, 45},
			"querynode_queue_latency_p99": {5, 6, 5}, "search_qps": {500, 520, 510},
			"index_task_failures":    {0, 0, 0},
			"querynode_memory_ratio": {0.4, 0.45, 0.5}, "loaded_entities": {1e6, 1e6, 1e6},
		},
		logs: []string{
			"[2026/08/23 10:06:00] [WARN] [querynode] insufficient memory to load segment 4412",
		},
		wantModes: []string{"query-node-latency", "memory-pressure"},
		wantText:  []string{"queued", "ran out of memory"},
	},
	{
		middleware: "oceanbase",
		symptom:    "tenant memory exhausted and clog sync lag",
		incident: map[string][]float64{
			"up": {1, 1, 1}, "rs_status": {1, 1, 1},
			"major_merge_lag": {600, 900, 1200}, "merge_error": {0, 0, 0},
			"clog_disk_ratio": {0.4, 0.5, 0.66}, "data_disk_ratio": {0.5, 0.5, 0.5},
			"tenant_memory_ratio": {0.6, 0.8, 0.93}, "memstore_ratio": {0.4, 0.6, 0.75},
			"tenant_cpu_usage": {2, 2, 2}, "tenant_cpu_limit": {8, 8, 8},
			"active_sessions": {10, 20, 30},
			"clog_sync_delay": {0.2, 3, 14}, "partition_leader_count": {200, 200, 200},
		},
		healthy: map[string][]float64{
			"up": {1, 1, 1}, "rs_status": {1, 1, 1},
			"major_merge_lag": {600, 600, 600}, "merge_error": {0, 0, 0},
			"clog_disk_ratio": {0.3, 0.3, 0.3}, "data_disk_ratio": {0.5, 0.5, 0.5},
			"tenant_memory_ratio": {0.4, 0.45, 0.5}, "memstore_ratio": {0.2, 0.3, 0.35},
			"tenant_cpu_usage": {2, 2, 2}, "tenant_cpu_limit": {8, 8, 8},
			"active_sessions": {10, 11, 12},
			"clog_sync_delay": {0.1, 0.2, 0.15}, "partition_leader_count": {200, 200, 200},
		},
		logs:      []string{"[2026-08-23 10:07:00] WARN clog sync delay detected, replica sync lag=14s"},
		wantModes: []string{"tenant-memory-exhaustion", "clog-sync-lag"},
		wantText:  []string{"memory limit", "replication delay"},
	},
}

// TestNewPacksRunAgainstStubTelemetry is the feature-002 integration gate
// (T150): every pack this feature adds must reach its declared failure modes
// through the real rules engine, not merely satisfy a schema.
func TestNewPacksRunAgainstStubTelemetry(t *testing.T) {
	for _, sc := range packScenarios {
		t.Run(sc.middleware, func(t *testing.T) {
			e, lib := newFullEngine(t, sc.incident, sc.logs, sc.middleware)
			pack := packByMiddleware(t, lib, sc.middleware)
			metrics := metricsOf(e)

			out := e.RunAll(context.Background(), pack, sc.symptom,
				Input{Language: "en", Selector: stubSelector})

			if len(metrics.queries) == 0 {
				t.Fatal("no metrics were queried: the symptom matched no playbook")
			}
			if len(metrics.unresolved) > 0 {
				t.Errorf("the scenario supplies no value for %d queries the playbooks read, "+
					"which would be read as an empty result:\n  %s",
					len(metrics.unresolved), strings.Join(unique(metrics.unresolved), "\n  "))
			}
			if out.LLMCalls != 0 {
				t.Errorf("the deterministic phase made %d model calls", out.LLMCalls)
			}
			if len(out.Findings) == 0 {
				t.Fatalf("evidence describing a live incident produced no finding; gaps: %+v", out.Gaps)
			}
			if len(out.Gaps) > 0 {
				t.Errorf("fully supplied telemetry still produced gaps: %+v", out.Gaps)
			}

			concluded := map[string]bool{}
			for _, id := range out.Conclusions {
				concluded[id] = true
			}
			for _, want := range sc.wantModes {
				if !concluded[want] {
					t.Errorf("failure mode %q was not concluded; concluded %v", want, sortedKeys(concluded))
				}
			}

			joined := strings.ToLower(findingText(out))
			for _, want := range sc.wantText {
				if !strings.Contains(joined, strings.ToLower(want)) {
					t.Errorf("no finding mentions %q; findings were:\n%s", want, findingText(out))
				}
			}

			for _, f := range out.Findings {
				if len(f.Evidence) == 0 {
					t.Errorf("finding %q cites no evidence", f.Statement)
				}
				if !strings.HasPrefix(f.Origin, "rule:") {
					t.Errorf("finding %q has origin %q; it must name the rule that produced it",
						f.Statement, f.Origin)
				}
			}

			// A conclusion is only useful if the operator can act on it, so every
			// concluded mode must be declared and carry advice.
			for _, id := range out.Conclusions {
				mode, ok := pack.FailureMode(id)
				if !ok {
					t.Errorf("conclusion names undeclared failure mode %q", id)
					continue
				}
				if len(mode.Recommendations) == 0 {
					t.Errorf("failure mode %q carries no recommendation", mode.ID)
				}
			}
		})
	}
}

// TestNewPacksStayQuietOnHealthyTelemetry is the other half of the gate. A pack
// that reports a problem for every input is worth nothing: the same playbooks,
// against healthy readings, must reach no conclusion and must still say what
// they ruled out.
func TestNewPacksStayQuietOnHealthyTelemetry(t *testing.T) {
	for _, sc := range packScenarios {
		t.Run(sc.middleware, func(t *testing.T) {
			logs := sc.healthyLog
			if len(logs) == 0 {
				logs = []string{"10:00:00 INFO steady state"}
			}
			e, lib := newFullEngine(t, sc.healthy, logs, sc.middleware)
			pack := packByMiddleware(t, lib, sc.middleware)

			out := e.RunAll(context.Background(), pack, sc.symptom,
				Input{Language: "en", Selector: stubSelector})

			if len(out.Conclusions) != 0 {
				t.Errorf("healthy telemetry concluded %v\nfindings:\n%s",
					out.Conclusions, findingText(out))
			}
			if len(out.Findings) != 0 {
				t.Errorf("healthy telemetry produced findings:\n%s", findingText(out))
			}
			if len(out.ChecksPassed) == 0 {
				t.Error("no check was reported as passed, so the operator learns nothing was ruled out")
			}
		})
	}
}

// engines is how a test reaches the stub it registered; the Engine deliberately
// exposes only its own behaviour.
var engines = map[*Engine]*signalStub{}

func metricsOf(e *Engine) *signalStub { return engines[e] }

func newFullEngine(t *testing.T, bySignal map[string][]float64, logLines []string,
	middleware string) (*Engine, *knowledge.Library) {

	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := knowledge.LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	pack := packByMiddleware(t, lib, middleware)

	// Translate signal ids into the queries the playbooks will actually issue.
	byQuery := map[string][]float64{}
	for id, values := range bySignal {
		sig, ok := pack.Signal(id)
		if !ok {
			t.Fatalf("scenario names signal %q, which pack %s does not declare", id, middleware)
		}
		byQuery[strings.ReplaceAll(sig.PromQL, "{{.selector}}", stubSelector)] = values
	}

	metrics := &signalStub{byQuery: byQuery}
	reg := tool.NewRegistry()
	for _, name := range []string{"promql.instant", "promql.range", "promql.series"} {
		reg.MustRegister(&namedStub{Tool: metrics, name: name})
	}
	reg.MustRegister(&logStub{lines: logLines})

	inv, err := tool.NewInvoker(reg, tool.InvokerOptions{
		Guard: g, Mode: core.ModeOffline, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := New(inv, lib)
	engines[e] = metrics
	t.Cleanup(func() { delete(engines, e) })
	return e, lib
}

func packByMiddleware(t *testing.T, lib *knowledge.Library, middleware string) *knowledge.Pack {
	t.Helper()
	for _, p := range lib.All() {
		if p.Metadata.Middleware == middleware {
			return p
		}
	}
	t.Fatalf("no pack ships for %s", middleware)
	return nil
}

func findingText(out Output) string {
	parts := make([]string, 0, len(out.Findings))
	for _, f := range out.Findings {
		parts = append(parts, "  - "+f.Statement+" "+f.Detail)
	}
	return strings.Join(parts, "\n")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
