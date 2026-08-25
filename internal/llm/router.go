package llm

import (
	"sort"
	"strings"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Route is where one role's work goes: which provider, which model, and how
// deterministic it should be.
type Route struct {
	Name        string // the named provider, or "default"
	Provider    Provider
	Model       string
	Temperature float64
}

// Router resolves a role to its route and owns the providers it opened.
//
// Every distinct provider a run needs is opened once, at construction. That is
// what turns a bad credential into a refused run rather than a gap discovered
// by the correlator three minutes in, after the investigators have already
// spent their tokens.
//
// Governs: specs/005-model-routing-and-cost/design-lld.md §4
type Router struct {
	mu        sync.RWMutex
	cfg       config.LLMConfig
	ledger    *Ledger
	providers map[string]Provider // keyed by named-provider name; "" is the default
	byRole    map[string]Route
	fallback  Route
}

// NewRouter opens every provider the configuration routes to.
func NewRouter(cfg config.LLMConfig) (*Router, error) {
	r := &Router{
		cfg: cfg, ledger: NewLedger(),
		providers: map[string]Provider{}, byRole: map[string]Route{},
	}
	pricing := Pricing(cfg.Pricing)

	def, err := Open(cfg)
	if err != nil {
		return nil, err
	}
	// Every provider is wrapped onto one ledger, so a run that routes roles to
	// several of them still totals to a single figure and a single breakdown.
	r.providers[""] = NewCountingOn(def, pricing, r.ledger)
	r.fallback = Route{Name: "default", Provider: r.providers[""],
		Model: cfg.Model, Temperature: cfg.Temperature}

	// Only the named providers a role actually routes to are opened: declaring
	// one and never using it should not cost a connection or fail a run.
	for role, override := range cfg.PerAgent {
		name := strings.TrimSpace(override.Provider)
		if name != "" {
			if _, opened := r.providers[name]; !opened {
				named, ok := cfg.Providers[name]
				if !ok {
					_ = r.Close()
					return nil, errs.New("MAS-1003", "llm.per_agent."+role+".provider",
						"no provider named "+name+" is declared under llm.providers")
				}
				p, err := Open(inherit(cfg, named))
				if err != nil {
					_ = r.Close()
					return nil, err
				}
				r.providers[name] = NewCountingOn(p, pricing, r.ledger)
			}
		}
		r.byRole[role] = r.route(name, override)
	}
	return r, nil
}

// route builds one role's route, applying the role's own overrides on top of
// whichever provider it names.
func (r *Router) route(providerName string, override config.AgentModel) Route {
	base := r.fallback
	if providerName != "" {
		named := r.cfg.Providers[providerName]
		merged := inherit(r.cfg, named)
		base = Route{
			Name:        providerName,
			Provider:    r.providers[providerName],
			Model:       merged.Model,
			Temperature: merged.Temperature,
		}
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Temperature != 0 {
		base.Temperature = override.Temperature
	}
	return base
}

// inherit merges a named provider onto the run's default. A role that overrides
// one field must not lose the rest: restating the endpoint, the key and the
// timeout per role is how a production run fails on the one field somebody
// forgot (design-hld.md §3).
func inherit(def config.LLMConfig, named config.ProviderConfig) config.LLMConfig {
	out := def
	if named.Provider != "" {
		out.Provider = named.Provider
	}
	if named.Model != "" {
		out.Model = named.Model
	}
	if !named.APIKey.IsZero() {
		out.APIKey = named.APIKey
	}
	if named.BaseURL != "" {
		out.BaseURL = named.BaseURL
	}
	if named.Timeout.D() > 0 {
		out.Timeout = named.Timeout
	}
	if named.MaxTokens > 0 {
		out.MaxTokens = named.MaxTokens
	}
	if named.Temperature != 0 {
		out.Temperature = named.Temperature
	}
	if named.MockScript != "" {
		out.MockScript = named.MockScript
	}
	// A named provider is an alternative destination, not an alternative
	// routing table: nested per-agent rules would make the effective route
	// depend on where you started reading.
	out.PerAgent = nil
	out.Providers = nil
	return out
}

// For resolves a role's route, falling back to the default.
func (r *Router) For(role string) Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rt, ok := r.byRole[role]; ok {
		return rt
	}
	return r.fallback
}

// Default is the route a role with no override uses.
func (r *Router) Default() Route { return r.fallback }

// Ledger is this run's accounting, across every provider the router opened.
func (r *Router) Ledger() *Ledger { return r.ledger }

// Routes reports the effective routing for every role that overrides something,
// plus the default under the key "(default)". This is what `mas models` prints:
// the answer to "what will actually happen", which is a different question from
// "what did I write".
func (r *Router) Routes() map[string]Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Route, len(r.byRole)+1)
	for role, rt := range r.byRole {
		out[role] = rt
	}
	out["(default)"] = r.fallback
	return out
}

// Models lists every distinct model this run may use, sorted — for pricing
// checks in `mas doctor`.
func (r *Router) Models() []string {
	seen := map[string]bool{}
	for _, rt := range r.Routes() {
		if rt.Model != "" {
			seen[rt.Model] = true
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Close closes every provider the router opened, reporting the first failure.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, p := range r.providers {
		if err := p.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.providers = map[string]Provider{}
	return firstErr
}
