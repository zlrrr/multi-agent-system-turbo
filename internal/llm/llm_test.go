package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm/anthropic"
	_ "github.com/zlrrr/multi-agent-system-turbo/internal/llm/mock"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm/openai"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func toolDefs() []llm.ToolDefinition {
	return []llm.ToolDefinition{{
		Name: "promql.instant", Description: "run a PromQL query",
		Schema: tool.NewSchema(map[string]tool.Property{
			"query": {Type: tool.TypeString, Description: "PromQL"},
		}, "query"),
	}}
}

func conversation() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "diagnose redis latency"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "promql.instant", Args: map[string]any{"query": "up"}},
		}},
		{Role: llm.RoleTool, ToolCallID: "call-1", ToolName: "promql.instant", Content: "up = 1"},
	}
}

func TestRegistryOpen(t *testing.T) {
	names := llm.Names()
	for _, want := range []string{"mock", "anthropic", "openai"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("provider %s is not registered; registered: %v", want, names)
		}
	}
	p, err := llm.Open(config.LLMConfig{Provider: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "mock" {
		t.Fatalf("opened %s", p.Name())
	}
	_ = p.Close()
}

func TestUnknownProviderCoded(t *testing.T) {
	_, err := llm.Open(config.LLMConfig{Provider: "gemini"})
	if errs.CodeOf(err) != "MAS-2005" {
		t.Fatalf("got %v, want MAS-2005", err)
	}
}

func TestPerAgentModelOverride(t *testing.T) {
	cfg := config.LLMConfig{
		Model: "strong-model", Temperature: 0.2,
		PerAgent: map[string]config.AgentModel{
			"investigator": {Model: "cheap-model", Temperature: 0.7},
		},
	}
	if got := llm.ModelFor(cfg, "investigator"); got != "cheap-model" {
		t.Errorf("investigator model = %q", got)
	}
	if got := llm.ModelFor(cfg, "correlator"); got != "strong-model" {
		t.Errorf("correlator model = %q; it should inherit the default", got)
	}
	if got := llm.TemperatureFor(cfg, "investigator"); got != 0.7 {
		t.Errorf("investigator temperature = %v", got)
	}
	if got := llm.TemperatureFor(cfg, "planner"); got != 0.2 {
		t.Errorf("planner temperature = %v", got)
	}
}

func TestCountingAccumulatesUsage(t *testing.T) {
	p, _ := llm.Open(config.LLMConfig{Provider: "mock"})
	c := llm.NewCounting(p, nil)
	for i := 0; i < 3; i++ {
		if _, err := c.Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "role: reporter"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	calls, u := c.Totals()
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
	if u.PromptTokens == 0 || u.CompletionTokens == 0 {
		t.Fatalf("usage not accumulated: %+v", u)
	}
	if c.Name() != "mock" {
		t.Fatalf("wrapper lost the provider name: %s", c.Name())
	}
}

// ── Anthropic ───────────────────────────────────────────────────────────────

func TestAnthropicToolRoundTrip(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		if r.Header.Get("anthropic-version") == "" {
			t.Error("anthropic-version header missing")
		}
		if r.Header.Get("x-api-key") != "sk-ant-test-abcdef" {
			t.Errorf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		_, _ = w.Write([]byte(`{"id":"msg_1","model":"claude-opus-5","stop_reason":"tool_use",
		  "content":[{"type":"text","text":"Checking memory."},
		             {"type":"tool_use","id":"toolu_1","name":"promql.instant","input":{"query":"redis_memory_used_bytes"}}],
		  "usage":{"input_tokens":120,"output_tokens":40}}`))
	}))
	defer srv.Close()

	p, err := anthropic.New(config.LLMConfig{
		Provider: "anthropic", Model: "claude-opus-5", BaseURL: srv.URL,
		APIKey: "sk-ant-test-abcdef", MaxTokens: 2048, Timeout: config.Duration(2 * time.Second),
	}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	resp, err := p.Complete(context.Background(), llm.Request{
		System: "you are a diagnostician", Messages: conversation(), Tools: toolDefs(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != llm.StopToolUse || len(resp.ToolCalls) != 1 {
		t.Fatalf("response = %+v", resp)
	}
	if resp.ToolCalls[0].Name != "promql.instant" || resp.ToolCalls[0].Args["query"] != "redis_memory_used_bytes" {
		t.Fatalf("tool call = %+v", resp.ToolCalls[0])
	}
	if resp.Text != "Checking memory." {
		t.Fatalf("text = %q", resp.Text)
	}
	if resp.Usage.PromptTokens != 120 || resp.Usage.CompletionTokens != 40 {
		t.Fatalf("usage = %+v", resp.Usage)
	}

	// The request must carry the system prompt out of band and render the tool
	// result as a user-role tool_result block, as the API requires.
	if captured["system"] != "you are a diagnostician" {
		t.Errorf("system = %v", captured["system"])
	}
	msgs := captured["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" {
		t.Errorf("tool results must be user-role, got %v", last["role"])
	}
	block := last["content"].([]any)[0].(map[string]any)
	if block["type"] != "tool_result" || block["tool_use_id"] != "call-1" {
		t.Errorf("tool result block = %v", block)
	}
	tools := captured["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "promql.instant" {
		t.Errorf("tools = %v", tools)
	}
}

func TestAnthropicMergesConsecutiveToolResults(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_, _ = w.Write([]byte(`{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{}}`))
	}))
	defer srv.Close()
	p, _ := anthropic.New(config.LLMConfig{BaseURL: srv.URL, APIKey: "k-abcdef"}, srv.Client())

	_, err := p.Complete(context.Background(), llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "t1"}, {ID: "b", Name: "t2"}}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "r1"},
		{Role: llm.RoleTool, ToolCallID: "b", Content: "r2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	msgs := captured["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if n := len(last["content"].([]any)); n != 2 {
		t.Fatalf("consecutive tool results were not merged into one message: %d blocks", n)
	}
}

func TestAnthropicErrorMapping(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorised":  {http.StatusUnauthorized, `{"error":{"message":"invalid key"}}`, "MAS-2002"},
		"forbidden":     {http.StatusForbidden, `{}`, "MAS-2002"},
		"rate limited":  {http.StatusTooManyRequests, `{}`, "MAS-2003"},
		"server error":  {http.StatusInternalServerError, `{"error":{"message":"overloaded"}}`, "MAS-2008"},
		"not json":      {http.StatusOK, `<html/>`, "MAS-2008"},
		"error in body": {http.StatusOK, `{"error":{"type":"x","message":"bad"}}`, "MAS-2008"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p, _ := anthropic.New(config.LLMConfig{BaseURL: srv.URL, APIKey: "k-abcdef"}, srv.Client())
			_, err := p.Complete(context.Background(), llm.Request{Messages: conversation()})
			if errs.CodeOf(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, errs.CodeOf(err), tc.want)
			}
		})
	}
}

func TestAnthropicUnreachableIsCoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	p, _ := anthropic.New(config.LLMConfig{BaseURL: srv.URL, APIKey: "k-abcdef"}, srv.Client())
	srv.Close()
	_, err := p.Complete(context.Background(), llm.Request{Messages: conversation()})
	if errs.CodeOf(err) != "MAS-2001" {
		t.Fatalf("got %v, want MAS-2001", err)
	}
}

// TestAPIKeyRedactedInErrors is the FR-016 boundary test for provider errors.
func TestAPIKeyRedactedInErrors(t *testing.T) {
	const key = "sk-ant-super-secret-key-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream failure"}}`))
	}))
	defer srv.Close()
	p, _ := anthropic.New(config.LLMConfig{BaseURL: srv.URL, APIKey: config.Secret(key)}, srv.Client())
	_, err := p.Complete(context.Background(), llm.Request{Messages: conversation()})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("the API key leaked into an error string: %v", err)
	}
}

func TestAnthropicBadToolArgumentsAreCoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","stop_reason":"tool_use","usage":{},
		  "content":[{"type":"tool_use","id":"t1","name":"promql.instant","input":"not-an-object"}]}`))
	}))
	defer srv.Close()
	p, _ := anthropic.New(config.LLMConfig{BaseURL: srv.URL, APIKey: "k-abcdef"}, srv.Client())
	_, err := p.Complete(context.Background(), llm.Request{Messages: conversation(), Tools: toolDefs()})
	if errs.CodeOf(err) != "MAS-2004" {
		t.Fatalf("got %v, want MAS-2004", err)
	}
}

