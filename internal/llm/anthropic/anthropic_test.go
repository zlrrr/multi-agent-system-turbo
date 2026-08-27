package anthropic

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

// These tests exercise the provider against a server that speaks the Messages
// API shape, so translation in both directions is checked without a network or
// a key. Until they existed this file was at 0% coverage: the provider a real
// operator runs was the least verified code in the repository, while the mock
// that only tests use was the most.

// server captures what the provider sent and replies with what a test dictates.
type server struct {
	*httptest.Server
	gotBody   map[string]any
	gotHeader http.Header
}

func newServer(t *testing.T, status int, reply string) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("posted to %s, want /v1/messages", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method %s, want POST", r.Method)
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

func providerFor(t *testing.T, s *server) *Provider {
	t.Helper()
	p, err := New(config.LLMConfig{
		BaseURL: s.URL, Model: "claude-opus-5", MaxTokens: 1024,
		APIKey: config.Secret("test-key"), Timeout: config.Duration(5 * time.Second),
	}, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const textReply = `{
  "id": "msg_1", "model": "claude-opus-5", "stop_reason": "end_turn",
  "content": [{"type": "text", "text": "Memory is at the ceiling."}],
  "usage": {"input_tokens": 1200, "output_tokens": 90}
}`

func TestCompleteTranslatesTextAndUsage(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	resp, err := providerFor(t, s).Complete(context.Background(), llm.Request{
		System:      "You are a diagnostic agent.",
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "what is wrong?"}},
		Temperature: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Memory is at the ceiling." {
		t.Errorf("text = %q", resp.Text)
	}
	if resp.StopReason != llm.StopEnd {
		t.Errorf("stop = %q, want %q", resp.StopReason, llm.StopEnd)
	}
	if resp.Usage.PromptTokens != 1200 || resp.Usage.CompletionTokens != 90 {
		t.Errorf("usage = %+v; without it a run cannot be priced or budgeted", resp.Usage)
	}
	if resp.Model != "claude-opus-5" {
		t.Errorf("model = %q; the served model is what a bill would name", resp.Model)
	}

	// The request shape the API requires.
	if s.gotBody["system"] != "You are a diagnostic agent." {
		t.Errorf("system prompt was not sent at the top level: %v", s.gotBody["system"])
	}
	if s.gotBody["max_tokens"] == nil {
		t.Error("max_tokens is required by the API and was not sent")
	}
	if s.gotBody["temperature"] == nil {
		t.Error("temperature was dropped")
	}
	if got := s.gotHeader.Get("anthropic-version"); got == "" {
		t.Error("the anthropic-version header is required and was not sent")
	}
	if got := s.gotHeader.Get("x-api-key"); got != "test-key" {
		t.Errorf("x-api-key = %q", got)
	}
}

// TestTemperatureZeroIsOmitted: the provider sends temperature only when it was
// set, so the API's own default applies rather than a silent zero.
func TestTemperatureZeroIsOmitted(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	_, err := providerFor(t, s).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := s.gotBody["temperature"]; present {
		t.Error("an unset temperature was sent as 0, overriding the API default")
	}
}

func TestCompleteTranslatesToolCalls(t *testing.T) {
	const reply = `{
	  "id": "msg_2", "model": "claude-opus-5", "stop_reason": "tool_use",
	  "content": [
	    {"type": "text", "text": "Checking memory."},
	    {"type": "tool_use", "id": "toolu_1", "name": "promql.range",
	     "input": {"query": "redis_memory_used_bytes", "limit": 100}}
	  ],
	  "usage": {"input_tokens": 10, "output_tokens": 5}
	}`
	s := newServer(t, http.StatusOK, reply)

	resp, err := providerFor(t, s).Complete(context.Background(), llm.Request{
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
		t.Errorf("stop = %q, want %q", resp.StopReason, llm.StopToolUse)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want one", resp.ToolCalls)
	}
	call := resp.ToolCalls[0]
	if call.ID != "toolu_1" || call.Name != "promql.range" {
		t.Errorf("call = %+v", call)
	}
	if call.Args["query"] != "redis_memory_used_bytes" {
		t.Errorf("args lost the query: %+v", call.Args)
	}
	if resp.Text != "Checking memory." {
		t.Errorf("text alongside a tool call was dropped: %q", resp.Text)
	}

	// The tool definition must reach the API in its own shape.
	tools, _ := s.gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools sent = %v", s.gotBody["tools"])
	}
	spec, _ := tools[0].(map[string]any)
	if spec["name"] != "promql.range" || spec["input_schema"] == nil {
		t.Errorf("tool spec = %+v; the API needs name and input_schema", spec)
	}
}

// TestToolResultsBecomeUserBlocksAndMerge is the translation most likely to be
// wrong and least likely to be noticed: the API requires tool results as
// user-role blocks, and consecutive results in one message.
func TestToolResultsBecomeUserBlocksAndMerge(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	_, err := providerFor(t, s).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "ignored — carried at the top level"},
			{Role: llm.RoleUser, Content: "check memory"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "t1", Name: "promql.range", Args: map[string]any{"query": "x"}},
				{ID: "t2", Name: "loki.query", Args: map[string]any{"query": "y"}},
			}},
			{Role: llm.RoleTool, ToolCallID: "t1", Content: "900MB"},
			{Role: llm.RoleTool, ToolCallID: "t2", Content: "OOM lines"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := s.gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("sent %d messages, want 3 (user, assistant, merged tool results): %v", len(msgs), msgs)
	}
	for i, wantRole := range []string{"user", "assistant", "user"} {
		m, _ := msgs[i].(map[string]any)
		if m["role"] != wantRole {
			t.Errorf("message %d role = %v, want %s", i, m["role"], wantRole)
		}
	}
	// The system message must not appear as a message.
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		if m["role"] == "system" {
			t.Error("a system message was sent in the messages array, which the API rejects")
		}
	}
	// Both results must sit in the one trailing user message.
	last, _ := msgs[2].(map[string]any)
	blocks, _ := last["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("tool results were not merged into one message: %v", blocks)
	}
	for _, raw := range blocks {
		b, _ := raw.(map[string]any)
		if b["type"] != "tool_result" || b["tool_use_id"] == nil {
			t.Errorf("block = %+v; a result must carry its tool_use_id", b)
		}
	}
}

