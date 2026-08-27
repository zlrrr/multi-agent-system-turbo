package eval

import (
	"sort"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
)

// Outcome is what one case produced under one topology.
//
// Four facts, kept apart. They are never combined into a score, because a miss
// and a false conclusion are different failures: one leaves an operator where
// they started, the other sends them somewhere wrong with confidence. A
// weighted sum would let a change trading the first for the second look like an
// improvement — and that is precisely the trade an LLM-based system makes when
// it is pushed to be more decisive (design-hld.md §3).
type Outcome struct {
	Case     string `json:"case"`
	Topology string `json:"topology"`
	// Model is the model this cell actually ran, carried from the job rather
	// than read from the shared config: a shared read would attribute every
	// cell's cost to whichever model was configured last, which looks
	// authoritative and is wrong (specs/008-regression-baselines/plan.md RSK-4).
	Model string `json:"model,omitempty"`

	Concluded   []string `json:"concluded"`
	Missing     []string `json:"missing"`
	False       []string `json:"false_conclusions"`
	MissingGaps []string `json:"missing_gaps"`

	Usage         core.Usage    `json:"usage"`
	Duration      time.Duration `json:"duration"`
	TelemetryHits int           `json:"telemetry_hits"`
	Err           error         `json:"-"`
	ErrText       string        `json:"error,omitempty"`
}

// Hit reports whether the case passed: everything expected was concluded,
// nothing ruled out was, and every expected gap was declared.
//
// A conjunction rather than a score, deliberately: there is no partial credit
// that would let a wrong confident answer average out against a right one.
func (o Outcome) Hit() bool {
	return o.Err == nil && len(o.Missing) == 0 && len(o.False) == 0 && len(o.MissingGaps) == 0
}

// Summary is a matrix run.
type Summary struct {
	Outcomes   []Outcome `json:"outcomes"`
	Topologies []string  `json:"topologies"`
	Cases      int       `json:"cases"`
	Provider   string    `json:"provider"`
	Language   string    `json:"-"`
}

// Score compares a report against what the case expects.
//
// It reads ids only: the concluded failure modes and the gap codes. It never
// reads the summary, a hypothesis statement, a recommendation or any other
// prose — a similarity scorer would reward a model that restates the prompt,
// and would produce a number whose meaning nobody could state precisely
// (plan.md §1). TestScoringUsesNoTextSimilarity asserts that structurally.
func Score(c *Case, report *core.Report) Outcome {
	out := Outcome{Case: c.ID(), Concluded: append([]string(nil), report.Conclusions...)}

	concluded := map[string]bool{}
	for _, id := range report.Conclusions {
		concluded[id] = true
	}
	for _, want := range c.Expect.FailureModes {
		if !concluded[want] {
			out.Missing = append(out.Missing, want)
		}
	}
	for _, ruled := range c.Expect.NotFailureModes {
		if concluded[ruled] {
			out.False = append(out.False, ruled)
		}
	}

	// A withheld source is only meaningfully withheld if the run *said* so. A
	// system that reached the same conclusion without the evidence would pass
	// on correctness while having got there by luck.
	declared := map[string]bool{}
	for _, g := range report.Gaps {
		if g.Code != "" {
			declared[g.Code] = true
		}
	}
	for _, want := range c.Expect.Gaps {
		if !declared[want] {
			out.MissingGaps = append(out.MissingGaps, want)
		}
	}

	sort.Strings(out.Concluded)
	sort.Strings(out.Missing)
	sort.Strings(out.False)
	sort.Strings(out.MissingGaps)

	out.Usage = report.Usage
	return out
}

// Totals counts one topology's outcomes across the corpus.
type Totals struct {
	Topology   string     `json:"topology"`
	Cases      int        `json:"cases"`
	Hits       int        `json:"hits"`
	Misses     int        `json:"misses"`
	False      int        `json:"false_conclusions"`
	GapsMissed int        `json:"gaps_missed"`
	Errors     int        `json:"errors"`
	Usage      core.Usage `json:"usage"`
}

// ByTopology aggregates a summary, keeping the outcomes apart.
//
// There is deliberately no combined figure here. Hits, misses, false
// conclusions and missing gaps are reported side by side so a reader can see
// which way a change moved things, rather than a single number that could hide
// a trade between them (CON-002).
func (s Summary) ByTopology() []Totals {
	byName := map[string]*Totals{}
	for _, o := range s.Outcomes {
		t, ok := byName[o.Topology]
		if !ok {
			t = &Totals{Topology: o.Topology}
			byName[o.Topology] = t
		}
		t.Cases++
		switch {
		case o.Err != nil:
			t.Errors++
		case o.Hit():
			t.Hits++
		}
		if len(o.Missing) > 0 {
			t.Misses++
		}
		if len(o.False) > 0 {
			t.False++
		}
		if len(o.MissingGaps) > 0 {
			t.GapsMissed++
		}
		t.Usage.Add(o.Usage)
	}

	out := make([]Totals, 0, len(byName))
	for _, t := range byName {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topology < out[j].Topology })
	return out
}

// scriptedProviders replay a transcript that already contains the answer.
// Reporting their agent-phase results as model quality would be the most
// flattering possible lie, so the renderer says so instead (FR-009).
func scripted(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "mock")
}
