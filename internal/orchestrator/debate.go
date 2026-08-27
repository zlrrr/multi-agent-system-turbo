package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
)

func init() {
	Register("debate", Description{
		Summary: core.Text{
			EN: "Investigators gather evidence, a correlator turns it into competing positions, " +
				"an advocate argues each one as strongly as the evidence allows, and a judge " +
				"decides between the arguments.",
			ZH: "由调查员采集证据，correlator 将其转化为相互竞争的立场，" +
				"每个立场由一名 advocate 在证据允许的范围内尽力论证，最后由 judge 在各论证之间裁决。",
		},
		Cost: core.Text{
			EN: "The dearest of the five: `supervisor`'s calls plus one per argued position, " +
				"up to three.",
			ZH: "五者中最贵：在 `supervisor` 的调用之上，每个被论证的立场再加一次，最多三个。",
		},
		Choose: core.Text{
			EN: "Two explanations fit the same evidence and choosing wrong is expensive. " +
				"Assigning an advocate to a position it must defend surfaces the case *for* the " +
				"explanation you were about to dismiss.",
			ZH: "当两种解释都能吻合同一份证据、且选错代价很高时。" +
				"给每个立场指派一名必须为其辩护的 advocate，" +
				"能把你原本打算否掉的那个解释的**正面理由**摆出来。",
		},
		Avoid: core.Text{
			EN: "The evidence already points one way. Staging a debate then pays for arguments " +
				"nobody needed and can lend a weak position more standing than it earned.",
			ZH: "当证据已经明确指向某一侧时。此时办一场辩论只是为没人需要的论证付费，" +
				"还可能让一个弱立场获得它并不配得的分量。",
		},
	}, func() (Orchestrator, error) { return &debate{}, nil })
}

type debate struct{}

func (d *debate) Name() string { return "debate" }

func (d *debate) Describe(lang string) string { return Descriptions(lang)["debate"] }

// maxPositions caps how many hypotheses are argued. A debate between every
// hypothesis the correlator produced costs one call each for diminishing
// returns, and positions below third place are rarely live (design-lld.md §5).
const maxPositions = 3

// Run executes investigate → correlate → advocate → judge → report.
func (d *debate) Run(ctx context.Context, st *agent.State) error {
	log := obs.Log(ctx)
	log.Info("topology started", "topology", "debate")

	if _, err := (agent.Planner{}).Step(ctx, st); err != nil {
		return err
	}
	if err := investigateDomains(ctx, st); err != nil {
		return err
	}
	if _, err := (agent.Correlator{}).Step(ctx, st); err != nil {
		return err
	}

	positions := topPositions(st.Hypotheses(), maxPositions)
	if len(positions) < 2 {
		// Honest about the fact that a debate did not happen, rather than
		// staging one between a position and nothing.
		st.AddGap(core.Gap{
			Intent: "debate", Reason: core.GapUnavailable,
			Detail: fmt.Sprintf("only %d position(s) were available; a debate needs at least two",
				len(positions)),
			Impact: "the hypotheses were challenged individually by the critic rather than argued against each other",
		})
		if _, err := (agent.Critic{}).Step(ctx, st); err != nil {
			return err
		}
	} else {
		if err := argue(ctx, st, positions); err != nil {
			return err
		}
		if _, err := (agent.Judge{}).Step(ctx, st); err != nil {
			return err
		}
	}

	if _, err := (agent.Reporter{}).Step(ctx, st); err != nil {
		return err
	}

	log.Info("topology finished",
		"topology", "debate", "positions", len(positions),
		"hypotheses", len(st.Hypotheses()), "evidence", len(st.Evidence()), "steps", st.Steps())
	return nil
}

// argue runs one advocate per position. They are concurrent for the same reason
// the supervisor's investigators are — independent model calls dominate the wall
// clock — and their notes are re-sorted by position rank afterwards, so
// scheduling cannot change the report (CON-003).
func argue(ctx context.Context, st *agent.State, positions []core.Hypothesis) error {
	var wg sync.WaitGroup
	limit := st.MaxConcurrency
	if limit <= 0 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	errCh := make(chan error, len(positions))

	for i, p := range positions {
		wg.Add(1)
		go func(rank int, position core.Hypothesis) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			advocate := agent.Advocate{
				Position:     position,
				Alternatives: others(positions, position.ID),
				Rank:         rank,
			}
			if _, err := advocate.Step(ctx, st); err != nil {
				errCh <- err
			}
		}(i+1, p)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}

	order := make([]string, 0, len(positions)+1)
	order = append(order, "Investigation plan")
	for i := range positions {
		order = append(order, fmt.Sprintf("Argument %d", i+1))
	}
	st.SortNotes(order)
	return nil
}

// topPositions selects the hypotheses worth arguing, by descending confidence
// with the id as a tie-break so the selection is total and reproducible.
func topPositions(hyps []core.Hypothesis, n int) []core.Hypothesis {
	live := make([]core.Hypothesis, 0, len(hyps))
	for _, h := range hyps {
		if h.Status == core.HypothesisRefuted {
			continue // already settled; arguing it back would be theatre
		}
		live = append(live, h)
	}
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].Confidence != live[j].Confidence {
			return live[i].Confidence > live[j].Confidence
		}
		return live[i].ID < live[j].ID
	})
	if len(live) > n {
		live = live[:n]
	}
	return live
}

func others(all []core.Hypothesis, exceptID string) []core.Hypothesis {
	out := make([]core.Hypothesis, 0, len(all))
	for _, h := range all {
		if h.ID != exceptID {
			out = append(out, h)
		}
	}
	return out
}
