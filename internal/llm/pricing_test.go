package llm_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
)

func pricing() llm.Pricing {
	return llm.Pricing{
		"strong-model": {InputPerMTok: 5, OutputPerMTok: 25},
		"free-model":   {InputPerMTok: 0, OutputPerMTok: 0},
		// The mock answers as "mock-1" whatever it was asked for, which is the
		// provider being honest about what actually served the request.
		"mock-1": {InputPerMTok: 5, OutputPerMTok: 25},
	}
}

// TestZeroPriceIsKnownAbsentPriceIsNot is the distinction core.Cost exists for.
// A self-hosted model really is free at the margin, and an operator who wrote 0
// meant it. A model nobody priced is a different thing entirely, and reporting
// it as free would be a claim the run never earned.
func TestZeroPriceIsKnownAbsentPriceIsNot(t *testing.T) {
	p := pricing()

	free := p.CostOf("free-model", 1_000_000, 1_000_000)
	if !free.Known || free.USD != 0 {
		t.Errorf("a configured zero price = %+v, want a known 0", free)
	}

	unknown := p.CostOf("mystery-model", 1_000_000, 1_000_000)
	if unknown.Known {
		t.Error("a model with no configured price was reported as priced")
	}
	if unknown.USD != 0 || len(unknown.Unpriced) != 1 || unknown.Unpriced[0] != "mystery-model" {
		t.Errorf("unknown = %+v, want zero USD and the model named", unknown)
	}
	if !strings.Contains(unknown.String(), "not priced") {
		t.Errorf("rendered as %q, which does not say the cost is unknown", unknown.String())
	}
}

// TestCostIsComputedFromConfiguredPrices is FR-004.
func TestCostIsComputedFromConfiguredPrices(t *testing.T) {
	// 200k prompt at $5/Mtok = $1.00; 40k completion at $25/Mtok = $1.00.
	got := pricing().CostOf("strong-model", 200_000, 40_000)
	if !got.Known {
		t.Fatalf("cost = %+v, want known", got)
	}
	if got.USD < 1.99 || got.USD > 2.01 {
		t.Errorf("USD = %v, want 2.00", got.USD)
	}
}

