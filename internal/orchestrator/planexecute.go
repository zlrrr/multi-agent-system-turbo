package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
)

func init() {
	Register("plan-execute", Description{
		Summary: core.Text{
			EN: "A strategist names one or two objectives, an executor pursues them one at a " +
				"time, and the strategist re-plans on what came back — stopping as soon as the " +
				"answer is settled.",
			ZH: "由 strategist 提出一两个目标，executor 逐个推进，" +
				"strategist 再依据返回的结果重新规划 —— 一旦结论已定就立即收手。",
		},
		Cost: core.Text{
			EN: "Lowest of the multi-role topologies when the first objective settles it, and " +
				"the highest when it does not: rounds are sequential, so nothing overlaps.",
			ZH: "在首个目标即可定论时，是多角色拓扑中最便宜的；" +
				"若不能定论则最贵 —— 各轮串行，没有任何重叠。",
		},
		Choose: core.Text{
			EN: "Evidence is expensive, or one check would probably answer it. This is the only " +
				"topology that can stop after one.",
			ZH: "证据获取代价高，或某一项检查很可能就能回答问题时。" +
				"它是唯一一个能在一步之后就收手的拓扑。",
		},
		Avoid: core.Text{
			EN: "You already know several domains are involved. Pursuing them one at a time then " +
				"costs the same calls as `supervisor` and adds the latency of doing them in series.",
			ZH: "当你已经知道涉及多个域时。逐个推进的调用次数与 `supervisor` 相同，" +
				"却额外付出了串行执行的时延。",
		},
	}, func() (Orchestrator, error) { return &planExecute{}, nil })
}

type planExecute struct{}

func (p *planExecute) Name() string { return "plan-execute" }

func (p *planExecute) Describe(lang string) string { return Descriptions(lang)["plan-execute"] }

// maxPlanRounds bounds the adaptive loop. Past three rounds the loop is no
// longer adapting, it is wandering; the run's step budget would cut it off
// anyway, but a topology should bound itself rather than rely on being cut off
// (design-lld.md §4).
const maxPlanRounds = 3

// Run alternates strategist and executor until the strategist converges, then
// finishes through the shared correlate → critique → report tail.
//
// Objectives inside a round run sequentially, and that is the point: this
// topology trades the supervisor's parallelism for the ability to change its
// mind, and a round run concurrently would only make its rounds coarser.
func (p *planExecute) Run(ctx context.Context, st *agent.State) error {
	log := obs.Log(ctx)
	log.Info("topology started", "topology", "plan-execute")

	if _, err := (agent.Planner{}).Step(ctx, st); err != nil {
		return err
	}

	var learned []string
	rounds := 0
	for round := 0; round < maxPlanRounds; round++ {
		strategist := &agent.Strategist{Round: round, Learned: learned}
		out, err := strategist.Step(ctx, st)
		if err != nil {
			return err
		}
		objectives := strategist.Objectives()
		recordRound(st, round, objectives, strategist.Reasoning())

		if len(objectives) == 0 {
			log.Info("strategist converged", "topology", "plan-execute",
				"round", round+1, "reason", "no further objective would change the conclusion")
			break
		}
		rounds++

		for _, o := range objectives {
			result, err := (agent.Executor{Objective: o}).Step(ctx, st)
			if err != nil {
				return err
			}
			if s := strings.TrimSpace(result.Message); s != "" {
				learned = append(learned, fmt.Sprintf("%s: %s", o.Statement, s))
			}
		}
		if out.Done {
			break
		}
		if truncated, _ := st.Truncated(); truncated {
			// The budget is spent. Another round would produce nothing and the
			// truncation is already recorded, so stop rather than spin.
			break
		}
	}

	if _, err := (agent.Correlator{}).Step(ctx, st); err != nil {
		return err
	}
	if _, err := (agent.Critic{}).Step(ctx, st); err != nil {
		return err
	}
	if _, err := (agent.Reporter{}).Step(ctx, st); err != nil {
		return err
	}

	log.Info("topology finished",
		"topology", "plan-execute", "rounds", rounds,
		"hypotheses", len(st.Hypotheses()), "evidence", len(st.Evidence()), "steps", st.Steps())
	return nil
}

// recordRound makes the adaptation visible in the report rather than only in the
// transcript. A topology whose claim is "it changes its mind" has to show the
// reader where it did (design-hld.md §4).
func recordRound(st *agent.State, round int, objectives []agent.Objective, reasoning string) {
	var b strings.Builder
	fmt.Fprintf(&b, "Round %d objectives:\n", round+1)
	if len(objectives) == 0 {
		b.WriteString("- none: the strategist judged the question settled\n")
	}
	for _, o := range objectives {
		fmt.Fprintf(&b, "- %s — %s\n", o.Domain, o.Statement)
	}
	if strings.TrimSpace(reasoning) != "" {
		fmt.Fprintf(&b, "Reasoning: %s", strings.TrimSpace(reasoning))
	}
	st.AddNote(strings.TrimRight(b.String(), "\n"))
}
