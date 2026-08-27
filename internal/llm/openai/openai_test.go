package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// This provider is the one an operator points at DeepSeek, Qwen, vLLM or
// Ollama, which is exactly why the compatible-but-not-identical cases below
// matter: those servers differ from each other in small ways, and the provider
// has to absorb the differences rather than pass them on as failures.

type server struct {
	*httptest.Server
	gotBody   map[string]any
	gotHeader http.Header
}

func newServer(t *testing.T, status int, reply string) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("posted to %s, want …/chat/completions", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		s.gotHeader = r.Header.Clone()
		_ = json.Unmarshal(body, &s.gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)
	return s
}

func providerFor(t *testing.T, s *server, key string) *Provider {
	t.Helper()
	p, err := New(config.LLMConfig{
		BaseURL: s.URL, Model: "qwen2.5:14b", MaxTokens: 512,
		APIKey: config.Secret(key), Timeout: config.Duration(5 * time.Second),
	}, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const textReply = `{
  "id": "chatcmpl-1", "model": "qwen2.5:14b",
  "choices": [{"index": 0, "finish_reason": "stop",
    "message": {"role": "assistant", "content": "Memory is at the ceiling."}}],
  "usage": {"prompt_tokens": 800, "completion_tokens": 40}
}`

func TestCompleteTranslatesTextAndUsage(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	resp, err := providerFor(t, s, "sk-test").Complete(context.Background(), llm.Request{
		System:      "You are a diagnostic agent.",
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "what is wrong?"}},
		Temperature: 0.3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Memory is at the ceiling." {
		t.Errorf("text = %q", resp.Text)
	}
	if resp.StopReason != llm.StopEnd {
		t.Errorf("stop = %q", resp.StopReason)
	}
	if resp.Usage.PromptTokens != 800 || resp.Usage.CompletionTokens != 40 {
		t.Errorf("usage = %+v; without it a run cannot be priced or budgeted", resp.Usage)
	}
	if resp.Model != "qwen2.5:14b" {
		t.Errorf("model = %q", resp.Model)
	}

	// The system prompt is a message here, unlike Anthropic's top-level field.
	msgs, _ := s.gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("sent %d messages, want system + user: %v", len(msgs), msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "You are a diagnostic agent." {
		t.Errorf("the system prompt was not sent as the leading message: %+v", first)
	}
	if got := s.gotHeader.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
}

// TestNoKeySendsNoAuthorizationHeader is what makes a local server usable: an
// Ollama or vLLM endpoint has no key, and sending an empty bearer token is an
// error on some of them.
func TestNoKeySendsNoAuthorizationHeader(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	_, err := providerFor(t, s, "").Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.gotHeader.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q; a keyless endpoint must be sent no header at all", got)
	}
}

func TestCompleteTranslatesToolCalls(t *testing.T) {
	const reply = `{
	  "model": "qwen2.5:14b",
	  "choices": [{"index": 0, "finish_reason": "tool_calls", "message": {
	    "role": "assistant", "content": "",
	    "tool_calls": [{"id": "call_1", "type": "function",
	      "function": {"name": "promql.range", "arguments": "{\"query\":\"redis_memory_used_bytes\"}"}}]}}],
	  "usage": {"prompt_tokens": 10, "completion_tokens": 5}
	}`
	s := newServer(t, http.StatusOK, reply)

	resp, err := providerFor(t, s, "k").Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "check memory"}},
		Tools: []llm.ToolDefinition{{
			Name: "promql.range", Description: "range query",
			Schema: tool.NewSchema(map[string]tool.Property{
				"query": {Type: tool.TypeString, Description: "PromQL"},
			}, "query"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != llm.StopToolUse {
		t.Errorf("stop = %q", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "promql.range" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	// Arguments arrive as a JSON *string* here, not an object.
	if resp.ToolCalls[0].Args["query"] != "redis_memory_used_bytes" {
		t.Errorf("arguments were not decoded from their JSON string: %+v", resp.ToolCalls[0].Args)
	}

	tools, _ := s.gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools sent = %v", s.gotBody["tools"])
	}
	spec, _ := tools[0].(map[string]any)
	if spec["type"] != "function" {
		t.Errorf("tool spec type = %v, want function", spec["type"])
	}
	fn, _ := spec["function"].(map[string]any)
	if fn["name"] != "promql.range" || fn["parameters"] == nil {
		t.Errorf("function spec = %+v; the API needs name and parameters", fn)
	}
}

// TestToolCallsWithoutFinishReasonStillStop is the compatibility case the code
// carries a comment about, and the reason it is worth a test: several
// OpenAI-compatible servers return tool calls with finish_reason "stop", and a
// loop that trusted the reason alone would answer the user with an empty
// message instead of running the tool.
func TestToolCallsWithoutFinishReasonStillStop(t *testing.T) {
	s := newServer(t, http.StatusOK, `{
	  "model": "m",
	  "choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": "",
	    "tool_calls": [{"id": "c1", "type": "function",
	      "function": {"name": "loki.query", "arguments": "{}"}}]}}],
	  "usage": {"prompt_tokens": 1, "completion_tokens": 1}
	}`)
	resp, err := providerFor(t, s, "k").Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != llm.StopToolUse {
		t.Errorf("stop = %q; a reply carrying tool calls must stop for tool use however "+
			"the server labelled it", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Errorf("tool calls = %+v", resp.ToolCalls)
	}
}

// TestEmptyToolArgumentsAreAnEmptyObject: some servers send "" rather than "{}"
// for a tool with no arguments, and treating that as malformed would fail a
// call that is perfectly valid.
func TestEmptyToolArgumentsAreAnEmptyObject(t *testing.T) {
	s := newServer(t, http.StatusOK, `{
	  "model": "m",
	  "choices": [{"finish_reason": "tool_calls", "message": {"role": "assistant",
	    "tool_calls": [{"id": "c1", "type": "function",
	      "function": {"name": "host.resources", "arguments": ""}}]}}],
	  "usage": {"prompt_tokens": 1, "completion_tokens": 1}
	}`)
	resp, err := providerFor(t, s, "k").Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("empty arguments were treated as malformed: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Args == nil {
		t.Errorf("tool calls = %+v, want one with an empty argument map", resp.ToolCalls)
	}
}

// TestToolResultsAreToolRoleMessages: this API wants results as their own
// tool-role messages carrying tool_call_id, which is a different shape from
// Anthropic's user blocks.
func TestToolResultsAreToolRoleMessages(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	_, err := providerFor(t, s, "k").Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "check"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "promql.range", Args: map[string]any{"query": "x"}},
			}},
			{Role: llm.RoleTool, ToolCallID: "c1", ToolName: "promql.range", Content: "900MB"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("sent %d messages, want 3: %v", len(msgs), msgs)
	}
	assistant, _ := msgs[1].(map[string]any)
	calls, _ := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("the assistant's tool call was not sent back: %+v", assistant)
	}
	call, _ := calls[0].(map[string]any)
	fn, _ := call["function"].(map[string]any)
	if fn["arguments"] == nil {
		t.Error("arguments must be sent as a JSON string, and were absent")
	}
	if _, isString := fn["arguments"].(string); !isString {
		t.Errorf("arguments = %T, want a JSON string as this API requires", fn["arguments"])
	}
	result, _ := msgs[2].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "c1" {
		t.Errorf("tool result = %+v; it must be a tool-role message with its id", result)
	}
}

