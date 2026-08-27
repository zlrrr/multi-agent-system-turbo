// Package agent holds the diagnostic roles and the shared state they operate on.
//
// Agents never talk to each other directly: a topology composes them, and they
// meet only through State. That is what makes topologies interchangeable and
// therefore comparable (project goal G7.3).
//
// Governs: specs/001-mvp-core/design-lld.md §2.14, design-hld.md §4.3
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Role names a diagnostic specialisation.
type Role string

const (
	RolePlanner      Role = "planner"
	RoleStrategist   Role = "strategist"
	RoleExecutor     Role = "executor"
	RoleAdvocate     Role = "advocate"
	RoleJudge        Role = "judge"
	RoleInvestigator Role = "investigator"
	RoleCorrelator   Role = "correlator"
	RoleCritic       Role = "critic"
	RoleReporter     Role = "reporter"
	RoleGeneralist   Role = "generalist"
)

// Budget caps what one run may consume. Exceeding a cap truncates and is
// reported; it is never an error (design-hld.md §8).
type Budget struct {
	MaxSteps     int
	MaxToolCalls int
	MaxTokens    int
	MaxWall      time.Duration
}

// Outcome is what one agent step reports back to its topology.
type Outcome struct {
	Done    bool
	Message string
}

// Agent is one diagnostic role.
type Agent interface {
	Role() Role
	Step(ctx context.Context, s *State) (Outcome, error)
}

// State is the shared working memory of a diagnostic run. Investigators may run
// concurrently, so every mutation goes through a guarded accessor.
type State struct {
	Run       *core.RunRecord
	Request   core.DiagnoseRequest
	Target    core.Target
	Pack      *knowledge.Pack
	Prior     []core.Finding
	Passed    []string
	Tools     *tool.Invoker
	Provider  llm.Provider
	LLMConfig config.LLMConfig
	Language  string
	Budget    Budget

	// MaxConcurrency caps how many investigators a topology may run at once.
	MaxConcurrency int

	// Sink persists steps as they happen. Model exchanges go through it for the
	// same reason tool calls do: a run record that omits half the reasoning is
	// not an audit trail (Constitution Art. V.3).
	Sink tool.StepSink

	// Router resolves which provider and model each role uses. Provider stays
	// as the run's default so a caller that needs only one still has it, and so
	// existing tests construct a State the same way.
	Router *llm.Router

	mu              sync.Mutex
	evidence        []core.Evidence
	gaps            []core.Gap
	hypotheses      []core.Hypothesis
	recommendations []core.Recommendation
	notes           []string
	summary         string
	usage           core.Usage
	truncated       bool
	truncReason     string
	steps           int
	startedAt       time.Time
	hypSeq          int
}

// NewState builds a state with the clock started.
func NewState() *State { return &State{startedAt: time.Now()} }

// Route resolves the provider, model and temperature for a role. Without a
// router — a test, or a single-provider run — every role uses the run's default
// provider with the per-agent model and temperature overrides applied, which is
// exactly what happened before routing existed.
func (s *State) Route(role string) llm.Route {
	if s.Router != nil {
		return s.Router.For(role)
	}
	return llm.Route{
		Name:        "default",
		Provider:    s.Provider,
		Model:       llm.ModelFor(s.LLMConfig, role),
		Temperature: llm.TemperatureFor(s.LLMConfig, role),
	}
}

// Start records the run's start time, from which the wall-clock budget runs.
func (s *State) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startedAt = time.Now()
}

// Elapsed reports how long the run has been going.
func (s *State) Elapsed() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startedAt.IsZero() {
		return 0
	}
	return time.Since(s.startedAt)
}

// AddEvidence records collected evidence, ignoring duplicates by digest so two
// investigators asking the same question do not inflate the report.
func (s *State) AddEvidence(ev core.Evidence) {
	if ev.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.evidence {
		if existing.Digest != "" && existing.Digest == ev.Digest {
			return
		}
	}
	s.evidence = append(s.evidence, ev)
}

// Evidence returns a copy of the collected evidence.
func (s *State) Evidence() []core.Evidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.Evidence(nil), s.evidence...)
}

