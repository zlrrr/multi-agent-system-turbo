package orchestrator_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
)

// governed is every topology this project ships, and what it is expected to be.
// It is deliberately written before the topologies it governs: a contract
// derived from the implementations would encode whatever they happened to do,
// which measures nothing (plan.md D-1).
//
// A topology that registers without appearing here fails
// TestEveryRegisteredTopologyIsGoverned, so the contract cannot be escaped by
// omission.
var governed = []topologyExpectation{
	{name: "single", concurrent: false},
	{name: "supervisor", concurrent: true},
	{name: "plan-execute", concurrent: false, adaptive: true},
	{name: "debate", concurrent: true, adversarial: true},
	{name: "blackboard", concurrent: false, dataDriven: true},
}

type topologyExpectation struct {
	name string
	// concurrent topologies run some roles in parallel, which makes the
	// determinism check load-bearing rather than trivially true.
	concurrent bool
	// adaptive: the next action depends on what the last one returned.
	adaptive bool
	// adversarial: competing positions are argued and adjudicated.
	adversarial bool
	// dataDriven: control comes from the state, not from a fixed script.
	dataDriven bool
}

// stepRecorder captures the run's step record so the contract can check that
// every model exchange names the role that made it (FR-006).
type stepRecorder struct {
	mu    sync.Mutex
	steps []core.Step
}

func (r *stepRecorder) AppendStep(_ context.Context, s core.Step) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, s)
}

func (r *stepRecorder) all() []core.Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.Step(nil), r.steps...)
}

func TestEveryRegisteredTopologyIsGoverned(t *testing.T) {
	covered := map[string]bool{}
	for _, g := range governed {
		covered[g.name] = true
	}
	for _, n := range orchestrator.Names() {
		if !covered[n] {
			t.Errorf("topology %q is registered but not governed; add it to `governed`", n)
		}
	}
}

// runTopology is the contract's harness: identical state, identical tools,
// identical scripted model. Only the topology varies — which is what makes a
// comparison between them an experiment rather than an anecdote.
func runTopology(t *testing.T, name string, prepare func(*agent.State)) (*agent.State, *stepRecorder) {
	t.Helper()
	o, err := orchestrator.Open(name)
	if err != nil {
		t.Fatalf("topology %q is governed but not registered: %v", name, err)
	}
	st := newState(t)
	rec := &stepRecorder{}
	st.Sink = rec
	if prepare != nil {
		prepare(st)
	}
	if err := o.Run(context.Background(), st); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return st, rec
}

// registered returns the governed topologies that are actually registered, so
// the contract runs and passes while the remaining topologies are still being
// written (plan.md §4 sequencing).
func registeredGoverned() []topologyExpectation {
	live := map[string]bool{}
	for _, n := range orchestrator.Names() {
		live[n] = true
	}
	var out []topologyExpectation
	for _, g := range governed {
		if live[g.name] {
			out = append(out, g)
		}
	}
	return out
}

// TestTopologyProducesHypothesisAndSummary is HLD §2 property 2: whatever the
// control flow, evidence that supports a conclusion must reach the report.
func TestTopologyProducesHypothesisAndSummary(t *testing.T) {
	for _, g := range registeredGoverned() {
		t.Run(g.name, func(t *testing.T) {
			st, _ := runTopology(t, g.name, nil)
			if len(st.Hypotheses()) == 0 {
				t.Error("no hypothesis: evidence that supports one produced none")
			}
			if strings.TrimSpace(st.Summary()) == "" {
				t.Error("no summary: the report would open with nothing")
			}
			for _, h := range st.Hypotheses() {
				if strings.TrimSpace(h.Statement) == "" {
					t.Error("a hypothesis has no statement")
				}
			}
		})
	}
}

// TestTopologyAttributesEveryExchange is property 3. Without attribution a
// transcript cannot be read and cost cannot be assigned to a role.
func TestTopologyAttributesEveryExchange(t *testing.T) {
	for _, g := range registeredGoverned() {
		t.Run(g.name, func(t *testing.T) {
			_, rec := runTopology(t, g.name, nil)
			steps := rec.all()
			if len(steps) == 0 {
				t.Fatal("the run recorded no steps at all")
			}
			seenLLM := false
			for _, s := range steps {
				if s.Kind != core.StepLLMCall {
					continue
				}
				seenLLM = true
				if strings.TrimSpace(s.Actor) == "" {
					t.Errorf("a model exchange names no role: %+v", s)
				}
			}
			if !seenLLM {
				t.Error("no model exchange was recorded, so attribution is untested")
			}
		})
	}
}

