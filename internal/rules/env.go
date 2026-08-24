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

	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/loki"
	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/promql"
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
func bindEvidence(ev core.Evidence) any {
	switch payload := ev.Payload.(type) {
	case promql.Result:
		return metricView(payload, ev.Summary)
	case *promql.Result:
		return metricView(*payload, ev.Summary)
	case loki.Result:
		return logView(payload, ev.Summary)
	case *loki.Result:
		return logView(*payload, ev.Summary)
	case map[string]any:
		if lines, ok := payload["lines"].([]string); ok {
			return LogView{
				Empty: len(lines) == 0, Count: len(lines), Lines: lines,
				Text: strings.Join(lines, "\n"), Summary: ev.Summary,
			}
		}
		return ObjectView{Empty: len(payload) == 0, Data: payload, Summary: ev.Summary}
	default:
		return ObjectView{Empty: ev.Payload == nil, Data: map[string]any{}, Summary: ev.Summary}
	}
}

func metricView(r promql.Result, summary string) MetricView {
	v := MetricView{
		Empty: r.Empty(), Series: len(r.Series), Summary: summary,
		ByLabel:   map[string]float64{},
		Min:       math.Inf(1),
		Max:       math.Inf(-1),
		Latest:    math.Inf(-1),
		LatestMin: math.Inf(1),
	}
	if r.Empty() {
		v.Min, v.Max, v.Latest, v.LatestMin = 0, 0, 0, 0
		return v
	}
	var sum float64
	var first, last float64
	haveFirst := false
	for _, s := range r.Series {
		if s.Last > v.Latest {
			v.Latest = s.Last
		}
		if s.Last < v.LatestMin {
			v.LatestMin = s.Last
		}
		if s.Min < v.Min {
			v.Min = s.Min
		}
		if s.Max > v.Max {
			v.Max = s.Max
		}
		v.Count += s.Count
		sum += s.Avg * float64(s.Count)
		v.Sum += s.Last
		v.ByLabel[labelKey(s.Metric)] = s.Last
		if len(s.Points) > 0 {
			if !haveFirst {
				first, haveFirst = s.Points[0].Value, true
			}
			last = s.Points[len(s.Points)-1].Value
		}
	}
	if v.Count > 0 {
		v.Avg = sum / float64(v.Count)
	}
	v.Last = v.Latest
	if haveFirst {
		v.Delta = last - first
	}
	return v
}

func labelKey(m map[string]string) string {
	for _, k := range []string{"instance", "pod", "topic", "node", "job"} {
		if val, ok := m[k]; ok {
			return val
		}
	}
	if name, ok := m["__name__"]; ok {
		return name
	}
	return "series"
}

func logView(r loki.Result, summary string) LogView {
	lines := make([]string, 0, len(r.Lines))
	for _, l := range r.Lines {
		lines = append(lines, l.Text)
	}
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
