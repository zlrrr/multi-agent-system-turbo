package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
)

func init() {
	Register("blackboard", Description{
		Summary: core.Text{
			EN: "There is no script. Contributors watch a shared workspace and act when its " +
				"state makes them eligible; a round runs everyone eligible, and the run ends " +
				"when a round changes nothing.",
			ZH: "没有脚本。各贡献者盯着一块共享工作区，" +
				"当其状态使自己具备条件时才行动；每一轮运行所有具备条件者，" +
				"直到某一轮什么都没改变为止。",
		},
		Cost: core.Text{
			EN: "Varies with what evidence exists: it never pays for a contributor whose " +
				"precondition the run never satisfies, and control itself costs no model calls.",
			ZH: "随现有证据而变：它绝不会为一个前置条件从未被满足的贡献者付费，" +
				"且控制本身不消耗任何模型调用。",
		},
		Choose: core.Text{
			EN: "Evidence arrives unevenly — some domains are configured, others are not — and " +
				"a fixed script would spend calls discovering that.",
			ZH: "当证据到达不均匀 —— 有些域配置了、有些没有 —— " +
				"而固定脚本会白白花调用去发现这一点时。",
		},
		Avoid: core.Text{
			EN: "You want a predictable transcript. What runs here depends on the state, so two " +
				"incidents on the same system can take visibly different paths.",
			ZH: "当你需要可预期的转录时。这里运行什么取决于状态，" +
				"因此同一系统上的两次故障可能走出明显不同的路径。",
		},
	}, func() (Orchestrator, error) { return &blackboard{}, nil })
}

type blackboard struct{}

func (b *blackboard) Name() string { return "blackboard" }

func (b *blackboard) Describe(lang string) string { return Descriptions(lang)["blackboard"] }

// maxBlackboardRounds is a backstop, not the termination mechanism. Every
// contributor's precondition is falsified by its own contribution, and new
// evidence can only come from investigators that each run once — so the eligible
// set strictly shrinks. The cap exists for a future contributor that breaks that
// argument (design-lld.md §6).
const maxBlackboardRounds = 4

// contributor is one knowledge source: a precondition over the shared state and
// the agent to run when it holds.
type contributor struct {
	name     string
	eligible func(*boardState) bool
	run      func(context.Context, *agent.State) error
}

// boardState is the control component's view of the workspace. It holds only
// what the preconditions read, computed once per round, so every contributor in
// a round sees the same picture.
type boardState struct {
	notes          int
	evidence       int
	hypotheses     int
	assessed       int
	summary        bool
	domainsWithout []tool.Domain // domains that have tools but no note yet
	evidenceDigest string
	lastCorrelated string
}

// Run drives the workspace until a round contributes nothing.
func (b *blackboard) Run(ctx context.Context, st *agent.State) error {
	log := obs.Log(ctx)
	log.Info("topology started", "topology", "blackboard")

	investigated := map[tool.Domain]bool{}
	lastCorrelated := ""

	contributors := []contributor{
		{
			name:     "planner",
			eligible: func(s *boardState) bool { return s.notes == 0 },
			run:      func(c context.Context, a *agent.State) error { _, err := (agent.Planner{}).Step(c, a); return err },
		},
		{
			name: "investigators",
			eligible: func(s *boardState) bool {
				return len(s.domainsWithout) > 0
			},
			run: func(c context.Context, a *agent.State) error {
				for _, d := range activeDomains(a) {
					if investigated[d] {
						continue
					}
					investigated[d] = true
					if _, err := (agent.Investigator{Domain: d}).Step(c, a); err != nil {
						return err
					}
				}
				a.SortNotes(noteOrder(activeDomains(a)))
				return nil
			},
		},
		{
			name: "correlator",
			eligible: func(s *boardState) bool {
				return s.notes > 0 && s.evidenceDigest != s.lastCorrelated
			},
			run: func(c context.Context, a *agent.State) error {
				lastCorrelated = a.EvidenceDigest()
				_, err := (agent.Correlator{}).Step(c, a)
				return err
			},
		},
		{
			name: "critic",
			eligible: func(s *boardState) bool {
				return s.hypotheses > s.assessed
			},
			run: func(c context.Context, a *agent.State) error { _, err := (agent.Critic{}).Step(c, a); return err },
		},
		{
			name: "reporter",
			eligible: func(s *boardState) bool {
				return s.hypotheses > 0 && !s.summary
			},
			run: func(c context.Context, a *agent.State) error { _, err := (agent.Reporter{}).Step(c, a); return err },
		},
	}

	rounds := 0
	for round := 0; round < maxBlackboardRounds; round++ {
		before := observe(st, investigated, lastCorrelated)
		ran := make([]string, 0, len(contributors))

		for _, c := range contributors {
			if !c.eligible(before) {
				continue
			}
			if err := c.run(ctx, st); err != nil {
				return err
			}
			ran = append(ran, c.name)
		}

		if len(ran) == 0 {
			log.Info("blackboard settled", "topology", "blackboard", "rounds", rounds,
				"reason", "no contributor was eligible")
			break
		}
		rounds++

		after := observe(st, investigated, lastCorrelated)
		if !changed(before, after) {
			log.Info("blackboard settled", "topology", "blackboard", "rounds", rounds,
				"reason", "a round changed nothing")
			break
		}
		if truncated, _ := st.Truncated(); truncated {
			break
		}
	}

	recordControl(st, rounds)

	log.Info("topology finished",
		"topology", "blackboard", "rounds", rounds,
		"hypotheses", len(st.Hypotheses()), "evidence", len(st.Evidence()), "steps", st.Steps())
	return nil
}

// observe computes the control component's view. It reads State's existing
// accessors and digests: the workspace needs no storage of its own (plan D-2).
func observe(st *agent.State, investigated map[tool.Domain]bool, lastCorrelated string) *boardState {
	hyps := st.Hypotheses()
	assessed := 0
	for _, h := range hyps {
		if h.Status != core.HypothesisProposed {
			assessed++
		}
	}
	var pending []tool.Domain
	for _, d := range activeDomains(st) {
		if !investigated[d] {
			pending = append(pending, d)
		}
	}
	return &boardState{
		notes:          len(st.Notes()),
		evidence:       len(st.Evidence()),
		hypotheses:     len(hyps),
		assessed:       assessed,
		summary:        strings.TrimSpace(st.Summary()) != "",
		domainsWithout: pending,
		evidenceDigest: st.EvidenceDigest(),
		lastCorrelated: lastCorrelated,
	}
}

// changed reports whether a round moved the workspace at all. Termination is
// this predicate, not the round counter.
func changed(before, after *boardState) bool {
	return before.notes != after.notes ||
		before.evidence != after.evidence ||
		before.hypotheses != after.hypotheses ||
		before.assessed != after.assessed ||
		before.summary != after.summary ||
		len(before.domainsWithout) != len(after.domainsWithout)
}

// recordControl states how the run was driven. A topology whose path depends on
// the state owes the reader an account of the path it took.
func recordControl(st *agent.State, rounds int) {
	st.AddNote(fmt.Sprintf(
		"Blackboard control: settled after %d round(s). Contributors act when the shared "+
			"state makes them eligible, so this path reflects what evidence this run could reach.",
		rounds))
}