// ── OpenAI-compatible ───────────────────────────────────────────────────────

func TestOpenAIToolRoundTrip(t *testing.T) {
	var captured map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&captured)
		if r.Header.Get("Authorization") != "Bearer sk-test-abcdef" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"model":"gpt-4o","choices":[{"finish_reason":"tool_calls","message":{
		  "role":"assistant","content":"Checking.","tool_calls":[
		    {"id":"call_9","type":"function","function":{"name":"promql.instant","arguments":"{\"query\":\"up\"}"}}]}}],
		  "usage":{"prompt_tokens":80,"completion_tokens":20}}`))
	}))
	defer srv.Close()

	p, err := openai.New(config.LLMConfig{
		Provider: "openai", Model: "gpt-4o", BaseURL: srv.URL,
		APIKey: "sk-test-abcdef", MaxTokens: 1024,
	}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Complete(context.Background(), llm.Request{
		System: "system prompt", Messages: conversation(), Tools: toolDefs(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/chat/completions" {
		t.Fatalf("path = %s", path)
	}
	if resp.StopReason != llm.StopToolUse || len(resp.ToolCalls) != 1 {
		t.Fatalf("response = %+v", resp)
	}
	if resp.ToolCalls[0].Args["query"] != "up" {
		t.Fatalf("tool args = %+v", resp.ToolCalls[0].Args)
	}
	msgs := captured["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("the system prompt must lead the message list: %v", msgs[0])
	}
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "call-1" {
		t.Errorf("tool result message = %v", last)
	}
}

func TestBaseURLOverride(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"model":"local","choices":[{"finish_reason":"stop","message":{"content":"hi"}}],"usage":{}}`))
	}))
	defer srv.Close()

	// A self-hosted endpoint with no API key at all must work: that is the
	// common Ollama and vLLM configuration.
	p, _ := openai.New(config.LLMConfig{BaseURL: srv.URL, Model: "llama3"}, srv.Client())
	resp, err := p.Complete(context.Background(), llm.Request{Messages: conversation()})
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("the configured base URL was not used")
	}
	if resp.Text != "hi" {
		t.Fatalf("text = %q", resp.Text)
	}
}

func TestOpenAIInfersToolUseWhenFinishReasonOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{
		  "tool_calls":[{"id":"c1","type":"function","function":{"name":"promql.instant","arguments":"{}"}}]}}],"usage":{}}`))
	}))
	defer srv.Close()
	p, _ := openai.New(config.LLMConfig{BaseURL: srv.URL}, srv.Client())
	resp, err := p.Complete(context.Background(), llm.Request{Messages: conversation(), Tools: toolDefs()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != llm.StopToolUse {
		t.Fatalf("stop reason = %s; a compatible server that omits tool_calls must still work", resp.StopReason)
	}
}

func TestOpenAIErrorMapping(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorised": {http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, "MAS-2002"},
		"rate limited": {http.StatusTooManyRequests, `{}`, "MAS-2003"},
		"server error": {http.StatusBadGateway, `upstream`, "MAS-2008"},
		"no choices":   {http.StatusOK, `{"model":"m","choices":[],"usage":{}}`, "MAS-2008"},
		"bad tool args": {http.StatusOK, `{"model":"m","choices":[{"finish_reason":"tool_calls","message":{
		  "tool_calls":[{"id":"c","type":"function","function":{"name":"t","arguments":"not json"}}]}}],"usage":{}}`, "MAS-2004"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p, _ := openai.New(config.LLMConfig{BaseURL: srv.URL, APIKey: "k-abcdef"}, srv.Client())
			_, err := p.Complete(context.Background(), llm.Request{Messages: conversation()})
			if errs.CodeOf(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, errs.CodeOf(err), tc.want)
			}
		})
	}
}
