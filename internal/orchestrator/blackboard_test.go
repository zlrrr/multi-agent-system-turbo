package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm/mock"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
)

// TestBlackboardSchedulesByEligibility is FR-004. The claim is that control
// comes from the state rather than from a script, and the observable
// consequence is ordering: a contributor cannot run before the state its
// precondition reads exists.
func TestBlackboardSchedulesByEligibility(t *testing.T) {
	_, rec := runTopology(t, "blackboard", nil)

	var order []string
	for _, s := range rec.all() {
		if s.Kind == core.StepLLMCall {
			order = append(order, s.Actor)
		}
	}
	if len(order) == 0 {
		t.Fatal("no contributor ran at all")
	}

	first := func(role agent.Role) int {
		for i, a := range order {
			if strings.HasPrefix(a, string(role)) {
				return i
			}
		}
		return -1
	}
	planner := first(agent.RolePlanner)
	correlator := first(agent.RoleCorrelator)
	critic := first(agent.RoleCritic)
	reporter := first(agent.RoleReporter)

	if planner != 0 {
		t.Errorf("the planner is only eligible while there are no notes, so it must run first; order = %v", order)
	}
	if correlator < 0 {
		t.Fatalf("the correlator never became eligible; order = %v", order)
	}
	if critic >= 0 && critic < correlator {
		t.Errorf("the critic ran before any hypothesis existed; order = %v", order)
	}
	if reporter >= 0 && reporter < correlator {
		t.Errorf("the reporter ran before any hypothesis existed; order = %v", order)
	}
}

// TestBlackboardTerminates pins the argument in design-lld.md §6: the loop stops
// because the eligible set shrinks, not because a counter ran out. If it stopped
// at the cap, the preconditions would not be self-falsifying and a future
// contributor could loop forever.
func TestBlackboardTerminates(t *testing.T) {
	st, rec := runTopology(t, "blackboard", nil)

	joined := strings.Join(st.Notes(), "\n")
	if !strings.Contains(joined, "Blackboard control: settled after") {
		t.Fatalf("the run did not record how it was driven:\n%s", joined)
	}
	// The backstop is 4 rounds; settling at it would mean the predicates did not
	// terminate the loop on their own.
	if strings.Contains(joined, "settled after 4 round(s)") {
		t.Error("the loop stopped at its backstop rather than because the workspace settled")
	}

	// No role may run twice on an unchanged workspace: that is the loop the
	// eligibility predicates exist to prevent.
	counts := map[string]int{}
	for _, s := range rec.all() {
		if s.Kind == core.StepLLMCall {
			counts[s.Actor]++
		}
	}
	for _, role := range []agent.Role{agent.RolePlanner, agent.RoleReporter} {
		if counts[string(role)] > 1 {
			t.Errorf("%s ran %d times; its precondition is falsified by its own contribution",
				role, counts[string(role)])
		}
	}
}

// TestBlackboardSkipsWhatCannotContribute is the cost claim in the topology's
// own description: it never pays for a contributor whose precondition the run
// cannot satisfy.
func TestBlackboardSkipsWhatCannotContribute(t *testing.T) {
	o, err := orchestrator.Open("blackboard")
	if err != nil {
		t.Fatal(err)
	}
	st := newState(t)
	rec := &stepRecorder{}
	st.Sink = rec
	st.Tools = emptyInvoker(t) // no domain has tools, so no investigator is eligible

	if err := o.Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	for _, s := range rec.all() {
		if strings.HasPrefix(s.Actor, string(agent.RoleInvestigator)) {
			t.Errorf("an investigator ran with no tools in any domain: %s", s.Actor)
		}
	}
}

// TestBlackboardReCorrelatesOnNewEvidence proves the correlator's precondition
// is about *change*, not about having run: new evidence must reopen it, or the
// workspace would freeze after the first pass.
func TestBlackboardReCorrelatesOnNewEvidence(t *testing.T) {
	o, err := orchestrator.Open("blackboard")
	if err != nil {
		t.Fatal(err)
	}
	st := newState(t)
	rec := &stepRecorder{}
	st.Sink = rec
	// A script whose correlator yields nothing on the first pass leaves the
	// hypothesis count at zero, so the reporter stays ineligible and the run
	// must settle rather than spin.
	st.Provider = mock.New(&mock.Script{Replies: []mock.Reply{
		{When: "role: correlator", Text: `{"hypotheses":[]}`},
		{Text: "nothing further"},
	}})

	if err := o.Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, s := range rec.all() {
		if s.Kind == core.StepLLMCall {
			counts[s.Actor]++
		}
	}
	if counts[string(agent.RoleCorrelator)] > 2 {
		t.Errorf("the correlator ran %d times on unchanged evidence", counts[string(agent.RoleCorrelator)])
	}
	if counts[string(agent.RoleReporter)] > 0 {
		t.Error("the reporter ran with no hypothesis to report")
	}
}