// TestTopologiesRespectStepBudget is property 4. A topology that ignores the
// budget cannot be compared on cost with one that honours it.
//
// The budget is calibrated per topology rather than fixed: a topology that
// finishes inside a fixed budget has not been tested against it, and one that
// needs far more would be tested only on its first step. Each topology is run
// once unbudgeted to learn what it costs, then again with one step less than
// that — where it must stop short *and say so*.
func TestTopologiesRespectStepBudget(t *testing.T) {
	for _, g := range registeredGoverned() {
		t.Run(g.name, func(t *testing.T) {
			full, _ := runTopology(t, g.name, nil)
			need := full.Steps()
			if need < 2 {
				t.Skipf("%s takes %d step(s) unbudgeted; there is no budget below it to test",
					g.name, need)
			}
			if truncated, _ := full.Truncated(); truncated {
				t.Fatalf("the unbudgeted run truncated, so the calibration is meaningless")
			}

			budget := need - 1
			tight, _ := runTopology(t, g.name, func(s *agent.State) {
				s.Budget.MaxSteps = budget
			})
			if tight.Steps() > budget {
				t.Errorf("took %d steps against a budget of %d", tight.Steps(), budget)
			}
			truncated, reason := tight.Truncated()
			if !truncated {
				t.Errorf("the run needed %d steps but was given %d, and reported no truncation: "+
					"work was dropped silently", need, budget)
			} else if strings.TrimSpace(reason) == "" {
				t.Error("truncation was recorded without saying why")
			}
		})
	}
}

// TestTopologiesDegradeWithoutTools is property 5: comparability has to survive
// a run with no evidence sources, which is what an offline diagnosis is.
func TestTopologiesDegradeWithoutTools(t *testing.T) {
	for _, g := range registeredGoverned() {
		t.Run(g.name, func(t *testing.T) {
			st, _ := runTopology(t, g.name, func(s *agent.State) {
				s.Tools = emptyInvoker(t)
			})
			// The run must complete. What it must not do is claim to have
			// checked something it had no way to check.
			for _, h := range st.Hypotheses() {
				if h.Status == core.HypothesisSupported && len(h.Supporting) > 0 {
					for _, ref := range h.Supporting {
						if strings.HasPrefix(ref, "ev-") {
							t.Errorf("a hypothesis cites evidence %q in a run with no tools", ref)
						}
					}
				}
			}
		})
	}
}

// TestTopologiesAreDeterministic is property 6, and it is why concurrency inside
// a topology has to be paid for with a deterministic merge (CON-003).
func TestTopologiesAreDeterministic(t *testing.T) {
	for _, g := range registeredGoverned() {
		t.Run(g.name, func(t *testing.T) {
			first, _ := runTopology(t, g.name, nil)
			for i := 0; i < 3; i++ {
				again, _ := runTopology(t, g.name, nil)
				if a, b := statements(first), statements(again); a != b {
					t.Fatalf("run %d produced different hypotheses:\n  %s\n  %s", i+1, a, b)
				}
				if a, b := strings.Join(first.Notes(), "|"), strings.Join(again.Notes(), "|"); a != b {
					t.Fatalf("run %d produced notes in a different order:\n  %s\n  %s", i+1, a, b)
				}
			}
		})
	}
}

func statements(s *agent.State) string {
	out := make([]string, 0, len(s.Hypotheses()))
	for _, h := range s.Hypotheses() {
		out = append(out, h.Statement)
	}
	return strings.Join(out, "|")
}

