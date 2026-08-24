// Package openai implements llm.Provider against the OpenAI Chat Completions
// wire format. Because base_url is configurable, the same code serves OpenAI,
// DeepSeek, Qwen, vLLM, Ollama and any other OpenAI-compatible endpoint —
// which is most of the self-hosted ecosystem.
//
// Governs: specs/001-mvp-core/design-lld.md §2.13
package openai

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

// DefaultBaseURL is the public OpenAI endpoint.
const DefaultBaseURL = "https://api.openai.com/v1"

func init() {
	llm.Register("openai", func(cfg config.LLMConfig) (llm.Provider, error) { return New(cfg, nil) })
}

// Provider talks to an OpenAI-compatible Chat Completions endpoint.
type Provider struct {
	baseURL   string
	apiKey    config.Secret
	model     string
	maxTokens int
	hc        *http.Client
}

// New builds a provider.
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
	return &Provider{baseURL: base, apiKey: cfg.APIKey, model: cfg.Model, maxTokens: cfg.MaxTokens, hc: hc}, nil
}

// Name identifies the provider.
func (p *Provider) Name() string { return "openai" }

// Close is a no-op.
func (p *Provider) Close() error { return nil }

type function struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
	Arguments   string `json:"arguments,omitempty"`
}

type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function function `json:"function"`
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type toolSpec struct {
	Type     string   `json:"type"`
	Function function `json:"function"`
}

type request struct {
	Model       string     `json:"model"`
	Messages    []message  `json:"messages"`
	Tools       []toolSpec `json:"tools,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
}

type response struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete sends one completion request.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	body, err := json.Marshal(p.buildRequest(req))
	if err != nil {
		return llm.Response{}, errs.Wrap(err, "MAS-2008", p.Name(), err.Error())
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return llm.Response{}, errs.Wrap(err, "MAS-2001", p.Name())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if !p.apiKey.IsZero() {
		key, kerr := p.apiKey.Reveal()
		if kerr != nil {
			return llm.Response{}, kerr
		}
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := p.hc.Do(httpReq)
	if err != nil {
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
	if len(out.Choices) == 0 {
		return llm.Response{}, errs.New("MAS-2008", p.Name(), "the response contained no choices")
	}

	choice := out.Choices[0]
	res := llm.Response{
		Text: choice.Message.Content, Model: out.Model,
		Usage: llm.Usage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens},
	}
	for _, tc := range choice.Message.ToolCalls {
		args := map[string]any{}
		if strings.TrimSpace(tc.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return llm.Response{}, errs.Wrap(err, "MAS-2004", tc.Function.Name, "arguments are not a JSON object")
			}
		}
		res.ToolCalls = append(res.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: args})
	}
	switch choice.FinishReason {
	case "tool_calls", "function_call":
		res.StopReason = llm.StopToolUse
	case "length":
		res.StopReason = llm.StopMaxToken
	case "content_filter":
		res.StopReason = llm.StopRefusal
	default:
		res.StopReason = llm.StopEnd
	}
	if len(res.ToolCalls) > 0 && res.StopReason == llm.StopEnd {
		// Some compatible servers omit the tool_calls finish reason.
		res.StopReason = llm.StopToolUse
	}
	return res, nil
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
	out := request{Model: model, MaxTokens: maxTokens}
	if req.Temperature > 0 {
		t := req.Temperature
		out.Temperature = &t
	}
	if strings.TrimSpace(req.System) != "" {
		out.Messages = append(out.Messages, message{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleSystem:
			out.Messages = append(out.Messages, message{Role: "system", Content: m.Content})
		case llm.RoleUser:
			out.Messages = append(out.Messages, message{Role: "user", Content: m.Content})
		case llm.RoleAssistant:
			am := message{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Args)
				am.ToolCalls = append(am.ToolCalls, toolCall{
					ID: tc.ID, Type: "function",
					Function: function{Name: tc.Name, Arguments: string(args)},
				})
			}
			out.Messages = append(out.Messages, am)
		case llm.RoleTool:
			out.Messages = append(out.Messages, message{
				Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID, Name: m.ToolName,
			})
		}
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, toolSpec{
			Type:     "function",
			Function: function{Name: t.Name, Description: t.Description, Parameters: t.Schema},
		})
	}
	return out
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