// AddGap records evidence that could not be collected.
func (s *State) AddGap(g core.Gap) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.ID == "" {
		g.ID = fmt.Sprintf("gap-a%d", len(s.gaps)+1)
	}
	s.gaps = append(s.gaps, g)
}

// Gaps returns a copy of the recorded gaps.
func (s *State) Gaps() []core.Gap {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.Gap(nil), s.gaps...)
}

// AddHypothesis records a candidate explanation, assigning a stable id.
//
// Supporting and contradicting references are resolved against what this run
// actually collected. A model that cites evidence it was never given is
// guessing, and a report that repeats the citation launders the guess into
// provenance — so unresolved references are dropped and recorded as a gap
// rather than printed (MAS-2010).
func (s *State) AddHypothesis(h core.Hypothesis) string {
	var unresolved []string
	h.Supporting, unresolved = s.resolveRefs(h.Supporting, unresolved)
	h.Contradicting, unresolved = s.resolveRefs(h.Contradicting, unresolved)
	if len(unresolved) > 0 {
		s.AddGap(core.Gap{
			Intent: "hypothesis citations", Reason: core.GapUnavailable,
			Code:   "MAS-2010",
			Detail: errs.New("MAS-2010", len(unresolved), strings.Join(unresolved, ", ")).Error(),
			Impact: "the report shows this hypothesis without those references; " +
				"its support is weaker than the model claimed",
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.hypSeq++
	if h.ID == "" {
		h.ID = fmt.Sprintf("h-%d", s.hypSeq)
	}
	if h.Status == "" {
		h.Status = core.HypothesisProposed
	}
	s.hypotheses = append(s.hypotheses, h)
	return h.ID
}

// resolveRefs keeps the references that name evidence this run collected or a
// deterministic finding it was given, and returns the rest.
func (s *State) resolveRefs(refs, unresolved []string) (kept, stillUnresolved []string) {
	if len(refs) == 0 {
		return nil, unresolved
	}
	known := s.knownRefs()
	kept = make([]string, 0, len(refs))
	for _, r := range refs {
		id := strings.TrimSpace(r)
		if id == "" {
			continue
		}
		if known[id] {
			kept = append(kept, id)
			continue
		}
		unresolved = append(unresolved, id)
	}
	if len(kept) == 0 {
		kept = nil
	}
	return kept, unresolved
}

// knownRefs is every identifier a hypothesis may legitimately cite.
func (s *State) knownRefs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.evidence)+len(s.Prior))
	for _, e := range s.evidence {
		if e.ID != "" {
			out[e.ID] = true
		}
	}
	for _, f := range s.Prior {
		if f.ID != "" {
			out[f.ID] = true
		}
	}
	return out
}

// Hypotheses returns a copy of the current hypotheses.
func (s *State) Hypotheses() []core.Hypothesis {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.Hypothesis(nil), s.hypotheses...)
}

// UpdateHypothesis applies a critic's assessment to one hypothesis.
func (s *State) UpdateHypothesis(id string, status core.HypothesisStatus, confidence float64, rationale string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.hypotheses {
		if s.hypotheses[i].ID != id {
			continue
		}
		if status != "" {
			s.hypotheses[i].Status = status
		}
		if confidence >= 0 && confidence <= 1 {
			s.hypotheses[i].Confidence = confidence
		}
		if rationale != "" {
			s.hypotheses[i].Rationale = rationale
		}
		return true
	}
	return false
}

// AddRecommendation records advice for a human operator.
func (s *State) AddRecommendation(r core.Recommendation) {
	r.Advisory = true // CON-003: the system never acts, and cannot claim to
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recommendations = append(s.recommendations, r)
}

// Recommendations returns a copy of the advice gathered.
func (s *State) Recommendations() []core.Recommendation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]core.Recommendation(nil), s.recommendations...)
}

// AddNote records an agent's narrative output.
func (s *State) AddNote(note string) {
	if note == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = append(s.notes, note)
}

