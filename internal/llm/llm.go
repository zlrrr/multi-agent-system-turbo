// Package llm is the model-pluggability seam: agents reason through Provider,
// never against a vendor API (project goal G8).
//
// Governs: specs/001-mvp-core/design-lld.md §2.13, design-hld.md §4.2
package llm

import (
	"context"
	"sort"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
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
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

// Request is one completion request.
type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolDefinition
	Temperature float64
	MaxTokens   int
	System      string
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

// Counting wraps a provider and accumulates usage across a run, so accounting
// does not have to be repeated in every agent (FR-019).
type Counting struct {
	inner Provider
	mu    sync.Mutex
	calls int
	usage Usage
}

// NewCounting wraps a provider with usage accounting.
func NewCounting(p Provider) *Counting { return &Counting{inner: p} }

// Name reports the wrapped provider's name.
func (c *Counting) Name() string { return c.inner.Name() }

// Complete delegates and records usage.
func (c *Counting) Complete(ctx context.Context, req Request) (Response, error) {
	resp, err := c.inner.Complete(ctx, req)
	c.mu.Lock()
	c.calls++
	c.usage.PromptTokens += resp.Usage.PromptTokens
	c.usage.CompletionTokens += resp.Usage.CompletionTokens
	c.usage.CostUSD += resp.Usage.CostUSD
	c.mu.Unlock()
	return resp, err
}

// Close closes the wrapped provider.
func (c *Counting) Close() error { return c.inner.Close() }

// Totals reports accumulated usage.
func (c *Counting) Totals() (calls int, u Usage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.usage
}
