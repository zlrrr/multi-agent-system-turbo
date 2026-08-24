package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// maxRepairAttempts bounds how often a malformed tool call is re-prompted before
// the loop gives up on it. Two attempts is enough for a transient formatting
// slip and short enough that a weak model cannot burn the budget (RSK-002).
const maxRepairAttempts = 2

// loopOptions parameterises a reasoning loop.
type loopOptions struct {
	role       Role
	label      string // role as it appears in prompts, e.g. "investigator (metrics)"
	system     string
	user       string
	toolNames  []string
	maxTurns   int
	jsonResult bool
}

// runLoop is the shared reasoning loop: complete, invoke any requested tools,
// feed the results back, repeat until the model answers or a budget is spent.
//
// It never returns an error for a model or tool failure. A failure becomes a gap
// and the loop returns what it has, because a partial analysis with its holes
// marked is worth more in an incident than no analysis at all.
func runLoop(ctx context.Context, s *State, opts loopOptions) (string, error) {
	if opts.maxTurns <= 0 {
		opts.maxTurns = 6
	}
	messages := []llm.Message{{Role: llm.RoleUser, Content: opts.user}}
	defs := llm.Definitions(s.Tools.Registry().DefinitionsFor(opts.toolNames))

	var lastText string
	repairs := 0

	for turn := 0; turn < opts.maxTurns; turn++ {
		if !s.ConsumeStep() {
			_, reason := s.Truncated()
			s.AddGap(core.Gap{
				Intent: string(opts.role) + " reasoning", Reason: core.GapTruncated,
				Code: "MAS-3005", Detail: reason,
				Impact: "the analysis stopped early; conclusions reflect only what had been gathered",
			})
			return lastText, nil
		}

		started := time.Now()
		resp, err := s.Provider.Complete(ctx, llm.Request{
			Model:       llm.ModelFor(s.LLMConfig, string(opts.role)),
			Temperature: llm.TemperatureFor(s.LLMConfig, string(opts.role)),
			System:      opts.system,
			Messages:    messages,
			Tools:       defs,
		})
		s.AddUsage(core.Usage{
			LLMCalls: 1, PromptTokens: resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens, CostUSD: resp.Usage.CostUSD,
		})
		recordLLMStep(ctx, s, opts, resp, err, time.Since(started))

		if err != nil {
			s.AddGap(core.Gap{
				Intent: string(opts.role) + " reasoning", Reason: core.GapUnavailable,
				Code: errs.CodeOf(err), Detail: err.Error(),
				Impact: "this role could not contribute; deterministic findings still stand",
			})
			return lastText, nil
		}
		if resp.StopReason == llm.StopRefusal {
			s.AddGap(core.Gap{
				Intent: string(opts.role) + " reasoning", Reason: core.GapUnsupported,
				Code: "MAS-2006", Detail: resp.Text,
				Impact: "this role declined to answer",
			})
			return lastText, nil
		}

		if strings.TrimSpace(resp.Text) != "" {
			lastText = resp.Text
		}
		if len(resp.ToolCalls) == 0 {
			return lastText, nil
		}

		messages = append(messages, llm.Message{
			Role: llm.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls,
		})

		anyUsable := false
		for _, call := range resp.ToolCalls {
			ev, gap := s.Tools.Invoke(ctx, call.Name, call.Args)
			if gap != nil {
				s.AddGap(*gap)
				messages = append(messages, llm.Message{
					Role: llm.RoleTool, ToolCallID: call.ID, ToolName: call.Name,
					Content: fmt.Sprintf("This call did not return data (%s: %s). %s",
						gap.Reason, gap.Code, gap.Impact),
				})
				if gap.Code == "MAS-8005" || gap.Code == "MAS-8006" {
					repairs++
				}
				continue
			}
			anyUsable = true
			s.AddEvidence(ev)
			messages = append(messages, llm.Message{
				Role: llm.RoleTool, ToolCallID: call.ID, ToolName: call.Name,
				Content: renderEvidence(ev),
			})
		}

		if !anyUsable && repairs > maxRepairAttempts {
			s.AddGap(core.Gap{
				Intent: string(opts.role) + " tool use", Reason: core.GapUnsupported,
				Code: "MAS-2004", Detail: "the model could not produce a usable tool call after repair attempts",
				Impact: "this role gathered no further evidence",
			})
			return lastText, nil
		}
	}

	s.AddGap(core.Gap{
		Intent: string(opts.role) + " reasoning", Reason: core.GapTruncated,
		Code: "MAS-3010", Detail: fmt.Sprintf("stopped after %d turns without a final answer", opts.maxTurns),
		Impact: "this role's contribution is incomplete",
	})
	return lastText, nil
}

