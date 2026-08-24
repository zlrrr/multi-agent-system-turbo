// Package anthropic implements llm.Provider against the Anthropic Messages API.
//
// Governs: specs/001-mvp-core/design-lld.md §2.13
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// DefaultBaseURL is the public API endpoint.
const DefaultBaseURL = "https://api.anthropic.com"

// APIVersion is the Messages API version header value.
const APIVersion = "2023-06-01"

func init() {
	llm.Register("anthropic", func(cfg config.LLMConfig) (llm.Provider, error) { return New(cfg, nil) })
}

// Provider talks to the Anthropic Messages API.
type Provider struct {
	baseURL   string
	apiKey    config.Secret
	model     string
	maxTokens int
	hc        *http.Client
}

// New builds a provider. The API key stays a Secret until the moment a request
// header is written, and is never stored in plaintext on the struct.
func New(cfg config.LLMConfig, hc *http.Client) (*Provider, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	timeout := cfg.Timeout.D()
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	return &Provider{baseURL: base, apiKey: cfg.APIKey, model: cfg.Model, maxTokens: maxTokens, hc: hc}, nil
}

// Name identifies the provider.
func (p *Provider) Name() string { return "anthropic" }

// Close is a no-op; the HTTP client needs no teardown.
func (p *Provider) Close() error { return nil }

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type toolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type request struct {
	Model       string     `json:"model"`
	MaxTokens   int        `json:"max_tokens"`
	System      string     `json:"system,omitempty"`
	Messages    []message  `json:"messages"`
	Tools       []toolSpec `json:"tools,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
}

type response struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one completion request.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	body, err := json.Marshal(p.buildRequest(req))
	if err != nil {
		return llm.Response{}, errs.Wrap(err, "MAS-2008", p.Name(), err.Error())
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return llm.Response{}, errs.Wrap(err, "MAS-2001", p.Name())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", APIVersion)
	key, err := p.apiKey.Reveal()
	if err != nil {
		return llm.Response{}, err
	}
	httpReq.Header.Set("x-api-key", key)

	resp, err := p.hc.Do(httpReq)
	if err != nil {
		// The error string can echo a URL carrying credentials, so it is never
		// interpolated verbatim.
		return llm.Response{}, errs.Wrap(err, "MAS-2001", p.Name())
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return llm.Response{}, errs.Wrap(err, "MAS-2008", p.Name(), err.Error())
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return llm.Response{}, errs.New("MAS-2002", p.Name())
	case http.StatusTooManyRequests:
		return llm.Response{}, errs.New("MAS-2003", p.Name())
	default:
		return llm.Response{}, errs.New("MAS-2008", p.Name(), apiMessage(raw, resp.StatusCode))
	}

	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return llm.Response{}, errs.Wrap(err, "MAS-2008", p.Name(), "response is not JSON")
	}
	if out.Error != nil {
		return llm.Response{}, errs.New("MAS-2008", p.Name(), out.Error.Message)
	}
	return p.parseResponse(out)
}

func (p *Provider) buildRequest(req llm.Request) request {
	model := req.Model
	if model == "" {
		model = p.model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = p.maxTokens
	}
	out := request{Model: model, MaxTokens: maxTokens, System: req.System}
	if req.Temperature > 0 {
		t := req.Temperature
		out.Temperature = &t
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, toolSpec{
			Name: t.Name, Description: t.Description, InputSchema: t.Schema,
		})
	}
	out.Messages = buildMessages(req.Messages)
	return out
}

// buildMessages translates the canonical conversation into Anthropic's block
// form. Tool results must be user-role blocks, and consecutive results are
// merged into one message as the API requires.
func buildMessages(msgs []llm.Message) []message {
	var out []message
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			continue // carried in the top-level system field
		case llm.RoleUser:
			out = append(out, message{Role: "user", Content: []contentBlock{{Type: "text", Text: m.Content}}})
		case llm.RoleAssistant:
			blocks := []contentBlock{}
			if strings.TrimSpace(m.Content) != "" {
				blocks = append(blocks, contentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Args)
				blocks = append(blocks, contentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: args})
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, message{Role: "assistant", Content: blocks})
		case llm.RoleTool:
			block := contentBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			if n := len(out); n > 0 && out[n-1].Role == "user" && isToolResultOnly(out[n-1]) {
				out[n-1].Content = append(out[n-1].Content, block)
				continue
			}
			out = append(out, message{Role: "user", Content: []contentBlock{block}})
		}
	}
	return out
}

func isToolResultOnly(m message) bool {
	for _, b := range m.Content {
		if b.Type != "tool_result" {
			return false
		}
	}
	return len(m.Content) > 0
}

func (p *Provider) parseResponse(out response) (llm.Response, error) {
	res := llm.Response{
		Model: out.Model,
		Usage: llm.Usage{PromptTokens: out.Usage.InputTokens, CompletionTokens: out.Usage.OutputTokens},
	}
	var text strings.Builder
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args := map[string]any{}
			if len(b.Input) > 0 {
				if err := json.Unmarshal(b.Input, &args); err != nil {
					return llm.Response{}, errs.Wrap(err, "MAS-2004", b.Name, "arguments are not a JSON object")
				}
			}
			res.ToolCalls = append(res.ToolCalls, llm.ToolCall{ID: b.ID, Name: b.Name, Args: args})
		}
	}
	res.Text = text.String()
	switch out.StopReason {
	case "tool_use":
		res.StopReason = llm.StopToolUse
	case "max_tokens":
		res.StopReason = llm.StopMaxToken
	case "refusal":
		res.StopReason = llm.StopRefusal
	default:
		res.StopReason = llm.StopEnd
	}
	return res, nil
}

func apiMessage(raw []byte, status int) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &e); err == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	msg := strings.TrimSpace(string(raw))
	if len(msg) > 200 {
		msg = msg[:197] + "…"
	}
	if msg == "" {
		return "HTTP " + itoa(status)
	}
	return msg
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
