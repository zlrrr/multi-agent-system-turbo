package orchestrator

import (
	"context"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
)

func init() {
	Register("single", Description{
		Summary: core.Text{
			EN: "One generalist agent holding every tool, doing the whole job alone.",
			ZH: "一个通才 Agent 持有全部工具，独自完成整件事。",
		},
		Cost: core.Text{
			EN: "Cheapest of the five: one model conversation, no hand-offs.",
			ZH: "五者中最便宜：一次模型对话，没有任何交接。",
		},
		Choose: core.Text{
			EN: "You want the control condition — the baseline every other topology has to beat.",
			ZH: "你需要对照组 —— 其余每个拓扑都必须胜过的那条基线。",
		},
		Avoid: core.Text{
			EN: "The incident is ambiguous. With no specialisation and no refutation step, " +
				"a plausible first explanation goes unchallenged.",
			ZH: "故障本身有歧义时。它没有分工、也没有反驳环节，" +
				"一个看似合理的初始解释不会受到任何挑战。",
		},
	}, func() (Orchestrator, error) { return &single{}, nil })
}

type single struct{}

func (s *single) Name() string { return "single" }

func (s *single) Describe(lang string) string { return Descriptions(lang)["single"] }

// Run gives one agent the whole job. Keeping this as a real topology rather than
// a degenerate case matters: without a baseline, "the supervisor topology works
// well" is not a claim anyone can check.
func (s *single) Run(ctx context.Context, st *agent.State) error {
	obs.Log(ctx).Info("topology started", "topology", "single")
	if _, err := (agent.Generalist{}).Step(ctx, st); err != nil {
		return err
	}
	obs.Log(ctx).Info("topology finished",
		"topology", "single", "hypotheses", len(st.Hypotheses()), "steps", st.Steps())
	return nil
}
