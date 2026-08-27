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

// TestDebateProducesAdjudicatedPositions is FR-003. Two properties make this a
// debate rather than a second critique: every live position gets an argument
// made *for* it, and a distinct role decides between those arguments.
func TestDebateProducesAdjudicatedPositions(t *testing.T) {
	st, rec := runTopology(t, "debate", nil)

	advocates, judges := 0, 0
	for _, s := range rec.all() {
		switch {
		case strings.HasPrefix(s.Actor, string(agent.RoleAdvocate)):
			advocates++
		case strings.HasPrefix(s.Actor, string(agent.RoleJudge)):
			judges++
		}
	}
	if advocates < 2 {
		t.Errorf("%d advocate(s) ran; with fewer than two there is no debate", advocates)
	}
	if advocates > 3 {
		t.Errorf("%d advocates ran against a cap of 3", advocates)
	}
	if judges != 1 {
		t.Errorf("the judge ran %d time(s), want exactly 1", judges)
	}

	// The judge must actually have decided: a run where every position is still
	// `proposed` has adjudicated nothing.
	decided := 0
	supported := 0
	for _, h := range st.Hypotheses() {
		if h.Status != core.HypothesisProposed {
			decided++
		}
		if h.Status == core.HypothesisSupported {
			supported++
		}
	}
	if decided == 0 {
		t.Error("no position was decided; the judge adjudicated nothing")
	}
	if supported > 1 {
		t.Errorf("%d positions are supported; the judge's contract is that at most one wins", supported)
	}

	// The arguments have to reach the reader, in a fixed order.
	joined := strings.Join(st.Notes(), "\n")
	if !strings.Contains(joined, "Argument 1 for") {
		t.Errorf("no argument was recorded as a note:\n%s", joined)
	}
}

// TestDebateAdvocatesSeeTheAlternatives proves an advocate is arguing against
// something. An advocate shown only its own position writes a summary, not an
// argument.
func TestDebateAdvocatesSeeTheAlternatives(t *testing.T) {
	st, _ := runTopology(t, "debate", nil)

	sawCompeting := false
	for _, req := range st.Provider.(*mock.Provider).Calls() {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "The competing positions are") &&
				strings.Contains(m.Content, "h-") {
				sawCompeting = true
			}
		}
	}
	if !sawCompeting {
		t.Error("no advocate was shown the positions it was arguing against")
	}
}

// TestDebateWithoutPositionsFallsBack is the honest-degradation half of FR-003.
// With fewer than two live positions there is nothing to debate, and staging one
// anyway would be theatre: the topology says so and uses the critic instead.
func TestDebateWithoutPositionsFallsBack(t *testing.T) {
	o, err := orchestrator.Open("debate")
	if err != nil {
		t.Fatal(err)
	}
	st := newState(t)
	rec := &stepRecorder{}
	st.Sink = rec
	// A provider that produces exactly one hypothesis leaves nothing to argue.
	st.Provider = mock.New(&mock.Script{Replies: []mock.Reply{
		{
			When: "role: correlator",
			Text: `{"hypotheses":[{"statement":"Only one explanation fits.","confidence":0.9,"supporting":[],"rationale":"single"}]}`,
		},
		{
			When: "role: critic",
			Text: `{"assessments":[{"id":"h-1","status":"supported","confidence":0.9,"rationale":"nothing contradicts it"}]}`,
		},
		{
			When: "role: reporter",
			Text: `{"summary":"One explanation fits the evidence.","recommendations":[{"statement":"Confirm the reading before acting.","risk":"low","rationale":"cheapest next step"}]}`,
		},
		{Text: "no further analysis"},
	}})

	if err := o.Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	for _, s := range rec.all() {
		if strings.HasPrefix(s.Actor, string(agent.RoleAdvocate)) {
			t.Error("an advocate argued in a run with only one position")
		}
	}
	found := false
	for _, g := range st.Gaps() {
		if g.Intent == "debate" {
			found = true
			if g.Impact == "" {
				t.Error("the gap does not say what happened instead")
			}
		}
	}
	if !found {
		t.Errorf("a debate that did not happen was not recorded: %+v", st.Gaps())
	}
	if strings.TrimSpace(st.Summary()) == "" {
		t.Error("the fallback path produced no summary")
	}
}