// TestTopologyDescriptionsAreBilingualAndHonest is property 7. `Avoid` is
// required: five architectures all described as good choices is a list, not a
// choice (design-lld.md §2).
func TestTopologyDescriptionsAreBilingualAndHonest(t *testing.T) {
	details := orchestrator.Details()
	for _, g := range registeredGoverned() {
		t.Run(g.name, func(t *testing.T) {
			d, ok := details[g.name]
			if !ok {
				t.Fatal("registered with no description")
			}
			if !d.Complete() {
				t.Error("every field must carry both languages: Summary, Cost, Choose and Avoid")
			}
			if d.Avoid.Empty() {
				t.Error("the topology does not say when not to choose it")
			}
			for _, lang := range []string{"en", "zh"} {
				if strings.TrimSpace(d.In(lang)) == "" {
					t.Errorf("renders empty in %s", lang)
				}
			}
		})
	}
}

// emptyInvoker is a run with no evidence sources at all — the offline case.
func emptyInvoker(t *testing.T) *tool.Invoker {
	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := tool.NewInvoker(tool.NewRegistry(), tool.InvokerOptions{
		Guard: g, Mode: core.ModeOffline, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

// brokenTopology is defective on purpose: it produces a hypothesis but never a
// summary, and it makes no model call at all. Nothing registers it.
type brokenTopology struct{}

func (brokenTopology) Name() string           { return "broken" }
func (brokenTopology) Describe(string) string { return "" }
func (brokenTopology) Run(_ context.Context, s *agent.State) error {
	s.AddHypothesis(core.Hypothesis{Statement: "something is wrong"})
	return nil
}

// TestBrokenTopologyFailsTheContract is what makes every test above credible. A
// contract that has quietly stopped checking anything still passes for correct
// topologies; the only way to notice is to run it against one that is wrong and
// require it to fail.
func TestBrokenTopologyFailsTheContract(t *testing.T) {
	st := newState(t)
	rec := &stepRecorder{}
	st.Sink = rec
	if err := (brokenTopology{}).Run(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	// Property 2: a summary is required.
	if strings.TrimSpace(st.Summary()) != "" {
		t.Fatal("the broken topology was expected to produce no summary")
	}
	// Property 3: at least one attributed model exchange is required.
	for _, s := range rec.all() {
		if s.Kind == core.StepLLMCall {
			t.Fatal("the broken topology was expected to make no model call")
		}
	}
	// Property 7: an empty description must not satisfy the description check.
	var empty orchestrator.Description
	if empty.Complete() {
		t.Fatal("an empty description was accepted as complete")
	}
	if !empty.Avoid.Empty() {
		t.Fatal("an empty description reported a non-empty Avoid")
	}
}

// TestAttributionUnderConcurrentTopology is feature 005's NFR-006, checked
// where it matters: `supervisor` and `debate` run roles in parallel, and an
// accounting bug there would lose or misattribute a call in exactly the runs
// whose cost an operator most wants to compare.
func TestAttributionUnderConcurrentTopology(t *testing.T) {
	for _, g := range registeredGoverned() {
		if !g.concurrent {
			continue
		}
		t.Run(g.name, func(t *testing.T) {
			o, err := orchestrator.Open(g.name)
			if err != nil {
				t.Fatal(err)
			}
			st := newState(t)
			rec := &stepRecorder{}
			st.Sink = rec

			router, err := llm.NewRouter(config.LLMConfig{
				Provider: "mock", Model: "mock-1",
				Pricing: map[string]config.ModelPrice{
					"mock-1": {InputPerMTok: 3, OutputPerMTok: 15},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = router.Close() }()
			st.Router = router
			st.Provider = router.Default().Provider

			if err := o.Run(context.Background(), st); err != nil {
				t.Fatal(err)
			}

			calls, _ := router.Ledger().Totals()
			byRole := router.Ledger().ByRole()
			if calls == 0 || len(byRole) == 0 {
				t.Fatal("a run that used the model recorded no accounting")
			}

			var sum int
			var usd float64
			for _, u := range byRole {
				sum += u.Calls
				usd += u.Cost.USD
				if u.Role == "(unattributed)" {
					t.Errorf("a call made under a concurrent topology lost its role")
				}
			}
			if sum != calls {
				t.Errorf("per-role calls sum to %d, the total says %d", sum, calls)
			}
			if total := router.Ledger().Cost(); total.USD < usd-1e-9 || total.USD > usd+1e-9 {
				t.Errorf("per-role cost sums to %v, the total says %v", usd, total.USD)
			}
			if !router.Ledger().Cost().Known {
				t.Error("every model was priced, so the total must be known")
			}
		})
	}
}
