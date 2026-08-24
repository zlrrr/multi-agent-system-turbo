// Package rules runs deterministic diagnostic playbooks: ordered collect →
// evaluate → conclude steps that reach a verdict with no model in the loop
// (project goal G9.1, Constitution Art. VII.3).
//
// Governs: specs/001-mvp-core/design-lld.md §2.12
package rules

import (
	"math"
	"regexp"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
)

// MetricView is how a collected metric appears to a playbook expression.
//
// Threshold checks almost always mean "is any instance over the line", so
// Latest is the maximum of each series' most recent value rather than an
// arbitrary series' value. LatestMin gives the other end for "have all
// instances recovered" checks.
type MetricView struct {
	Empty     bool               `expr:"empty" json:"empty"`
	Series    int                `expr:"series" json:"series"`
	Count     int                `expr:"count" json:"count"`
	Latest    float64            `expr:"latest" json:"latest"`
	Last      float64            `expr:"last" json:"last"`
	LatestMin float64            `expr:"latestMin" json:"latest_min"`
	Min       float64            `expr:"min" json:"min"`
	Max       float64            `expr:"max" json:"max"`
	Avg       float64            `expr:"avg" json:"avg"`
	Sum       float64            `expr:"sum" json:"sum"`
	Delta     float64            `expr:"delta" json:"delta"`
	ByLabel   map[string]float64 `expr:"byLabel" json:"by_label"`
	Summary   string             `expr:"summary" json:"summary"`
}

// LogView is how collected log lines appear to a playbook expression.
type LogView struct {
	Empty   bool     `expr:"empty" json:"empty"`
	Count   int      `expr:"count" json:"count"`
	Lines   []string `expr:"lines" json:"lines"`
	Text    string   `expr:"text" json:"text"`
	Summary string   `expr:"summary" json:"summary"`
}

// ObjectView is how any other evidence appears: its payload plus a summary.
type ObjectView struct {
	Empty   bool           `expr:"empty" json:"empty"`
	Data    map[string]any `expr:"data" json:"data"`
	Summary string         `expr:"summary" json:"summary"`
}

// bindEvidence converts one piece of evidence into its expression view.
//
// It dispatches on the payload's own capability interfaces rather than on
// concrete collector types, so the reasoning layer never imports a collector
// (design-hld.md §3, enforced by internal/audit).
func bindEvidence(ev core.Evidence) any {
	switch payload := ev.Payload.(type) {
	case core.SeriesPayload:
		return metricView(payload.Stats(), ev.Summary)
	case core.LinesPayload:
		return logView(payload.LogLines(), ev.Summary)
	case map[string]any:
		if lines, ok := stringSlice(payload["lines"]); ok {
			return logView(lines, ev.Summary)
		}
		return ObjectView{Empty: len(payload) == 0, Data: payload, Summary: ev.Summary}
	default:
		return ObjectView{Empty: ev.Payload == nil, Data: map[string]any{}, Summary: ev.Summary}
	}
}

func stringSlice(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func metricView(s core.SeriesStats, summary string) MetricView {
	return MetricView{
		Empty: s.Empty, Series: s.Series, Count: s.Count,
		Latest: s.Latest, Last: s.Latest, LatestMin: s.LatestMin,
		Min: s.Min, Max: s.Max, Avg: s.Avg, Sum: s.Sum, Delta: s.Delta,
		ByLabel: s.ByLabel, Summary: summary,
	}
}

func logView(lines []string, summary string) LogView {
	return LogView{
		Empty: len(lines) == 0, Count: len(lines), Lines: lines,
		Text: strings.Join(lines, "\n"), Summary: summary,
	}
}

// helpers is the function set available to playbook expressions. The environment
// deliberately exposes nothing else: no process environment, no filesystem, no
// network — an expression can only reason about what the playbook collected.
func helpers() map[string]any {
	return map[string]any{
		"contains": func(haystack, needle string) bool {
			return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
		},
		"matches": func(s, pattern string) bool {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return false
			}
			return re.MatchString(s)
		},
		"countMatching": func(lines []string, pattern string) int {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return 0
			}
			n := 0
			for _, l := range lines {
				if re.MatchString(l) {
					n++
				}
			}
			return n
		},
		"lower": strings.ToLower,
		"ratio": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"pct": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b * 100
		},
		"isNaN":  math.IsNaN,
		"finite": func(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) },
	}
}
