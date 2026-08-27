package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
)

// Planner decides what the investigators should pursue.
type Planner struct{}

// Role identifies this agent.
func (Planner) Role() Role { return RolePlanner }

// Step produces an investigation plan and records it as a note.
func (Planner) Step(ctx context.Context, s *State) (Outcome, error) {
	text, err := runLoop(ctx, s, loopOptions{
		role:     RolePlanner,
		label:    "planner",
		system:   systemPreamble + languageInstruction(s.Language),
		user:     promptContext(s) + "\n\n" + plannerInstruction,
		maxTurns: 1,
	})
	if err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(text) != "" {
		s.AddNote("Investigation plan:\n" + text)
	}
	return Outcome{Done: true, Message: text}, nil
}

// Investigator gathers evidence from exactly one domain. Constraining each
// investigator to its own tools is what keeps their contributions independent,
// which is what makes correlating them meaningful.
type Investigator struct {
	Domain tool.Domain
}

// Role identifies this agent.
func (Investigator) Role() Role { return RoleInvestigator }

// Label names this investigator's specialisation.
func (i Investigator) Label() string { return fmt.Sprintf("investigator (%s)", i.Domain) }

// Step investigates one evidence domain and records a factual note.
func (i Investigator) Step(ctx context.Context, s *State) (Outcome, error) {
	names := toolNames(s, i.Domain)
	if len(names) == 0 {
		s.AddGap(core.Gap{
			Intent: i.Label(), Reason: core.GapNotConfigured,
			Detail: fmt.Sprintf("no %s tools are available in this run", i.Domain),
			Impact: fmt.Sprintf("the %s domain was not investigated", i.Domain),
		})
		return Outcome{Done: true}, nil
	}
	text, err := runLoop(ctx, s, loopOptions{
		role:      RoleInvestigator,
		label:     i.Label(),
		system:    systemPreamble + languageInstruction(s.Language),
		user:      promptContext(s) + "\n\n" + fmt.Sprintf(investigatorInstruction, i.Domain, i.Domain),
		toolNames: names,
		maxTurns:  6,
	})
	if err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(text) != "" {
		s.AddNote(fmt.Sprintf("%s findings:\n%s", strings.Title(string(i.Domain)), text)) //nolint:staticcheck // ASCII domain names only
	}
	return Outcome{Done: true, Message: text}, nil
}

func toolNames(s *State, domain tool.Domain) []string {
	var out []string
	for _, t := range s.Tools.Registry().InDomains(domain) {
		out = append(out, t.Name())
	}
	return out
}

// Correlator turns evidence into ranked hypotheses.
type Correlator struct{}

// Role identifies this agent.
func (Correlator) Role() Role { return RoleCorrelator }

type correlatorReply struct {
	Hypotheses []struct {
		Statement     string   `json:"statement"`
		Confidence    float64  `json:"confidence"`
		Supporting    []string `json:"supporting"`
		Contradicting []string `json:"contradicting"`
		Rationale     string   `json:"rationale"`
	} `json:"hypotheses"`
}

// Step produces hypotheses from the collected evidence.
func (Correlator) Step(ctx context.Context, s *State) (Outcome, error) {
	text, err := runLoop(ctx, s, loopOptions{
		role:     RoleCorrelator,
		label:    "correlator",
		system:   systemPreamble + languageInstruction(s.Language),
		user:     promptContext(s) + notesSection(s) + "\n\n" + correlatorInstruction,
		maxTurns: 1,
	})
	if err != nil {
		return Outcome{}, err
	}
	var reply correlatorReply
	if !decodeJSON(s, RoleCorrelator, text, &reply) {
		return Outcome{Done: true}, nil
	}
	for _, h := range reply.Hypotheses {
		if strings.TrimSpace(h.Statement) == "" {
			continue
		}
		s.AddHypothesis(core.Hypothesis{
			Statement:     h.Statement,
			Confidence:    clamp(h.Confidence),
			Supporting:    h.Supporting,
			Contradicting: h.Contradicting,
			Rationale:     h.Rationale,
		})
	}
	return Outcome{Done: true, Message: text}, nil
}

