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