// TestEmptyAssistantTurnIsDropped: the API rejects a message with no content,
// and an assistant turn that produced neither text nor a call is exactly that.
func TestEmptyAssistantTurnIsDropped(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	_, err := providerFor(t, s).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleAssistant, Content: "   "},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("sent %d messages, want the empty assistant turn dropped: %v", len(msgs), msgs)
	}
}

func TestStopReasonsAreTranslated(t *testing.T) {
	for apiReason, want := range map[string]llm.StopReason{
		"end_turn":      llm.StopEnd,
		"tool_use":      llm.StopToolUse,
		"max_tokens":    llm.StopMaxToken,
		"refusal":       llm.StopRefusal,
		"stop_sequence": llm.StopEnd,
	} {
		t.Run(apiReason, func(t *testing.T) {
			s := newServer(t, http.StatusOK, `{"model":"m","stop_reason":"`+apiReason+
				`","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
			resp, err := providerFor(t, s).Complete(context.Background(), llm.Request{
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

// TestErrorsCarryTheRightCode: an operator reading MAS-2002 knows to check the
// key; one reading MAS-2003 knows to wait. Collapsing them would waste both.
func TestErrorsCarryTheRightCode(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorized":  {http.StatusUnauthorized, `{"error":{"message":"invalid key"}}`, "MAS-2002"},
		"forbidden":     {http.StatusForbidden, `{"error":{"message":"no access"}}`, "MAS-2002"},
		"rate limited":  {http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, "MAS-2003"},
		"server error":  {http.StatusInternalServerError, `{"error":{"message":"boom"}}`, "MAS-2008"},
		"not json":      {http.StatusOK, `<html>gateway</html>`, "MAS-2008"},
		"error in body": {http.StatusOK, `{"error":{"message":"overloaded"}}`, "MAS-2008"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newServer(t, tc.status, tc.body)
			_, err := providerFor(t, s).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
			})
			if code := errs.CodeOf(err); code != tc.want {
				t.Errorf("got %v (%s), want %s", err, code, tc.want)
			}
		})
	}
}

// TestMalformedToolArgumentsAreCoded: a tool call whose input is not an object
// must be a coded failure the loop can repair, not a panic and not a silent
// call with no arguments.
func TestMalformedToolArgumentsAreCoded(t *testing.T) {
	s := newServer(t, http.StatusOK, `{"model":"m","stop_reason":"tool_use","content":[
	  {"type":"tool_use","id":"t1","name":"promql.range","input":"not-an-object"}
	],"usage":{"input_tokens":1,"output_tokens":1}}`)
	_, err := providerFor(t, s).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
	})
	if code := errs.CodeOf(err); code != "MAS-2004" {
		t.Fatalf("got %v (%s), want MAS-2004", err, code)
	}
}

// TestAPIKeyIsNeverStoredInPlaintext guards the promise New's comment makes.
func TestAPIKeyIsNeverStoredInPlaintext(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	p := providerFor(t, s)
	if rendered := strings.Join([]string{p.baseURL, p.model, p.apiKey.String()}, " "); strings.Contains(rendered, "test-key") {
		t.Errorf("the key is recoverable from the struct's rendering: %s", rendered)
	}
}

// TestContextCancellationIsCoded: a cancelled run must not hang or panic.
func TestContextCancellationIsCoded(t *testing.T) {
	s := newServer(t, http.StatusOK, textReply)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := providerFor(t, s).Complete(ctx, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
	})
	if code := errs.CodeOf(err); code != "MAS-2001" {
		t.Errorf("got %v (%s), want MAS-2001", err, code)
	}
}
