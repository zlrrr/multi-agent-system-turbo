package eval

import (
	"sort"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Transition is what happened to one cell between a baseline and a run.
//
// Names rather than a signed delta, deliberately. "Regressed" and "improved"
// are different facts, and a number that could be either makes them one — which
// is the same collapse feature 006 refused when it declined to score an
// outcome (specs/008-regression-baselines/plan.md D-3).
type Transition string

const (
	Regressed      Transition = "regressed"
	Improved       Transition = "improved"
	KnownBad       Transition = "known-bad"
	ChangedFailure Transition = "changed-failure"
	New            Transition = "new"
	NotRun         Transition = "not-run"
)

// Change is one cell's transition.
type Change struct {
	Cell       Cell       `json:"cell"`
	Was        Class      `json:"was,omitempty"`
	Transition Transition `json:"transition"`
	Detail     string     `json:"detail,omitempty"`
}

// Delta is a run compared against a baseline.
//
// There is deliberately no total, no net and no score: a change that fixes two
// cells and breaks one is two improvements and one regression, and the person
// reviewing it decides. Summing them would let one hide the other (CON-001).
type Delta struct {
	Changes  []Change `json:"changes"`
	Mismatch string   `json:"provider_mismatch,omitempty"`
	Caveats  []string `json:"caveats"`
	Baseline string   `json:"baseline_recorded,omitempty"`
	Provider string   `json:"provider,omitempty"`
}

// Compare walks a baseline and a run, naming what happened to every cell.
func Compare(base Baseline, s Summary) Delta {
	d := Delta{Baseline: base.Recorded, Provider: s.Provider}

	if base.Provider != "" && s.Provider != "" && base.Provider != s.Provider {
		d.Mismatch = errs.New("MAS-9107", base.Provider, s.Provider).Message("en")
	}

	was := base.index()
	now := map[string]Cell{}
	for _, o := range s.Outcomes {
		c := Cell{
			Case: o.Case, Topology: o.Topology, Model: o.Model, Class: o.Class(),
			Missing: o.Missing, False: o.False, GapsMissed: o.MissingGaps,
		}
		now[c.Key()] = c
	}

	for key, cell := range now {
		prior, known := was[key]
		switch {
		case !known:
			d.Changes = append(d.Changes, Change{Cell: cell, Transition: New,
				Detail: describeCell(cell)})
		case prior.Class == ClassHit && cell.Class == ClassHit:
			// An unchanged pass is not news.
		case prior.Class == ClassHit:
			d.Changes = append(d.Changes, Change{Cell: cell, Was: prior.Class,
				Transition: Regressed, Detail: describeCell(cell)})
		case cell.Class == ClassHit:
			d.Changes = append(d.Changes, Change{Cell: cell, Was: prior.Class,
				Transition: Improved, Detail: describeCell(prior)})
		case cell.sameFailure(prior):
			// Known-bad is emitted every run, not only on change: a gap that
			// stops being visible is a gap that stops being fixed (RSK-2).
			d.Changes = append(d.Changes, Change{Cell: cell, Was: prior.Class,
				Transition: KnownBad, Detail: describeCell(cell)})
		default:
			d.Changes = append(d.Changes, Change{Cell: cell, Was: prior.Class,
				Transition: ChangedFailure,
				Detail:     describeCell(prior) + " → " + describeCell(cell)})
		}
	}

	for key, cell := range was {
		if _, ran := now[key]; !ran {
			d.Changes = append(d.Changes, Change{Cell: cell, Was: cell.Class,
				Transition: NotRun})
		}
	}

	sort.SliceStable(d.Changes, func(i, j int) bool {
		if d.Changes[i].Transition != d.Changes[j].Transition {
			return transitionRank(d.Changes[i].Transition) < transitionRank(d.Changes[j].Transition)
		}
		return d.Changes[i].Cell.Key() < d.Changes[j].Cell.Key()
	})

	d.Caveats = deltaCaveats(s, "en")
	return d
}

// transitionRank orders the report: what failed first, what needs a decision
// next, what is merely informational last.
func transitionRank(t Transition) int {
	switch t {
	case Regressed:
		return 0
	case ChangedFailure:
		return 1
	case Improved:
		return 2
	case KnownBad:
		return 3
	case New:
		return 4
	default:
		return 5
	}
}

// Gate reports whether the comparison should fail a build.
//
// Only regressions do. A known-bad cell keeps CI green while staying listed,
// which is what stops a case being deleted to get a green build — and the case
// is the only record that the gap exists (design-hld.md §3).
func (d Delta) Gate() error {
	var keys []string
	for _, c := range d.Changes {
		if c.Transition == Regressed {
			keys = append(keys, c.Cell.Key())
		}
	}
	if len(keys) == 0 {
		return nil
	}
	shown := keys
	if len(shown) > 3 {
		shown = append(append([]string{}, shown[:3]...), "…")
	}
	return errs.New("MAS-9105", len(keys), strings.Join(shown, ", "))
}

// Count returns how many cells took one transition.
func (d Delta) Count(t Transition) int {
	n := 0
	for _, c := range d.Changes {
		if c.Transition == t {
			n++
		}
	}
	return n
}

// describeCell says what a cell's failure consisted of, in ids only.
func describeCell(c Cell) string {
	var parts []string
	if len(c.Missing) > 0 {
		parts = append(parts, "not concluded: "+strings.Join(c.Missing, ", "))
	}
	if len(c.False) > 0 {
		parts = append(parts, "wrongly concluded: "+strings.Join(c.False, ", "))
	}
	if len(c.GapsMissed) > 0 {
		parts = append(parts, "gap not declared: "+strings.Join(c.GapsMissed, ", "))
	}
	if len(parts) == 0 {
		return string(c.Class)
	}
	return strings.Join(parts, "; ")
}

// deltaCaveats are what a comparison does not mean.
func deltaCaveats(s Summary, lang string) []string {
	sample := core.Text{
		EN: "Each cell is one sample. Under a deterministic provider that is a measurement; " +
			"under a real model it is a single draw, and two draws can differ. This comparison " +
			"reports what changed, not whether the change is significant — one run cannot " +
			"support that claim and none is made.",
		ZH: "每个格子只是一个样本。在确定性 provider 下，那是一次测量；" +
			"在真实模型下，它是一次抽样，而两次抽样可以不同。本次比较报告的是“什么变了”，" +
			"而不是“这个变化是否显著” —— 一次运行支撑不了那个论断，因此我们也不做那个论断。",
	}
	out := []string{sample.In(lang)}
	out = append(out, caveatsFor(s, lang).Rendered...)
	return out
}