// Critic challenges each hypothesis against the evidence.
type Critic struct{}

// Role identifies this agent.
func (Critic) Role() Role { return RoleCritic }

type criticReply struct {
	Assessments []struct {
		ID         string  `json:"id"`
		Status     string  `json:"status"`
		Confidence float64 `json:"confidence"`
		Rationale  string  `json:"rationale"`
	} `json:"assessments"`
}

// Step adjusts hypothesis status and confidence.
func (Critic) Step(ctx context.Context, s *State) (Outcome, error) {
	hyps := s.Hypotheses()
	if len(hyps) == 0 {
		return Outcome{Done: true, Message: "no hypotheses to challenge"}, nil
	}
	var b strings.Builder
	b.WriteString("\n\n## Hypotheses to challenge\n")
	for _, h := range hyps {
		fmt.Fprintf(&b, "- %s (confidence %.2f): %s\n  rationale: %s\n  supporting: %v\n",
			h.ID, h.Confidence, h.Statement, h.Rationale, h.Supporting)
	}
	text, err := runLoop(ctx, s, loopOptions{
		role:     RoleCritic,
		label:    "critic",
		system:   systemPreamble + languageInstruction(s.Language),
		user:     promptContext(s) + b.String() + "\n\n" + criticInstruction,
		maxTurns: 1,
	})
	if err != nil {
		return Outcome{}, err
	}
	var reply criticReply
	if !decodeJSON(s, RoleCritic, text, &reply) {
		return Outcome{Done: true}, nil
	}
	for _, a := range reply.Assessments {
		status := core.HypothesisStatus(strings.ToLower(strings.TrimSpace(a.Status)))
		switch status {
		case core.HypothesisSupported, core.HypothesisRefuted, core.HypothesisInconclusive:
		default:
			status = ""
		}
		s.UpdateHypothesis(a.ID, status, clamp(a.Confidence), a.Rationale)
	}
	return Outcome{Done: true, Message: text}, nil
}

// Reporter writes the summary and the advisory recommendations.
type Reporter struct{}

// Role identifies this agent.
func (Reporter) Role() Role { return RoleReporter }

type reporterReply struct {
	Summary         string `json:"summary"`
	Recommendations []struct {
		Statement string   `json:"statement"`
		Risk      string   `json:"risk"`
		Rationale string   `json:"rationale"`
		Refs      []string `json:"refs"`
	} `json:"recommendations"`
}

// Step writes the operator-facing summary and recommendations.
func (Reporter) Step(ctx context.Context, s *State) (Outcome, error) {
	var b strings.Builder
	b.WriteString("\n\n## Hypotheses after critique\n")
	for _, h := range s.Hypotheses() {
		fmt.Fprintf(&b, "- %s [%s, confidence %.2f]: %s\n  %s\n",
			h.ID, h.Status, h.Confidence, h.Statement, h.Rationale)
	}
	text, err := runLoop(ctx, s, loopOptions{
		role:     RoleReporter,
		label:    "reporter",
		system:   systemPreamble + languageInstruction(s.Language),
		user:     promptContext(s) + notesSection(s) + b.String() + "\n\n" + reporterInstruction,
		maxTurns: 1,
	})
	if err != nil {
		return Outcome{}, err
	}
	var reply reporterReply
	if !decodeJSON(s, RoleReporter, text, &reply) {
		// Structured output failed, but the prose is still worth keeping.
		if strings.TrimSpace(text) != "" {
			s.SetSummary(strings.TrimSpace(text))
		}
		return Outcome{Done: true}, nil
	}
	if strings.TrimSpace(reply.Summary) != "" {
		s.SetSummary(reply.Summary)
	}
	for _, r := range reply.Recommendations {
		if strings.TrimSpace(r.Statement) == "" {
			continue
		}
		s.AddRecommendation(core.NewRecommendation(r.Statement, parseRisk(r.Risk), r.Rationale, r.Refs...))
	}
	return Outcome{Done: true, Message: text}, nil
}

