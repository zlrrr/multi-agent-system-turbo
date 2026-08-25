package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm/mock"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
)

// TestPlanExecuteReplansOnFindings is FR-002. The claim that distinguishes this
// topology from `supervisor` is that its next action depends on what the last
// one returned. A run that plans once and executes a fixed list would pass every
// other test in the suite, so the adaptation itself needs its own.
func TestPlanExecuteReplansOnFindings(t *testing.T) {
	st, rec := runTopology(t, "plan-execute", nil)

	rounds := 0
	for _, s := range rec.all() {
		if strings.HasPrefix(s.Actor, string(agent.RoleStrategist)) {
			rounds++
		}
	}
	if rounds < 2 {
		t.Errorf("the strategist ran %d time(s); without a second round nothing was re-planned", rounds)
	}

	// The second strategist call must have been given what the first round
	// established, or it is not re-planning, merely planning again.
	var sawLearned bool
	for _, req := range st.Provider.(*mock.Provider).Calls() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "What the executed objectives established") {
				sawLearned = true
			}
		}
	}
	if !sawLearned {
		t.Error("the strategist was never shown what the executed objectives returned")
	}

	// The adaptation must be visible to a reader of the report, not only in the
	// transcript (design-hld.md §4).
	joined := strings.Join(st.Notes(), "\n")
	if !strings.Contains(joined, "Round 1 objectives") {
		t.Errorf("the round's objectives were not recorded as a note:\n%s", joined)
	}
	if !strings.Contains(joined, "Objective (") {
		t.Errorf("no executed objective was recorded as a note:\n%s", joined)
	}
}

// TestPlanExecuteStopsWhenConverged is the other half of FR-002, and the reason
// the topology exists: it must be able to stop early. A loop that always runs
// its maximum rounds is a sequential supervisor with extra steps.
func TestPlanExecuteStopsWhenConverged(t *testing.T) {
	st, rec := runTopology(t, "plan-execute", nil)

	rounds := 0
	for _, s := range rec.all() {
		if strings.HasPrefix(s.Actor, string(agent.RoleStrategist)) {
			rounds++
		}
	}
	if rounds > 3 {
		t.Errorf("the strategist ran %d times against a cap of 3", rounds)
	}
	// The scripted strategist declares itself done in round 2, so a third round
	// would mean the topology ignored convergence.
	if rounds > 2 {
		t.Errorf("the strategist said it was done after round 2 but ran %d rounds", rounds)
	}

	joined := strings.Join(st.Notes(), "\n")
	if !strings.Contains(joined, "the strategist judged the question settled") {
		t.Errorf("convergence was not recorded, so a reader cannot tell it stopped early:\n%s", joined)
	}
}

// TestPlanExecuteUnanswerableObjectiveIsAResult proves an objective the run
// cannot pursue degrades honestly: it becomes a gap and is fed back, rather than
// failing the run or being silently dropped (FR-008).
func TestPlanExecuteUnanswerableObjectiveIsAResult(t *testing.T) {
	o, err := orchestrator.Open("plan-execute")
	if err != nil {
		t.Fatal(err)
	}
	st := newState(t)
	st.Tools = emptyInvoker(t)
	if err := o.Run(context.Background(), st); err != nil {
		t.Fatalf("a run with no tools must complete: %v", err)
	}

	found := false
	for _, g := range st.Gaps() {
		if strings.Contains(g.Intent, "executor") {
			found = true
			if g.Impact == "" {
				t.Error("the gap does not say what the operator loses")
			}
		}
	}
	if !found {
		t.Errorf("an objective with no tools to pursue it was not recorded as a gap: %+v", st.Gaps())
	}
}
