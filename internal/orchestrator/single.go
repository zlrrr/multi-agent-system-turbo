package orchestrator

import (
	"context"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
)

func init() {
	Register("single",
		"One generalist agent holding every tool. The control condition for topology experiments: "+
			"cheapest in tokens, but with no role specialisation and no critique step.",
		func() (Orchestrator, error) { return &single{}, nil })
}

type single struct{}

func (s *single) Name() string { return "single" }

func (s *single) Describe() string { return Descriptions()["single"] }

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
