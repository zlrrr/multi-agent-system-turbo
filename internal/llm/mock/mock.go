// Package mock is a scripted, deterministic Provider. It is what makes
// Constitution Art. VI.3 achievable: no test and no demo needs a network or a
// real model, and identical input always produces an identical report (NFR-010).
//
// Governs: specs/001-mvp-core/design-lld.md §2.13
package mock

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
	"gopkg.in/yaml.v3"
)

func init() {
	llm.Register("mock", func(cfg config.LLMConfig) (llm.Provider, error) {
		if cfg.MockScript == "" {
			return New(DefaultScript()), nil
		}
		s, err := LoadScript(cfg.MockScript)
		if err != nil {
			return nil, err
		}
		return New(s), nil
	})
}

// Reply is one scripted response.
type Reply struct {
	// When matches against the concatenated conversation; an empty When matches
	// anything, which makes it the fallback.
	When      string         `yaml:"when" json:"when"`
	Role      string         `yaml:"role" json:"role"`
	Text      string         `yaml:"text" json:"text"`
	ToolCalls []ScriptedCall `yaml:"toolCalls" json:"tool_calls"`
	Stop      string         `yaml:"stop" json:"stop"`
}

// ScriptedCall is a tool call the mock will emit.
type ScriptedCall struct {
	Name string         `yaml:"name" json:"name"`
	Args map[string]any `yaml:"args" json:"args"`
}

// Script drives the mock provider.
type Script struct {
	Replies []Reply `yaml:"replies" json:"replies"`
}

// LoadScript reads a script from a YAML or JSON file.
func LoadScript(path string) (*Script, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return nil, errs.Wrap(err, "MAS-1001", path)
	}
	var s Script
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, errs.Wrap(err, "MAS-1001", path)
	}
	if len(s.Replies) == 0 {
		return nil, errs.New("MAS-1003", "llm.mock_script", "the script has no replies")
	}
	return &s, nil
}

// Provider is a deterministic scripted model.
type Provider struct {
	mu     sync.Mutex
	script *Script
	turns  int
	calls  []llm.Request
}

// New builds a mock provider from a script.
func New(s *Script) *Provider {
	if s == nil {
		s = DefaultScript()
	}
	return &Provider{script: s}
}

// Name identifies the provider.
func (p *Provider) Name() string { return "mock" }

// Close is a no-op.
func (p *Provider) Close() error { return nil }

// Calls returns the requests received, for test assertions.
func (p *Provider) Calls() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.Request(nil), p.calls...)
}

// Complete returns the first reply whose When appears in the conversation.
// Matching on content rather than on turn index keeps a script valid when an
// agent's internal step count changes.
func (p *Provider) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turns++
	p.calls = append(p.calls, req)

	haystack := strings.ToLower(conversationText(req))
	toolNames := map[string]bool{}
	for _, t := range req.Tools {
		toolNames[t.Name] = true
	}

	for _, r := range p.script.Replies {
		if r.When != "" && !strings.Contains(haystack, strings.ToLower(r.When)) {
			continue
		}
		// A reply that calls a tool the agent was not given would stall the loop,
		// so it is skipped in favour of a later, applicable reply.
		if len(r.ToolCalls) > 0 && len(req.Tools) > 0 {
			usable := true
			for _, c := range r.ToolCalls {
				if !toolNames[c.Name] {
					usable = false
					break
				}
			}
			if !usable {
				continue
			}
		}
		if len(r.ToolCalls) > 0 && alreadyCalled(req, r.ToolCalls) {
			continue // do not loop on a tool whose result is already in context
		}
		return p.build(r), nil
	}
	return llm.Response{
		Text:       "No further analysis is available from the scripted provider.",
		StopReason: llm.StopEnd, Model: "mock-1",
	}, nil
}

func (p *Provider) build(r Reply) llm.Response {
	resp := llm.Response{
		Text: r.Text, Model: "mock-1",
		Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 50},
	}
	for i, c := range r.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
			ID: "mock-call-" + itoa(p.turns) + "-" + itoa(i), Name: c.Name, Args: c.Args,
		})
	}
	switch {
	case r.Stop != "":
		resp.StopReason = llm.StopReason(r.Stop)
	case len(resp.ToolCalls) > 0:
		resp.StopReason = llm.StopToolUse
	default:
		resp.StopReason = llm.StopEnd
	}
	return resp
}

// alreadyCalled reports whether every scripted call already has a result in the
// conversation, which is how the mock avoids repeating itself.
func alreadyCalled(req llm.Request, calls []ScriptedCall) bool {
	seen := map[string]bool{}
	for _, m := range req.Messages {
		if m.Role == llm.RoleTool && m.ToolName != "" {
			seen[m.ToolName] = true
		}
		for _, tc := range m.ToolCalls {
			seen[tc.Name] = true
		}
	}
	for _, c := range calls {
		if !seen[c.Name] {
			return false
		}
	}
	return true
}

