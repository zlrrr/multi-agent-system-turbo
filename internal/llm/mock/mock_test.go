package mock

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func req(content string, tools ...string) llm.Request {
	r := llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: content}}}
	for _, name := range tools {
		r.Tools = append(r.Tools, llm.ToolDefinition{Name: name, Schema: tool.NewSchema(nil)})
	}
	return r
}

// TestMockDeterminism underwrites NFR-010: identical input must produce an
// identical report, which is only possible if the provider is deterministic.
func TestMockDeterminism(t *testing.T) {
	run := func() string {
		p := New(DefaultScript())
		var b strings.Builder
		for _, role := range []string{"role: planner", "role: correlator", "role: critic", "role: reporter"} {
			resp, err := p.Complete(context.Background(), req(role))
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString(resp.Text)
			b.WriteByte('|')
		}
		return b.String()
	}
	if run() != run() {
		t.Fatal("the mock provider is not deterministic")
	}
}

func TestMockMatchesOnContent(t *testing.T) {
	p := New(DefaultScript())
	resp, err := p.Complete(context.Background(), req("you are the role: reporter for this run"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "recommendations") {
		t.Fatalf("the reporter reply was not selected: %q", resp.Text)
	}
}

func TestMockToolSequence(t *testing.T) {
	p := New(DefaultScript())
	first, err := p.Complete(context.Background(), req("role: investigator (metrics)", "promql.range"))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "promql.range" {
		t.Fatalf("first turn should call a tool: %+v", first)
	}
	if first.StopReason != llm.StopToolUse {
		t.Fatalf("stop reason = %s", first.StopReason)
	}

	// With the tool result in context, the mock must move on rather than repeat.
	second := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "role: investigator (metrics)"},
			{Role: llm.RoleAssistant, ToolCalls: first.ToolCalls},
			{Role: llm.RoleTool, ToolCallID: first.ToolCalls[0].ID, ToolName: "promql.range", Content: "eviction rose"},
		},
		Tools: []llm.ToolDefinition{{Name: "promql.range"}},
	}
	resp, err := p.Complete(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("the mock repeated a tool whose result was already in context: %+v", resp.ToolCalls)
	}
	if !strings.Contains(resp.Text, "eviction") {
		t.Fatalf("the follow-up reply was not selected: %q", resp.Text)
	}
}

func TestMockSkipsRepliesCallingUnavailableTools(t *testing.T) {
	p := New(DefaultScript())
	// The investigator reply wants promql.range; with only loki.query offered it
	// must fall through rather than emit an uninvokable call.
	resp, err := p.Complete(context.Background(), req("role: investigator (metrics)", "loki.query"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range resp.ToolCalls {
		if tc.Name != "loki.query" {
			t.Fatalf("the mock called a tool the agent does not have: %s", tc.Name)
		}
	}
}

func TestMockFallbackReply(t *testing.T) {
	p := New(DefaultScript())
	resp, err := p.Complete(context.Background(), req("something no reply matches"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text == "" || resp.StopReason != llm.StopEnd {
		t.Fatalf("the fallback reply must terminate cleanly: %+v", resp)
	}
}

func TestMockRecordsCalls(t *testing.T) {
	p := New(DefaultScript())
	_, _ = p.Complete(context.Background(), req("role: planner"))
	_, _ = p.Complete(context.Background(), req("role: critic"))
	if got := len(p.Calls()); got != 2 {
		t.Fatalf("recorded %d calls, want 2", got)
	}
}

func TestLoadScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.yaml")
	body := `
replies:
  - when: "hello"
    text: "world"
  - text: "fallback"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadScript(path)
	if err != nil {
		t.Fatal(err)
	}
	p := New(s)
	resp, _ := p.Complete(context.Background(), req("say hello please"))
	if resp.Text != "world" {
		t.Fatalf("text = %q", resp.Text)
	}
}

func TestLoadScriptErrors(t *testing.T) {
	if _, err := LoadScript("/no/such/script.yaml"); errs.CodeOf(err) != "MAS-1001" {
		t.Fatalf("missing file: got %v, want MAS-1001", err)
	}
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte("replies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScript(empty); errs.CodeOf(err) != "MAS-1003" {
		t.Fatalf("empty script: got %v, want MAS-1003", err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("replies: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScript(bad); errs.CodeOf(err) != "MAS-1001" {
		t.Fatalf("malformed script: got %v, want MAS-1001", err)
	}
}

// TestDefaultScriptCoversEveryRole keeps the out-of-the-box demo honest: a new
// user running `mas diagnose` with no credentials must get a complete report.
func TestDefaultScriptCoversEveryRole(t *testing.T) {
	p := New(DefaultScript())
	for _, role := range []string{
		"role: planner", "role: correlator", "role: critic", "role: reporter", "role: generalist",
	} {
		resp, err := p.Complete(context.Background(), req(role))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(resp.Text) == "" {
			t.Errorf("%s has no scripted reply", role)
		}
		if strings.Contains(resp.Text, "No further analysis") {
			t.Errorf("%s fell through to the fallback reply", role)
		}
	}
}

func TestDefaultScriptStructuredRepliesParse(t *testing.T) {
	p := New(DefaultScript())
	for _, role := range []string{"role: correlator", "role: critic", "role: reporter", "role: generalist"} {
		resp, err := p.Complete(context.Background(), req(role))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(strings.TrimSpace(resp.Text), "{") {
			t.Errorf("%s reply is not JSON: %q", role, resp.Text)
		}
	}
}