// Generalist is the single-agent control condition: one agent with every tool,
// doing the whole job alone. It exists so topology comparisons have a baseline.
type Generalist struct{}

// Role identifies this agent.
func (Generalist) Role() Role { return RoleGeneralist }

type generalistReply struct {
	Summary    string `json:"summary"`
	Hypotheses []struct {
		Statement  string   `json:"statement"`
		Confidence float64  `json:"confidence"`
		Supporting []string `json:"supporting"`
		Rationale  string   `json:"rationale"`
	} `json:"hypotheses"`
	Recommendations []struct {
		Statement string   `json:"statement"`
		Risk      string   `json:"risk"`
		Rationale string   `json:"rationale"`
		Refs      []string `json:"refs"`
	} `json:"recommendations"`
}

// Step performs the entire diagnosis in one agent.
func (Generalist) Step(ctx context.Context, s *State) (Outcome, error) {
	var names []string
	for _, t := range s.Tools.Registry().List() {
		names = append(names, t.Name())
	}
	text, err := runLoop(ctx, s, loopOptions{
		role:      RoleGeneralist,
		label:     "generalist",
		system:    systemPreamble + languageInstruction(s.Language),
		user:      promptContext(s) + "\n\n" + generalistInstruction,
		toolNames: names,
		maxTurns:  10,
	})
	if err != nil {
		return Outcome{}, err
	}
	var reply generalistReply
	if !decodeJSON(s, RoleGeneralist, text, &reply) {
		if strings.TrimSpace(text) != "" {
			s.SetSummary(strings.TrimSpace(text))
		}
		return Outcome{Done: true}, nil
	}
	if strings.TrimSpace(reply.Summary) != "" {
		s.SetSummary(reply.Summary)
	}
	for _, h := range reply.Hypotheses {
		if strings.TrimSpace(h.Statement) == "" {
			continue
		}
		s.AddHypothesis(core.Hypothesis{
			Statement: h.Statement, Confidence: clamp(h.Confidence),
			Supporting: h.Supporting, Rationale: h.Rationale,
			Status: core.HypothesisProposed,
		})
	}
	for _, r := range reply.Recommendations {
		if strings.TrimSpace(r.Statement) == "" {
			continue
		}
		s.AddRecommendation(core.NewRecommendation(r.Statement, parseRisk(r.Risk), r.Rationale, r.Refs...))
	}
	return Outcome{Done: true, Message: text}, nil
}

func notesSection(s *State) string {
	notes := s.Notes()
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Investigator reports\n")
	for _, n := range notes {
		b.WriteString(n)
		b.WriteString("\n\n")
	}
	return b.String()
}

func parseRisk(v string) core.Risk {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "high":
		return core.RiskHigh
	case "medium", "med":
		return core.RiskMedium
	default:
		return core.RiskLow
	}
}