func conversationText(req llm.Request) string {
	var b strings.Builder
	b.WriteString(req.System)
	b.WriteByte('\n')
	for _, m := range req.Messages {
		b.WriteString(m.Content)
		b.WriteByte('\n')
		for _, tc := range m.ToolCalls {
			b.WriteString(tc.Name)
			if raw, err := json.Marshal(tc.Args); err == nil {
				b.Write(raw)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// DefaultScript is a self-contained transcript that exercises every agent role
// against the shipped Redis pack. It is what `mas diagnose` uses out of the box,
// so a new user gets a complete, believable report with no credentials at all.
func DefaultScript() *Script {
	return &Script{Replies: []Reply{
		{
			When: "role: planner",
			Text: `Deterministic checks already established the memory position, so the plan targets what they could not settle.

1. metrics — confirm whether the eviction rate rose before or after latency, which decides cause from effect.
2. logs — look for OOM refusals and restart markers inside the window.
3. cluster — check pod restarts and OOMKilled events on the instance.
4. source — locate the exact message Redis emits when it refuses a write.`,
		},
		{
			When:      "role: investigator (metrics)",
			ToolCalls: []ScriptedCall{{Name: "promql.range", Args: map[string]any{"query": "rate(redis_evicted_keys_total[5m])"}}},
		},
		{
			When: "role: investigator (metrics)",
			Text: "Eviction begins roughly four minutes before the latency rise, so eviction leads and latency follows rather than the reverse.",
		},
		{
			When:      "role: investigator (logs)",
			ToolCalls: []ScriptedCall{{Name: "loki.query", Args: map[string]any{"query": `{job="redis"} |= "OOM"`, "limit": 100}}},
		},
		{
			When: "role: investigator (logs)",
			Text: "The log carries repeated 'OOM command not allowed' refusals and no restart marker, so the process stayed up while refusing writes.",
		},
		{
			When:      "role: investigator (cluster)",
			ToolCalls: []ScriptedCall{{Name: "kube.events", Args: map[string]any{"type": "Warning"}}},
		},
		{
			When: "role: investigator (cluster)",
			Text: "No OOMKilled event is present, so the container was not terminated: the pressure is inside Redis's own maxmemory, not the pod's cgroup limit.",
		},
		{
			When:      "role: investigator (source)",
			ToolCalls: []ScriptedCall{{Name: "source.search", Args: map[string]any{"pattern": "OOM command not allowed"}}},
		},
		{
			When: "role: investigator (source)",
			Text: "The refusal is emitted when the used-memory check fails ahead of command execution, which confirms the client errors are the documented maxmemory path.",
		},
		{
			When: "role: correlator",
			Text: `{"hypotheses":[
  {"statement":"Redis reached its configured maxmemory; the eviction policy could not free space fast enough, so writes were refused and read latency rose as the hit ratio collapsed.","confidence":0.85,"supporting":["ev-1","ev-2"],"rationale":"Used memory sits above 90% of maxmemory, eviction starts before the latency rise, and the log shows OOM refusals in the same window."},
  {"statement":"A single slow command blocked the event loop.","confidence":0.2,"supporting":[],"rationale":"CPU stayed below saturation and no long fork pause was observed, so this does not fit the evidence."},
  {"statement":"The container was OOM-killed by the kubelet.","confidence":0.1,"supporting":[],"rationale":"No OOMKilled event and no restart marker; the process stayed up throughout."}
]}`,
		},
		{
			When: "role: critic",
			Text: `{"assessments":[
  {"id":"h-1","status":"supported","confidence":0.85,"rationale":"Three independent sources agree, and the ordering of eviction before latency rules out latency as the cause."},
  {"id":"h-2","status":"refuted","confidence":0.05,"rationale":"Contradicted by the CPU and fork evidence collected in this run."},
  {"id":"h-3","status":"refuted","confidence":0.05,"rationale":"Contradicted by the absence of any OOMKilled event or restart marker."}
]}`,
		},
		{
			When: "role: reporter",
			Text: `{"summary":"Redis is at its configured memory ceiling. Eviction began before latency rose and the log shows write refusals in the same window, so memory pressure is the cause rather than a consequence. The container was not OOM-killed: the limit being hit is Redis's own maxmemory.","recommendations":[
  {"statement":"Read CONFIG GET maxmemory-policy: it determines whether clients see errors or silent data loss under this pressure.","risk":"low","rationale":"The policy decides how the pressure presents to callers."},
  {"statement":"Identify the largest keyspaces with MEMORY DOCTOR before changing any limit.","risk":"low","rationale":"Raising a limit without knowing what fills it repeats the incident later."},
  {"statement":"Raise maxmemory only if the host has headroom; without it the kernel OOM killer replaces a degraded Redis with a dead one.","risk":"medium","rationale":"The failure mode changes from degradation to outage."}
]}`,
		},
		{
			When: "role: generalist",
			Text: `{"summary":"Redis reached its configured maxmemory. Eviction and write refusals follow from that, and the container was not OOM-killed.","hypotheses":[{"statement":"Memory pressure at maxmemory is causing eviction and refused writes.","confidence":0.85,"rationale":"Used memory is above 90% of maxmemory and the log shows OOM refusals in the window."}],"recommendations":[{"statement":"Check maxmemory-policy, then size the keyspace before raising any limit.","risk":"low","rationale":"Determines whether pressure presents as errors or as data loss."}]}`,
		},
		{
			Text: "No further analysis is available from the scripted provider.",
		},
	}}
}
