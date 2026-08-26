package eval

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

// stubs serves the Prometheus and Loki HTTP APIs from a case.
//
// Real servers rather than injected tools, deliberately. The layer most likely
// to regress sits between a signal's PromQL and the parsed series — query
// construction, the guard's verdict on it, the collector's decoding, the
// engine's handling of an empty result. Every defect this project found in the
// last four features lived exactly there: regex literals read as slot names,
// empty series read as healthy, citations naming nothing. A harness that
// injected tools would skip all of it and measure a system nobody runs
// (design-hld.md §5).
type stubs struct {
	prom *httptest.Server
	loki *httptest.Server

	mu        sync.Mutex
	promHits  int
	lokiHits  int
	queries   []string
	unmatched []string
}

// newStubs builds the servers for one case. Withheld sources are served by a
// handler that fails, so the run experiences the absence rather than being
// handed empty data — the difference between "there is nothing wrong" and "we
// could not look", which is what the case is testing for.
func newStubs(c *Case) *stubs {
	s := &stubs{}

	// Longest key first: a case keyed `redis_memory_used_bytes` must not be
	// shadowed by a shorter key that also appears in the query.
	keys := make([]string, 0, len(c.Telemetry.Metrics))
	for k := range c.Telemetry.Metrics {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})

	s.prom = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.promHits++
		s.mu.Unlock()

		if c.Withholds("metrics") {
			http.Error(w, "metrics source withheld by the case", http.StatusServiceUnavailable)
			return
		}

		_ = r.ParseForm()
		query := r.Form.Get("query")
		s.record(query)

		values, matched := lookup(c.Telemetry.Metrics, keys, query)
		if !matched {
			s.mu.Lock()
			s.unmatched = append(s.unmatched, query)
			s.mu.Unlock()
			// An unmatched query returns an *empty* result, not a zero. Zero is
			// a measurement; empty is "this deployment does not export that",
			// and since feature 002's engine fix the difference is what turns a
			// silent false pass into a recorded gap.
			writeJSON(w, emptyMatrix(query, r.URL.Path))
			return
		}
		writeJSON(w, series(values, r.URL.Path))
	}))

	s.loki = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.lokiHits++
		s.mu.Unlock()

		if c.Withholds("logs") {
			http.Error(w, "log source withheld by the case", http.StatusServiceUnavailable)
			return
		}
		if strings.Contains(r.URL.Path, "labels") {
			writeJSON(w, map[string]any{"status": "success", "data": []string{"job", "pod"}})
			return
		}
		writeJSON(w, streams(c.Telemetry.Logs))
	}))

	return s
}

func (s *stubs) record(query string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, query)
}

// hits reports how much of the pipeline actually reached the network, which is
// how a test proves the harness did not quietly bypass the collectors.
func (s *stubs) hits() (prom, loki int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.promHits, s.lokiHits
}

func (s *stubs) close() {
	s.prom.Close()
	s.loki.Close()
}

// lookup finds the series a query should return, by longest matching key.
func lookup(metrics map[string][]float64, keys []string, query string) ([]float64, bool) {
	for _, k := range keys {
		if strings.Contains(query, k) {
			return metrics[k], true
		}
	}
	return nil, false
}

const stubEpoch = 1724400000

func series(values []float64, path string) map[string]any {
	metric := map[string]string{"instance": "instance-0", "job": "case"}
	if strings.Contains(path, "query_range") {
		points := make([][]any, 0, len(values))
		for i, v := range values {
			points = append(points, []any{stubEpoch + i*60, fmt.Sprintf("%g", v)})
		}
		return map[string]any{"status": "success", "data": map[string]any{
			"resultType": "matrix",
			"result":     []any{map[string]any{"metric": metric, "values": points}},
		}}
	}
	last := 0.0
	if len(values) > 0 {
		last = values[len(values)-1]
	}
	return map[string]any{"status": "success", "data": map[string]any{
		"resultType": "vector",
		"result": []any{map[string]any{
			"metric": metric, "value": []any{stubEpoch, fmt.Sprintf("%g", last)},
		}},
	}}
}

func emptyMatrix(_ string, path string) map[string]any {
	kind := "vector"
	if strings.Contains(path, "query_range") {
		kind = "matrix"
	}
	return map[string]any{"status": "success", "data": map[string]any{
		"resultType": kind, "result": []any{},
	}}
}

func streams(lines []string) map[string]any {
	values := make([][]string, 0, len(lines))
	base := time.Unix(stubEpoch, 0)
	for i, line := range lines {
		values = append(values, []string{
			fmt.Sprintf("%d", base.Add(time.Duration(i)*time.Second).UnixNano()), line,
		})
	}
	return map[string]any{"status": "success", "data": map[string]any{
		"resultType": "streams",
		"result": []any{map[string]any{
			"stream": map[string]string{"job": "case"}, "values": values,
		}},
	}}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