// TestUsageIsAttributedPerRole is FR-007, and TestAttributionSumsToTheTotal is
// the property that keeps the breakdown honest: a table that does not add up to
// the figure above it is worse than no table.
func TestUsageIsAttributedPerRole(t *testing.T) {
	p, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	c := llm.NewCounting(p, pricing())

	for _, role := range []string{"planner", "investigator", "investigator", "reporter"} {
		if _, err := c.Complete(context.Background(), llm.Request{
			Model: "strong-model", Agent: role,
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: " + role}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	byRole := c.ByRole()
	if len(byRole) != 3 {
		t.Fatalf("roles = %+v, want three", byRole)
	}
	counts := map[string]int{}
	for _, u := range byRole {
		counts[u.Role] = u.Calls
		// The recorded model is the one that served the request, not the one
		// that was asked for: that is what a bill would say.
		if u.Model != "mock-1" {
			t.Errorf("%s recorded model %q, want the served model", u.Role, u.Model)
		}
		if u.PromptTokens == 0 {
			t.Errorf("%s recorded no tokens", u.Role)
		}
	}
	if counts["investigator"] != 2 {
		t.Errorf("investigator calls = %d, want 2", counts["investigator"])
	}

	// The breakdown must sum to the total, or the table contradicts the figure
	// above it.
	var sumCalls, sumPrompt int
	var sumUSD float64
	for _, u := range byRole {
		sumCalls += u.Calls
		sumPrompt += u.PromptTokens
		sumUSD += u.Cost.USD
	}
	calls, total := c.Totals()
	if sumCalls != calls {
		t.Errorf("per-role calls sum to %d, total says %d", sumCalls, calls)
	}
	if sumPrompt != total.PromptTokens {
		t.Errorf("per-role prompt tokens sum to %d, total says %d", sumPrompt, total.PromptTokens)
	}
	if got := c.Cost(); got.USD < sumUSD-1e-9 || got.USD > sumUSD+1e-9 {
		t.Errorf("per-role cost sums to %v, total says %v", sumUSD, got.USD)
	}
}

// TestUnpricedModelMakesCostUnknown is FR-005 at the ledger level.
func TestUnpricedModelMakesCostUnknown(t *testing.T) {
	p, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	c := llm.NewCounting(p, pricing())

	// One priced call and one unpriced call: the run is partly priced, which is
	// neither "known" nor "nothing to report". The unpriced provider answers
	// under its own name, which is the one that must appear in the report.
	unpriced, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	c2 := llm.NewCountingOn(unpriced, llm.Pricing{}, c.Ledger())
	if _, err := c.Complete(context.Background(), llm.Request{
		Model: "strong-model", Agent: "planner",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: planner"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Complete(context.Background(), llm.Request{
		Model: "mystery-model", Agent: "critic",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: critic"}},
	}); err != nil {
		t.Fatal(err)
	}

	cost := c.Cost()
	if cost.Known {
		t.Error("a run containing an unpriced model reported a known cost")
	}
	if !cost.Partial() {
		t.Errorf("cost = %+v, want the priced part preserved", cost)
	}
	if len(cost.Unpriced) != 1 || cost.Unpriced[0] != "mock-1" {
		t.Errorf("unpriced = %v, want the served model named", cost.Unpriced)
	}
}

// TestAttributionUnderConcurrency is NFR-006: the topologies that run roles in
// parallel must not lose or misattribute a call.
func TestAttributionUnderConcurrency(t *testing.T) {
	p, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	c := llm.NewCounting(p, pricing())

	const perRole = 20
	roles := []string{"investigator", "advocate", "executor"}
	var wg sync.WaitGroup
	for _, role := range roles {
		for i := 0; i < perRole; i++ {
			wg.Add(1)
			go func(role string) {
				defer wg.Done()
				_, _ = c.Complete(context.Background(), llm.Request{
					Model: "strong-model", Agent: role,
					Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: " + role}},
				})
			}(role)
		}
	}
	wg.Wait()

	byRole := c.ByRole()
	if len(byRole) != len(roles) {
		t.Fatalf("roles = %d, want %d", len(byRole), len(roles))
	}
	for _, u := range byRole {
		if u.Calls != perRole {
			t.Errorf("%s recorded %d calls, want %d", u.Role, u.Calls, perRole)
		}
	}
	if calls, _ := c.Totals(); calls != perRole*len(roles) {
		t.Errorf("total calls = %d, want %d", calls, perRole*len(roles))
	}
}

// TestByRoleIsDeterministic keeps the report stable: two runs of the same case
// must render the same table.
func TestByRoleIsDeterministic(t *testing.T) {
	p, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	c := llm.NewCounting(p, pricing())
	for _, role := range []string{"reporter", "planner", "critic", "planner"} {
		_, _ = c.Complete(context.Background(), llm.Request{
			Model: "strong-model", Agent: role,
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: " + role}},
		})
	}
	first := renderRoles(c.ByRole())
	for i := 0; i < 5; i++ {
		if got := renderRoles(c.ByRole()); got != first {
			t.Fatalf("ordering changed between reads:\n %s\n %s", first, got)
		}
	}
}

func renderRoles(us []core.RoleUsage) string {
	parts := make([]string, 0, len(us))
	for _, u := range us {
		parts = append(parts, u.Role)
	}
	return strings.Join(parts, ",")
}

// TestAttributionSumsToTheTotal is the property that keeps a breakdown honest.
// A per-role table that does not add up to the figure above it is worse than no
// table: the reader cannot tell which number to believe, and both look
// authoritative.
func TestAttributionSumsToTheTotal(t *testing.T) {
	p, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	c := llm.NewCounting(p, pricing())

	for _, role := range []string{"planner", "investigator", "investigator", "critic", "reporter"} {
		if _, err := c.Complete(context.Background(), llm.Request{
			Model: "strong-model", Agent: role,
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: " + role}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	var calls, prompt, completion int
	var usd float64
	for _, u := range c.ByRole() {
		calls += u.Calls
		prompt += u.PromptTokens
		completion += u.CompletionTokens
		usd += u.Cost.USD
	}
	totalCalls, total := c.Totals()
	if calls != totalCalls {
		t.Errorf("per-role calls sum to %d, the total says %d", calls, totalCalls)
	}
	if prompt != total.PromptTokens {
		t.Errorf("per-role prompt tokens sum to %d, the total says %d", prompt, total.PromptTokens)
	}
	if completion != total.CompletionTokens {
		t.Errorf("per-role completion tokens sum to %d, the total says %d", completion, total.CompletionTokens)
	}
	if got := c.Cost(); got.USD < usd-1e-9 || got.USD > usd+1e-9 {
		t.Errorf("per-role cost sums to %v, the total says %v", usd, got.USD)
	}
}

// TestPartiallyPricedRunIsExplicit is FR-009. Suppressing the figure would
// discard real information; showing it bare would understate the run. The
// report needs both halves, so the ledger has to keep both.
func TestPartiallyPricedRunIsExplicit(t *testing.T) {
	ledger := llm.NewLedger()
	priced, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	unpriced, _ := llm.Open(config.LLMConfig{Provider: "mock"})

	pricedSide := llm.NewCountingOn(priced, pricing(), ledger)
	unpricedSide := llm.NewCountingOn(unpriced, llm.Pricing{}, ledger)

	if _, err := pricedSide.Complete(context.Background(), llm.Request{
		Model: "strong-model", Agent: "correlator",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: correlator"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := unpricedSide.Complete(context.Background(), llm.Request{
		Model: "strong-model", Agent: "investigator",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: investigator"}},
	}); err != nil {
		t.Fatal(err)
	}

	cost := ledger.Cost()
	if cost.Known {
		t.Error("a partly priced run claimed to know its total")
	}
	if !cost.Partial() {
		t.Errorf("cost = %+v; the priced part must survive so the report can state it", cost)
	}
	if cost.USD <= 0 {
		t.Errorf("USD = %v; the priced call cost something", cost.USD)
	}
	if len(cost.Unpriced) == 0 {
		t.Error("the run cannot name what it failed to price")
	}
	if !strings.Contains(cost.String(), "not priced") {
		t.Errorf("rendered as %q, which does not admit the missing part", cost.String())
	}

	// The role that was priced must still show a known cost of its own: the
	// unknown belongs to the run, not to every line of the breakdown.
	for _, u := range ledger.ByRole() {
		if u.Role == "correlator" && !u.Cost.Known {
			t.Error("the priced role's own cost was marked unknown")
		}
		if u.Role == "investigator" && u.Cost.Known {
			t.Error("the unpriced role's cost was marked known")
		}
	}
}
