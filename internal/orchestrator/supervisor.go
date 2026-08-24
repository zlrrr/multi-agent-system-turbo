package orchestrator

import (
	"context"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
)

func init() {
	Register("supervisor",
		"A planner directs specialised investigators — one per evidence domain, run concurrently — "+
			"whose findings a correlator merges into hypotheses, a critic challenges, and a reporter writes up. "+
			"The default: specialisation plus an explicit refutation step.",
		func() (Orchestrator, error) { return &supervisor{}, nil })
}

type supervisor struct{}

func (s *supervisor) Name() string { return "supervisor" }

func (s *supervisor) Describe() string { return Descriptions()["supervisor"] }

// investigationDomains are attempted in a fixed order so a run is reproducible
// even though the investigators execute concurrently.
var investigationDomains = []tool.Domain{
	tool.DomainMetrics, tool.DomainLogs, tool.DomainCluster, tool.DomainHost, tool.DomainSource,
}

// Run executes plan → investigate → correlate → critique → report.
//
// Investigators run concurrently because they touch disjoint evidence sources
// and independent I/O is the dominant cost of a diagnosis. They write only
// through State's guarded accessors, and their narrative notes are re-ordered
// deterministically afterwards, so concurrency never changes the report.
func (s *supervisor) Run(ctx context.Context, st *agent.State) error {
	log := obs.Log(ctx)
	log.Info("topology started", "topology", "supervisor")

	if _, err := (agent.Planner{}).Step(ctx, st); err != nil {
		return err
	}

	active := activeDomains(st)
	if len(active) > 0 {
		var wg sync.WaitGroup
		limit := st.MaxConcurrency
		if limit <= 0 {
			limit = 4
		}
		sem := make(chan struct{}, limit)
		errCh := make(chan error, len(active))

		for _, d := range active {
			wg.Add(1)
			go func(domain tool.Domain) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if _, err := (agent.Investigator{Domain: domain}).Step(ctx, st); err != nil {
					errCh <- err
				}
			}(d)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				return err
			}
		}
		st.SortNotes(noteOrder(active))
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
		"topology", "supervisor", "hypotheses", len(st.Hypotheses()),
		"evidence", len(st.Evidence()), "steps", st.Steps())
	return nil
}

// activeDomains returns the domains that actually have tools in this run, so an
// offline run does not spend a model call discovering it has no cluster access.
func activeDomains(st *agent.State) []tool.Domain {
	var out []tool.Domain
	for _, d := range investigationDomains {
		if len(st.Tools.Registry().InDomains(d)) > 0 {
			out = append(out, d)
		}
	}
	return out
}

// noteOrder gives the deterministic ordering investigator notes are sorted into.
func noteOrder(domains []tool.Domain) []string {
	out := make([]string, 0, len(domains)+1)
	out = append(out, "Investigation plan")
	for _, d := range domains {
		out = append(out, title(string(d)))
	}
	return out
}

func title(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-32) + s[1:]
	}
	return s
}