// renderEvidence gives the model a compact, faithful view of a result: the
// summary always, and a bounded slice of the payload so it can reason about
// actual numbers without the whole series entering the context.
func renderEvidence(ev core.Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "evidence %s (%s): %s\n", ev.ID, ev.Kind, ev.Summary)
	if ev.Truncated {
		b.WriteString("(this result was truncated at the configured ceiling)\n")
	}
	if raw, err := json.Marshal(ev.Payload); err == nil {
		const cap = 4000
		s := string(raw)
		if len(s) > cap {
			s = s[:cap] + " …(payload truncated for context)"
		}
		b.WriteString(s)
	}
	return b.String()
}

func recordLLMStep(ctx context.Context, s *State, opts loopOptions, resp llm.Response, err error, d time.Duration) {
	obs.MetricsOf(ctx).Observe("mas_llm_duration_seconds", d.Seconds(),
		map[string]string{"provider": s.Provider.Name(), "role": string(opts.role)})
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	obs.MetricsOf(ctx).IncCounter("mas_llm_calls_total",
		map[string]string{"provider": s.Provider.Name(), "outcome": outcome})
	obs.MetricsOf(ctx).AddCounter("mas_llm_tokens_total", float64(resp.Usage.PromptTokens),
		map[string]string{"provider": s.Provider.Name(), "direction": "prompt"})
	obs.MetricsOf(ctx).AddCounter("mas_llm_tokens_total", float64(resp.Usage.CompletionTokens),
		map[string]string{"provider": s.Provider.Name(), "direction": "completion"})

	step := core.Step{
		Kind: core.StepLLMCall, At: time.Now().UTC(), DurationMillis: d.Milliseconds(),
		Actor: string(opts.role), Name: s.Provider.Name(),
		Output: map[string]any{
			"text":        truncate(resp.Text, 2000),
			"tool_calls":  toolCallNames(resp.ToolCalls),
			"stop_reason": string(resp.StopReason),
			"tokens":      resp.Usage.PromptTokens + resp.Usage.CompletionTokens,
		},
	}
	if err != nil {
		step.Code, step.Err = errs.CodeOf(err), err.Error()
	}
	if s.Sink != nil {
		s.Sink.AppendStep(ctx, step)
		return
	}
	if s.Run == nil {
		return
	}
	s.mu.Lock()
	step.ID = fmt.Sprintf("llm-%d", len(s.Run.Steps)+1)
	s.Run.Steps = append(s.Run.Steps, step)
	s.mu.Unlock()
}

func toolCallNames(calls []llm.ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Name)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// extractJSON pulls the first complete JSON object out of a model's reply.
// Models routinely wrap structured output in prose or a fenced block, and
// refusing to parse that would throw away a good answer over presentation.
func extractJSON(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if fenced := betweenFence(s); fenced != "" {
		s = fenced
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return "", false
	}
	open := s[start]
	closer := byte('}')
	if open == '[' {
		closer = ']'
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// content
		case c == open:
			depth++
		case c == closer:
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func betweenFence(s string) string {
	i := strings.Index(s, "```")
	if i < 0 {
		return ""
	}
	rest := s[i+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	}
	if j := strings.Index(rest, "```"); j >= 0 {
		return strings.TrimSpace(rest[:j])
	}
	return strings.TrimSpace(rest)
}

// decodeJSON parses a model's structured reply, recording a gap rather than
// failing when the model did not produce parseable output.
func decodeJSON(s *State, role Role, text string, into any) bool {
	raw, ok := extractJSON(text)
	if !ok {
		s.AddGap(core.Gap{
			Intent: string(role) + " structured output", Reason: core.GapUnsupported,
			Code: "MAS-2004", Detail: "the reply contained no JSON object",
			Impact: "this role's structured contribution was not usable",
		})
		return false
	}
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		s.AddGap(core.Gap{
			Intent: string(role) + " structured output", Reason: core.GapUnsupported,
			Code: "MAS-2004", Detail: err.Error(),
			Impact: "this role's structured contribution was not usable",
		})
		return false
	}
	return true
}

var _ = context.Background