func TestFinishReasonsAreTranslated(t *testing.T) {
	for reason, want := range map[string]llm.StopReason{
		"stop":           llm.StopEnd,
		"tool_calls":     llm.StopToolUse,
		"function_call":  llm.StopToolUse,
		"length":         llm.StopMaxToken,
		"content_filter": llm.StopRefusal,
		"":               llm.StopEnd,
	} {
		name := reason
		if name == "" {
			name = "(absent)"
		}
		t.Run(name, func(t *testing.T) {
			s := newServer(t, http.StatusOK, `{"model":"m","choices":[{"finish_reason":"`+reason+
				`","message":{"role":"assistant","content":"x"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
			resp, err := providerFor(t, s, "k").Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StopReason != want {
				t.Errorf("stop = %q, want %q", resp.StopReason, want)
			}
		})
	}
}

func TestErrorsCarryTheRightCode(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorized":  {http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, "MAS-2002"},
		"forbidden":     {http.StatusForbidden, `{"error":{"message":"denied"}}`, "MAS-2002"},
		"rate limited":  {http.StatusTooManyRequests, `{"error":{"message":"slow"}}`, "MAS-2003"},
		"server error":  {http.StatusBadGateway, `upstream unavailable`, "MAS-2008"},
		"not json":      {http.StatusOK, `<html>proxy</html>`, "MAS-2008"},
		"error in body": {http.StatusOK, `{"error":{"message":"model not found"}}`, "MAS-2008"},
		"no choices":    {http.StatusOK, `{"model":"m","choices":[]}`, "MAS-2008"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newServer(t, tc.status, tc.body)
			_, err := providerFor(t, s, "k").Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
			})
			if code := errs.CodeOf(err); code != tc.want {
				t.Errorf("got %v (%s), want %s", err, code, tc.want)
			}
		})
	}
}

func TestMalformedToolArgumentsAreCoded(t *testing.T) {
	s := newServer(t, http.StatusOK, `{"model":"m","choices":[{"finish_reason":"tool_calls",
	  "message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function",
	    "function":{"name":"promql.range","arguments":"not json at all"}}]}}],
	  "usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	_, err := providerFor(t, s, "k").Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
	})
	if code := errs.CodeOf(err); code != "MAS-2004" {
		t.Fatalf("got %v (%s), want MAS-2004", err, code)
	}
}

func TestContextCancellationIsCoded(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := providerFor(t, s, "k").Complete(ctx, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
	})
	if code := errs.CodeOf(err); code != "MAS-2001" {
		t.Errorf("got %v (%s), want MAS-2001", err, code)
	}
}