func clamp(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// Objective is one unit of work an adaptive topology decides to do next. It
// names the domain that can answer it and what would be established — never the
// tool to run, which is the executor's business.
type Objective struct {
	Domain    tool.Domain `json:"domain"`
	Statement string      `json:"statement"`
}

// Strategist decides the next objectives from what is known so far, and says
// when nothing further is worth doing.
//
// It is a different role from Planner rather than a mode of it. Planner writes
// one prose plan for the reader and for the investigators' context; this
// contract is structured, iterative and — decisively — terminating. Overloading
// Planner with a second contract would have made both harder to reason about
// and impossible to attribute separately in a transcript.
type Strategist struct {
	Round   int      // 0-based; round 0 has learned nothing yet
	Learned []string // what the executed objectives returned

	objectives []Objective
	converged  bool
	reasoning  string
}

// Role identifies this agent.
func (*Strategist) Role() Role { return RoleStrategist }

type strategistReply struct {
	Objectives []struct {
		Domain    string `json:"domain"`
		Statement string `json:"statement"`
	} `json:"objectives"`
	Done      bool   `json:"done"`
	Reasoning string `json:"reasoning"`
}

// Step decides the next objectives. Outcome.Done reports convergence: the
// strategist judged that nothing further would change the conclusion.
func (s *Strategist) Step(ctx context.Context, st *State) (Outcome, error) {
	var b strings.Builder
	if len(s.Learned) > 0 {
		b.WriteString("\n\n## What the executed objectives established\n")
		for _, l := range s.Learned {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}
	text, err := runLoop(ctx, st, loopOptions{
		role:     RoleStrategist,
		label:    fmt.Sprintf("strategist (round %d)", s.Round+1),
		system:   systemPreamble + languageInstruction(st.Language),
		user:     promptContext(st) + b.String() + "\n\n" + strategistInstruction,
		maxTurns: 1,
	})
	if err != nil {
		return Outcome{}, err
	}

	var reply strategistReply
	if !decodeJSON(st, RoleStrategist, text, &reply) {
		// An unusable reply must stop the loop rather than repeat it: another
		// round would produce the same unusable reply and spend the budget.
		s.converged = true
		return Outcome{Done: true, Message: text}, nil
	}

	for _, o := range reply.Objectives {
		domain := tool.Domain(strings.ToLower(strings.TrimSpace(o.Domain)))
		if !knownDomain(domain) || strings.TrimSpace(o.Statement) == "" {
			continue
		}
		s.objectives = append(s.objectives, Objective{Domain: domain, Statement: strings.TrimSpace(o.Statement)})
	}
	s.converged = reply.Done || len(s.objectives) == 0

	if strings.TrimSpace(reply.Reasoning) != "" {
		s.reasoning = strings.TrimSpace(reply.Reasoning)
	}
	return Outcome{Done: s.converged, Message: text}, nil
}

// Objectives returns what Step decided to pursue.
func (s *Strategist) Objectives() []Objective { return s.objectives }

// Reasoning returns why, for the note the topology records.
func (s *Strategist) Reasoning() string { return s.reasoning }

func knownDomain(d tool.Domain) bool {
	switch d {
	case tool.DomainMetrics, tool.DomainLogs, tool.DomainCluster, tool.DomainHost, tool.DomainSource:
		return true
	default:
		return false
	}
}

// Executor pursues exactly one stated objective with the tools of its domain.
type Executor struct{ Objective Objective }

// Role identifies this agent.
func (Executor) Role() Role { return RoleExecutor }

// Label names this executor by the objective it was given.
func (e Executor) Label() string { return fmt.Sprintf("executor (%s)", e.Objective.Domain) }

// Step pursues the objective and records what it established.
func (e Executor) Step(ctx context.Context, s *State) (Outcome, error) {
	names := toolNames(s, e.Objective.Domain)
	if len(names) == 0 {
		s.AddGap(core.Gap{
			Intent: e.Label(), Reason: core.GapNotConfigured,
			Detail: fmt.Sprintf("objective %q needs %s tools, and this run has none",
				e.Objective.Statement, e.Objective.Domain),
			Impact: "the objective was not pursued, so what it would have established is unknown",
		})
		// An unanswerable objective is a result the strategist needs.
		return Outcome{Done: true, Message: fmt.Sprintf(
			"could not pursue %q: no %s tools in this run", e.Objective.Statement, e.Objective.Domain)}, nil
	}
	text, err := runLoop(ctx, s, loopOptions{
		role:      RoleExecutor,
		label:     e.Label(),
		system:    systemPreamble + languageInstruction(s.Language),
		user:      promptContext(s) + "\n\n" + fmt.Sprintf(executorInstruction, e.Objective.Domain, e.Objective.Statement),
		toolNames: names,
		maxTurns:  5,
	})
	if err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(text) != "" {
		s.AddNote(fmt.Sprintf("Objective (%s): %s\n%s", e.Objective.Domain, e.Objective.Statement, text))
	}
	return Outcome{Done: true, Message: text}, nil
}

// Advocate argues one position against the alternatives, from shared evidence.
//
// It does not choose its position. That is the whole design: a role that picks
// what to argue produces a second opinion, whereas a role assigned a position it
// must defend produces the strongest case *for* it — which is what a judge needs
// in order to compare, rather than merely agree.
type Advocate struct {
	Position     core.Hypothesis
	Alternatives []core.Hypothesis
	Rank         int // 1-based; fixes note order regardless of scheduling
}

// Role identifies this agent.
func (Advocate) Role() Role { return RoleAdvocate }

// Label names this advocate by the position it holds.
func (a Advocate) Label() string { return fmt.Sprintf("advocate (%s)", a.Position.ID) }

// Step argues the position and records the argument as a note.
func (a Advocate) Step(ctx context.Context, s *State) (Outcome, error) {
	var others strings.Builder
	for _, alt := range a.Alternatives {
		fmt.Fprintf(&others, "- %s: %s\n", alt.ID, alt.Statement)
	}
	if others.Len() == 0 {
		others.WriteString("- (none)\n")
	}
	text, err := runLoop(ctx, s, loopOptions{
		role:     RoleAdvocate,
		label:    a.Label(),
		system:   systemPreamble + languageInstruction(s.Language),
		user:     promptContext(s) + notesSection(s) + "\n\n" + fmt.Sprintf(advocateInstruction, a.Position.Statement, others.String()),
		maxTurns: 1,
	})
	if err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(text) != "" {
		s.AddNote(fmt.Sprintf("Argument %d for %s:\n%s", a.Rank, a.Position.ID, text))
	}
	return Outcome{Done: true, Message: text}, nil
}

// Judge adjudicates the advocates' arguments against the evidence.
//
// It differs from Critic in what it is given, not in what it returns: the critic
// challenges each hypothesis on its own terms; the judge is handed competing
// arguments about one body of evidence and must prefer at most one. The reply
// shape is deliberately the critic's, so the report needs no new concept.
type Judge struct{}

// Role identifies this agent.
func (Judge) Role() Role { return RoleJudge }

// Step decides between the argued positions.
func (Judge) Step(ctx context.Context, s *State) (Outcome, error) {
	hyps := s.Hypotheses()
	if len(hyps) == 0 {
		return Outcome{Done: true, Message: "no positions to judge"}, nil
	}
	var b strings.Builder
	b.WriteString("\n\n## Positions\n")
	for _, h := range hyps {
		fmt.Fprintf(&b, "- %s (confidence %.2f): %s\n", h.ID, h.Confidence, h.Statement)
	}
	text, err := runLoop(ctx, s, loopOptions{
		role:     RoleJudge,
		label:    "judge",
		system:   systemPreamble + languageInstruction(s.Language),
		user:     promptContext(s) + notesSection(s) + b.String() + "\n\n" + judgeInstruction,
		maxTurns: 1,
	})
	if err != nil {
		return Outcome{}, err
	}
	var reply criticReply
	if !decodeJSON(s, RoleJudge, text, &reply) {
		return Outcome{Done: true}, nil
	}
	supported := 0
	for _, a := range reply.Assessments {
		status := core.HypothesisStatus(strings.ToLower(strings.TrimSpace(a.Status)))
		switch status {
		case core.HypothesisSupported:
			// The judge's contract is that at most one position wins. A model
			// that supports two has not decided, and recording both as
			// supported would present indecision as agreement.
			supported++
			if supported > 1 {
				status = core.HypothesisInconclusive
			}
		case core.HypothesisRefuted, core.HypothesisInconclusive:
		default:
			status = ""
		}
		s.UpdateHypothesis(a.ID, status, clamp(a.Confidence), a.Rationale)
	}
	return Outcome{Done: true, Message: text}, nil
}