// SortNotes re-orders the narrative notes to match the given prefixes, so a
// concurrent topology still produces a byte-identical report (NFR-010). Notes
// whose prefix is not listed keep their relative order at the end.
func (s *State) SortNotes(prefixes []string) {
	rank := make(map[string]int, len(prefixes))
	for i, p := range prefixes {
		rank[p] = i
	}
	rankOf := func(note string) int {
		for prefix, r := range rank {
			if strings.HasPrefix(note, prefix) {
				return r
			}
		}
		return len(prefixes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sort.SliceStable(s.notes, func(i, j int) bool { return rankOf(s.notes[i]) < rankOf(s.notes[j]) })
}

// Notes returns a copy of the narrative notes.
func (s *State) Notes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.notes...)
}

// SetSummary records the reporter's summary.
func (s *State) SetSummary(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summary = v
}

// Summary returns the recorded summary.
func (s *State) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summary
}

// AddUsage accumulates model usage.
func (s *State) AddUsage(u core.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage.Add(u)
}

// Usage returns the accumulated usage.
func (s *State) Usage() core.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usage
	u.WallMillis = time.Since(s.startedAt).Milliseconds()
	return u
}

// Truncate records that a budget was exhausted, keeping the first reason.
func (s *State) Truncate(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.truncated = true
	if s.truncReason == "" {
		s.truncReason = reason
	}
}

// Truncated reports whether any budget was exhausted, and why.
func (s *State) Truncated() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.truncated, s.truncReason
}

// ConsumeStep reserves one reasoning step, reporting false when the step or
// wall-clock budget is spent.
func (s *State) ConsumeStep() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Budget.MaxWall > 0 && !s.startedAt.IsZero() && time.Since(s.startedAt) > s.Budget.MaxWall {
		s.truncated = true
		if s.truncReason == "" {
			s.truncReason = fmt.Sprintf("wall-clock budget of %s exhausted", s.Budget.MaxWall)
		}
		return false
	}
	if s.Budget.MaxSteps > 0 && s.steps >= s.Budget.MaxSteps {
		s.truncated = true
		if s.truncReason == "" {
			s.truncReason = fmt.Sprintf("step budget of %d exhausted", s.Budget.MaxSteps)
		}
		return false
	}
	if s.Budget.MaxTokens > 0 && s.usage.PromptTokens+s.usage.CompletionTokens >= s.Budget.MaxTokens {
		s.truncated = true
		if s.truncReason == "" {
			s.truncReason = fmt.Sprintf("token budget of %d exhausted", s.Budget.MaxTokens)
		}
		return false
	}
	s.steps++
	return true
}

// Steps reports how many reasoning steps have been taken.
func (s *State) Steps() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.steps
}

// EvidenceDigest renders the collected evidence for a prompt: identifiers and
// one-line summaries, never raw payloads, so a model sees what exists without
// the context cost of every sample.
func (s *State) EvidenceDigest() string {
	evs := s.Evidence()
	if len(evs) == 0 {
		return "(no evidence collected yet)"
	}
	var b []byte
	for _, e := range evs {
		b = append(b, fmt.Sprintf("- %s [%s from %s] %s\n", e.ID, e.Kind, e.Source, e.Summary)...)
	}
	return string(b)
}

// PriorFindingsDigest renders the deterministic phase's conclusions, which are
// the agents' starting context (design-hld.md §5.1).
func (s *State) PriorFindingsDigest() string {
	if len(s.Prior) == 0 {
		return "(the deterministic checks produced no findings)"
	}
	var b []byte
	for _, f := range s.Prior {
		b = append(b, fmt.Sprintf("- [%s, confidence %.2f, from %s] %s\n",
			f.Severity, f.Confidence, f.Origin, f.Statement)...)
		if f.Detail != "" {
			b = append(b, "    "+f.Detail+"\n"...)
		}
	}
	return string(b)
}

// GapsDigest renders what could not be collected, so a model states its
// uncertainty rather than inventing coverage it does not have.
func (s *State) GapsDigest() string {
	gaps := s.Gaps()
	if len(gaps) == 0 {
		return "(no gaps: every attempted collection succeeded)"
	}
	var b []byte
	for _, g := range gaps {
		b = append(b, fmt.Sprintf("- %s (%s%s): %s\n", g.Intent, g.Reason, codeSuffix(g.Code), g.Impact)...)
	}
	return string(b)
}

func codeSuffix(code string) string {
	if code == "" {
		return ""
	}
	return ", " + code
}
