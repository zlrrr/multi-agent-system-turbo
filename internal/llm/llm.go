// Package llm is the model-pluggability seam: agents reason through Provider,
// never against a vendor API (project goal G8).
//
// Governs: specs/001-mvp-core/design-lld.md §2.13, design-hld.md §4.2
package llm

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Role identifies who produced a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is a model's request to invoke a capability.
type ToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// Message is one turn in a conversation.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant turns
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool result turns
	ToolName   string     `json:"tool_name,omitempty"`
}

// ToolDefinition describes a capability to a model.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Schema      tool.Schema `json:"schema"`
}

// Usage accounts for one completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Request is one completion request.
type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolDefinition
	Temperature float64
	MaxTokens   int
	System      string

	// Agent is the diagnostic role that issued this request — planner,
	// investigator, advocate. It is not llm.Role, which identifies the author
	// of a message; this is who is doing the reasoning, and it is what makes
	// cost attributable to a role rather than only to a run.
	Agent string
}

// StopReason explains why generation ended.
type StopReason string

const (
	StopEnd      StopReason = "end"
	StopToolUse  StopReason = "tool_use"
	StopMaxToken StopReason = "max_tokens"
	StopRefusal  StopReason = "refusal"
)

// Response is one completion result.
type Response struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason StopReason
	Usage      Usage
	Model      string
}

// Provider is a model backend.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Close() error
}

// Factory builds a provider from configuration.
type Factory func(cfg config.LLMConfig) (Provider, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register adds a provider implementation. Called from provider package init.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	factories[name] = f
}

// Open builds the configured provider.
func Open(cfg config.LLMConfig) (Provider, error) {
	mu.RLock()
	f, ok := factories[cfg.Provider]
	mu.RUnlock()
	if !ok {
		return nil, errs.New("MAS-2005", cfg.Provider)
	}
	return f(cfg)
}

// Names lists the registered providers.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ModelFor resolves the model an agent role should use, honouring a per-role
// override so cheap extraction and expensive reasoning can use different models
// (project goal G8.2).
func ModelFor(cfg config.LLMConfig, role string) string {
	if m, ok := cfg.PerAgent[role]; ok && m.Model != "" {
		return m.Model
	}
	return cfg.Model
}

// TemperatureFor resolves the temperature for an agent role.
func TemperatureFor(cfg config.LLMConfig, role string) float64 {
	if m, ok := cfg.PerAgent[role]; ok && m.Temperature != 0 {
		return m.Temperature
	}
	return cfg.Temperature
}

// Definitions converts a tool registry's definitions into model-facing form.
func Definitions(defs []tool.Definition) []ToolDefinition {
	out := make([]ToolDefinition, 0, len(defs))
	for _, d := range defs {
		out = append(out, ToolDefinition{Name: d.Name, Description: d.Description, Schema: d.Schema})
	}
	return out
}

// Ledger is one run's accounting. It is separate from the provider wrapper
// because a run may reach several providers — routing exists precisely so it
// can — and a total that lived in one wrapper would count only that one.
//
// Governs: specs/005-model-routing-and-cost/design-lld.md §5
type Ledger struct {
	mu     sync.Mutex
	calls  int
	usage  Usage
	cost   core.Cost
	byRole map[string]*core.RoleUsage
}

// NewLedger builds an empty ledger. Cost starts known and zero: a run that
// makes no model call really did cost nothing.
func NewLedger() *Ledger {
	return &Ledger{byRole: map[string]*core.RoleUsage{}, cost: core.KnownCost(0)}
}

// record accounts for one exchange, attributing it to the role that made it.
// The attribution happens inside the mutex that guards the total, so the
// breakdown cannot drift from the sum it forms.
func (l *Ledger) record(agent, provider, model string, u Usage, elapsed time.Duration, cost core.Cost) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.usage.PromptTokens += u.PromptTokens
	l.usage.CompletionTokens += u.CompletionTokens
	l.cost = l.cost.Add(cost)

	role := agent
	if role == "" {
		role = "(unattributed)"
	}
	entry, ok := l.byRole[role]
	if !ok {
		entry = &core.RoleUsage{Role: role, Provider: provider, Model: model}
		l.byRole[role] = entry
	}
	entry.Calls++
	entry.PromptTokens += u.PromptTokens
	entry.CompletionTokens += u.CompletionTokens
	entry.WallMillis += elapsed.Milliseconds()
	entry.Cost = entry.Cost.Add(cost)
}

// Totals reports accumulated usage.
func (l *Ledger) Totals() (calls int, u Usage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls, l.usage
}

// Cost reports what the run spent, or that nobody knows.
func (l *Ledger) Cost() core.Cost {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cost
}

// ByRole reports the per-role breakdown, ordered by descending cost, then
// descending calls, then role name — a total order, so two runs of the same
// case produce the same table.
func (l *Ledger) ByRole() []core.RoleUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]core.RoleUsage, 0, len(l.byRole))
	for _, u := range l.byRole {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost.USD != out[j].Cost.USD {
			return out[i].Cost.USD > out[j].Cost.USD
		}
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Role < out[j].Role
	})
	return out
}

// Counting wraps a provider and records every exchange in a ledger, so
// accounting does not have to be repeated in every agent (FR-019).
type Counting struct {
	inner   Provider
	pricing Pricing
	ledger  *Ledger
}

// NewCounting wraps a provider with its own ledger.
func NewCounting(p Provider, pricing Pricing) *Counting {
	return NewCountingOn(p, pricing, NewLedger())
}

// NewCountingOn wraps a provider onto a shared ledger, which is how a run that
// routes roles to several providers still totals to one figure.
func NewCountingOn(p Provider, pricing Pricing, l *Ledger) *Counting {
	if l == nil {
		l = NewLedger()
	}
	return &Counting{inner: p, pricing: pricing, ledger: l}
}

// Name reports the wrapped provider's name.
func (c *Counting) Name() string { return c.inner.Name() }

// Ledger exposes the accounting this wrapper writes to.
func (c *Counting) Ledger() *Ledger { return c.ledger }

// Complete delegates, then records what it cost and who spent it.
func (c *Counting) Complete(ctx context.Context, req Request) (Response, error) {
	started := time.Now()
	resp, err := c.inner.Complete(ctx, req)
	elapsed := time.Since(started)

	// The served model is authoritative — a provider may answer with an alias,
	// or fall back — so that is what gets recorded and priced first. But an
	// operator who priced the name they asked for should not be told the run is
	// unpriced because the provider answered with a different string, so the
	// requested name is tried before giving up.
	served := resp.Model
	if served == "" {
		served = req.Model
	}
	cost := c.pricing.CostOf(served, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	if !cost.Known && req.Model != "" && req.Model != served {
		if alt := c.pricing.CostOf(req.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens); alt.Known {
			cost = alt
		}
	}
	c.ledger.record(req.Agent, c.inner.Name(), served, resp.Usage, elapsed, cost)
	return resp, err
}

// Close closes the wrapped provider.
func (c *Counting) Close() error { return c.inner.Close() }

// Totals reports accumulated usage.
func (c *Counting) Totals() (calls int, u Usage) { return c.ledger.Totals() }

// Cost reports what the run spent, or that nobody knows.
func (c *Counting) Cost() core.Cost { return c.ledger.Cost() }

// ByRole reports the per-role breakdown.
func (c *Counting) ByRole() []core.RoleUsage { return c.ledger.ByRole() }
