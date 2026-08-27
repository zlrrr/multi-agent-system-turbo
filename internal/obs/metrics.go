package obs

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Metrics is a minimal Prometheus-compatible registry. The tool exposes fewer
// than twenty series, so the client library's collector machinery would be
// unused weight (plan.md §6).
type Metrics struct {
	mu         sync.RWMutex
	counters   map[string]*series
	gauges     map[string]*series
	histograms map[string]*histogram
	help       map[string]string
}

type series struct {
	name    string
	samples map[string]*sample // label signature → sample
}

type sample struct {
	labels map[string]string
	value  float64
}

type histogram struct {
	name    string
	buckets []float64
	entries map[string]*histEntry
}

type histEntry struct {
	labels map[string]string
	counts []uint64
	sum    float64
	count  uint64
}

// DefaultBuckets covers the latency range this tool operates in: sub-second
// tool calls through multi-minute agent runs.
var DefaultBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

var (
	defaultOnce     sync.Once
	defaultRegistry *Metrics
)

// Default returns the process-wide registry.
func Default() *Metrics {
	defaultOnce.Do(func() { defaultRegistry = NewMetrics() })
	return defaultRegistry
}

// NewMetrics creates an empty registry with the tool's standard help text.
func NewMetrics() *Metrics {
	m := &Metrics{
		counters:   map[string]*series{},
		gauges:     map[string]*series{},
		histograms: map[string]*histogram{},
		help: map[string]string{
			"mas_runs_total":            "Diagnostic runs started, by topology and mode.",
			"mas_runs_completed_total":  "Diagnostic runs finished, by status.",
			"mas_tool_calls_total":      "Tool invocations, by tool and outcome.",
			"mas_tool_refusals_total":   "Tool invocations refused by the safety guard, by code.",
			"mas_llm_calls_total":       "LLM completions, by provider and outcome.",
			"mas_llm_tokens_total":      "LLM tokens consumed, by provider and direction.",
			"mas_gaps_total":            "Evidence gaps recorded, by reason.",
			"mas_run_duration_seconds":  "Wall-clock duration of a diagnostic run.",
			"mas_tool_duration_seconds": "Wall-clock duration of a tool invocation.",
			"mas_llm_duration_seconds":  "Wall-clock duration of an LLM completion.",
			"mas_packs_loaded":          "Knowledge packs currently loaded.",
			"mas_build_info":            "Build metadata; always 1.",
		},
	}
	return m
}

func signature(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

// IncCounter adds one to a counter.
func (m *Metrics) IncCounter(name string, labels map[string]string) { m.AddCounter(name, 1, labels) }

// AddCounter adds a delta to a counter.
func (m *Metrics) AddCounter(name string, delta float64, labels map[string]string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.counters[name]
	if !ok {
		s = &series{name: name, samples: map[string]*sample{}}
		m.counters[name] = s
	}
	sig := signature(labels)
	e, ok := s.samples[sig]
	if !ok {
		e = &sample{labels: copyLabels(labels)}
		s.samples[sig] = e
	}
	e.value += delta
}

// SetGauge sets a gauge value.
func (m *Metrics) SetGauge(name string, v float64, labels map[string]string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.gauges[name]
	if !ok {
		s = &series{name: name, samples: map[string]*sample{}}
		m.gauges[name] = s
	}
	sig := signature(labels)
	e, ok := s.samples[sig]
	if !ok {
		e = &sample{labels: copyLabels(labels)}
		s.samples[sig] = e
	}
	e.value = v
}

// Observe records a value into a histogram.
func (m *Metrics) Observe(name string, v float64, labels map[string]string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.histograms[name]
	if !ok {
		h = &histogram{name: name, buckets: DefaultBuckets, entries: map[string]*histEntry{}}
		m.histograms[name] = h
	}
	sig := signature(labels)
	e, ok := h.entries[sig]
	if !ok {
		e = &histEntry{labels: copyLabels(labels), counts: make([]uint64, len(h.buckets))}
		h.entries[sig] = e
	}
	for i, b := range h.buckets {
		if v <= b {
			e.counts[i]++
		}
	}
	e.sum += v
	e.count++
}

// CounterValue reads a counter, for tests and `mas doctor`.
func (m *Metrics) CounterValue(name string, labels map[string]string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.counters[name]
	if !ok {
		return 0
	}
	e, ok := s.samples[signature(labels)]
	if !ok {
		return 0
	}
	return e.value
}

// WriteProm renders the registry in the Prometheus text exposition format.
func (m *Metrics) WriteProm(w io.Writer) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, name := range sortedKeys(m.counters) {
		if err := m.writeSeries(w, m.counters[name], "counter"); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(m.gauges) {
		if err := m.writeSeries(w, m.gauges[name], "gauge"); err != nil {
			return err
		}
	}
	for _, name := range sortedHistKeys(m.histograms) {
		if err := m.writeHistogram(w, m.histograms[name]); err != nil {
			return err
		}
	}
	return nil
}

func (m *Metrics) writeSeries(w io.Writer, s *series, kind string) error {
	if help, ok := m.help[s.name]; ok {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n", s.name, help); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", s.name, kind); err != nil {
		return err
	}
	for _, sig := range sortedSampleKeys(s.samples) {
		e := s.samples[sig]
		if _, err := fmt.Fprintf(w, "%s%s %g\n", s.name, renderLabels(e.labels, nil), e.value); err != nil {
			return err
		}
	}
	return nil
}

func (m *Metrics) writeHistogram(w io.Writer, h *histogram) error {
	if help, ok := m.help[h.name]; ok {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n", h.name, help); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "# TYPE %s histogram\n", h.name); err != nil {
		return err
	}
	for _, sig := range sortedHistEntryKeys(h.entries) {
		e := h.entries[sig]
		for i, b := range h.buckets {
			if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", h.name,
				renderLabels(e.labels, map[string]string{"le": trimFloat(b)}), e.counts[i]); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s_bucket%s %d\n", h.name,
			renderLabels(e.labels, map[string]string{"le": "+Inf"}), e.count); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s_sum%s %g\n", h.name, renderLabels(e.labels, nil), e.sum); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s_count%s %d\n", h.name, renderLabels(e.labels, nil), e.count); err != nil {
			return err
		}
	}
	return nil
}

func renderLabels(labels map[string]string, extra map[string]string) string {
	merged := make(map[string]string, len(labels)+len(extra))
	for k, v := range labels {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	if len(merged) == 0 {
		return ""
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", k, escapeLabelValue(merged[k]))
	}
	b.WriteByte('}')
	return b.String()
}

func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

func trimFloat(f float64) string { return strings.TrimSuffix(fmt.Sprintf("%g", f), ".0") }

func copyLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]*series) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedHistKeys(m map[string]*histogram) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSampleKeys(m map[string]*sample) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedHistEntryKeys(m map[string]*histEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
