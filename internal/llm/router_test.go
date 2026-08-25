package llm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func routedConfig() config.LLMConfig {
	return config.LLMConfig{
		Provider: "mock", Model: "strong-model", Temperature: 0.2,
		MaxTokens: 4096,
		Providers: map[string]config.ProviderConfig{
			"cheap": {Model: "cheap-model"},
		},
		PerAgent: map[string]config.AgentModel{
			"investigator": {Provider: "cheap"},
			"executor":     {Provider: "cheap", Temperature: 0.9},
			"correlator":   {Temperature: 0.1},
			"critic":       {Model: "other-model"},
		},
	}
}

// TestPerRoleProviderIsUsed is FR-001. Routing a role to a different provider
// is what `per_agent` could not previously express, even though the provider
// interface was built to make it substitutable.
func TestPerRoleProviderIsUsed(t *testing.T) {
	r, err := llm.NewRouter(routedConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	for role, want := range map[string]struct{ name, model string }{
		"investigator": {"cheap", "cheap-model"},
		"executor":     {"cheap", "cheap-model"},
		"correlator":   {"default", "strong-model"},
		"critic":       {"default", "other-model"},
		"reporter":     {"default", "strong-model"}, // no override at all
	} {
		got := r.For(role)
		if got.Name != want.name || got.Model != want.model {
			t.Errorf("%s routed to %s/%s, want %s/%s", role, got.Name, got.Model, want.name, want.model)
		}
		if got.Provider == nil {
			t.Errorf("%s has no provider", role)
		}
	}
}

// TestRouteInheritsDefaults is the field that quietly breaks production runs. A
// role that overrides only the temperature must not lose the endpoint and the
// key; a named provider that sets only the model must not lose the timeout.
func TestRouteInheritsDefaults(t *testing.T) {
	cfg := routedConfig()
	r, err := llm.NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	// Overriding only the temperature keeps the default model.
	if got := r.For("correlator"); got.Model != "strong-model" || got.Temperature != 0.1 {
		t.Errorf("correlator = %s @ %v, want strong-model @ 0.1", got.Model, got.Temperature)
	}
	// Naming a provider and a temperature keeps that provider's model.
	if got := r.For("executor"); got.Model != "cheap-model" || got.Temperature != 0.9 {
		t.Errorf("executor = %s @ %v, want cheap-model @ 0.9", got.Model, got.Temperature)
	}
	// A named provider that sets only the model inherits the default's
	// temperature rather than falling to zero.
	if got := r.For("investigator"); got.Temperature != 0.2 {
		t.Errorf("investigator temperature = %v, want the default 0.2", got.Temperature)
	}
}

// TestProvidersAreOpenedOnceAndClosed is FR-002: two roles on one named
// provider must share a connection, and everything opened must be closed.
func TestProvidersAreOpenedOnceAndClosed(t *testing.T) {
	r, err := llm.NewRouter(routedConfig())
	if err != nil {
		t.Fatal(err)
	}
	// investigator and executor both name "cheap"; they must be the same
	// instance, or a run pays for two connections and two credential checks.
	if r.For("investigator").Provider != r.For("executor").Provider {
		t.Error("two roles on one named provider were given different instances")
	}
	if r.For("correlator").Provider != r.Default().Provider {
		t.Error("a role with no provider override did not get the default instance")
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close reported %v", err)
	}
}

// TestUnknownNamedProviderFailsAdmission is FR-003. A typo in a provider name
// must stop the run before any work, not silently fall back to the default —
// which would run the expensive model the operator was trying to avoid.
func TestUnknownNamedProviderFailsAdmission(t *testing.T) {
	cfg := routedConfig()
	cfg.PerAgent["investigator"] = config.AgentModel{Provider: "chaep"} // typo
	_, err := llm.NewRouter(cfg)
	if err == nil {
		t.Fatal("a role naming an undeclared provider was accepted")
	}
	if code := errs.CodeOf(err); code != "MAS-1003" {
		t.Errorf("got %s, want MAS-1003", code)
	}
	if !strings.Contains(err.Error(), "chaep") {
		t.Errorf("the error does not name the missing provider: %v", err)
	}
}

// TestUnopenableRoleProviderFailsAdmission: a per-role provider that cannot be
// opened is an admission failure, not a gap discovered mid-run after the other
// roles have already spent their tokens.
func TestUnopenableRoleProviderFailsAdmission(t *testing.T) {
	cfg := routedConfig()
	cfg.Providers["cheap"] = config.ProviderConfig{Provider: "no-such-provider"}
	_, err := llm.NewRouter(cfg)
	if err == nil {
		t.Fatal("a per-role provider that cannot be opened was accepted")
	}
	if code := errs.CodeOf(err); code != "MAS-2005" {
		t.Errorf("got %v (%s), want MAS-2005", err, code)
	}
}

// TestRoutesAndModelsDescribeTheRun backs `mas models` and doctor's pricing
// check: both need to know what will actually be used, not what was written.
func TestRoutesAndModelsDescribeTheRun(t *testing.T) {
	r, err := llm.NewRouter(routedConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	routes := r.Routes()
	if _, ok := routes["(default)"]; !ok {
		t.Error("Routes omits the default, so a role with no override is unexplained")
	}
	for _, role := range []string{"investigator", "executor", "correlator", "critic"} {
		if _, ok := routes[role]; !ok {
			t.Errorf("Routes omits %s", role)
		}
	}

	models := r.Models()
	want := map[string]bool{"strong-model": true, "cheap-model": true, "other-model": true}
	if len(models) != len(want) {
		t.Errorf("Models() = %v, want exactly %v", models, want)
	}
	for _, m := range models {
		if !want[m] {
			t.Errorf("Models() reports %q, which no role uses", m)
		}
	}
	for i := 1; i < len(models); i++ {
		if models[i-1] > models[i] {
			t.Errorf("Models() is not sorted: %v", models)
		}
	}
}

// TestRoutedRequestsCarryTheRole proves the attribution key reaches the
// provider: without it every exchange lands under "(unattributed)".
func TestRoutedRequestsCarryTheRole(t *testing.T) {
	r, err := llm.NewRouter(config.LLMConfig{Provider: "mock", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	route := r.For("planner")
	if _, err := route.Provider.Complete(context.Background(), llm.Request{
		Model: route.Model, Agent: "planner",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: planner"}},
	}); err != nil {
		t.Fatal(err)
	}
	byRole := r.Ledger().ByRole()
	if len(byRole) != 1 || byRole[0].Role != "planner" {
		t.Fatalf("attribution = %+v, want one entry for planner", byRole)
	}
}
