package source

import (
	"context"
	"fmt"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Tools returns the source-domain capabilities.
func Tools(f *Fetcher, kind core.MiddlewareKind, version string) []tool.Tool {
	return []tool.Tool{
		&fetchTool{f: f, kind: kind, version: version},
		&searchTool{f: f, kind: kind, version: version},
	}
}

type fetchTool struct {
	f       *Fetcher
	kind    core.MiddlewareKind
	version string
}

func (t *fetchTool) Name() string { return "source.fetch" }
func (t *fetchTool) Description() string {
	return "Acquire the middleware's source tree for the deployed version, from the configured network repository " +
		"or, when the network is unavailable, from the configured local mirror. Reports which source was used so a " +
		"version mismatch is visible. Call this before source.search."
}
func (t *fetchTool) Domain() tool.Domain  { return tool.DomainSource }
func (t *fetchTool) Safety() safety.Class { return safety.ClassReadOnly }
func (t *fetchTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"version": {Type: tool.TypeString, Description: "Version to fetch; defaults to the target's detected version"},
	})
}
func (t *fetchTool) Plan(args map[string]any) (safety.Call, error) {
	// The declared effect is the git clone this tool will run. Writing into our
	// own cache is not a target-environment mutation, but the command still
	// passes the guard so the binary and its verbs are allow-listed.
	return safety.Call{
		Class:   safety.ClassReadOnly,
		Command: &safety.CommandEffect{Binary: "git", Args: []string{"clone", "--depth", "1"}},
		Timeout: t.f.networkTimeout(),
	}, nil
}
func (t *fetchTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	version := tool.Str(args, "version", t.version)
	got, gap := t.f.Fetch(ctx, t.kind, version)
	if gap != nil && got.Path == "" {
		return core.Evidence{}, errs.New(gap.Code, string(t.kind), gap.Detail)
	}
	summary := fmt.Sprintf("source for %s@%s obtained from %s", t.kind, orDefault(got.Ref, "default branch"), got.Origin)
	if got.Fallback {
		summary += " (network unreachable; the mirror may not match the deployed version)"
	}
	return core.Evidence{
		Kind: core.EvidenceSourceRef, Source: "source",
		Query:   fmt.Sprintf("fetch %s %s", t.kind, version),
		Payload: got, Summary: summary,
	}, nil
}

type searchTool struct {
	f       *Fetcher
	kind    core.MiddlewareKind
	version string
}

func (t *searchTool) Name() string { return "source.search" }
func (t *searchTool) Description() string {
	return "Search the acquired source tree for a regular expression and return matching lines with surrounding " +
		"context. Use to locate the code that emits an observed log line, to read the condition that triggers an " +
		"error, or to confirm a behaviour exists in the deployed version."
}
func (t *searchTool) Domain() tool.Domain  { return tool.DomainSource }
func (t *searchTool) Safety() safety.Class { return safety.ClassReadOnly }
func (t *searchTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"pattern": {Type: tool.TypeString, Description: "RE2 regular expression, e.g. OOM command not allowed"},
		"max_matches": {Type: tool.TypeInteger, Description: "Maximum matches to return",
			Default: 20, Minimum: tool.Float(1), Maximum: tool.Float(200)},
		"case_sensitive": {Type: tool.TypeBoolean, Description: "Match case exactly", Default: false},
	}, "pattern")
}
func (t *searchTool) Plan(args map[string]any) (safety.Call, error) {
	root, err := t.root()
	if err != nil {
		return safety.Call{}, err
	}
	return safety.Call{
		Class: safety.ClassReadOnly,
		File:  &safety.FileEffect{Path: root},
	}, nil
}
func (t *searchTool) root() (string, error) {
	if !t.f.Enabled() {
		return "", errs.New("MAS-4402", string(t.kind), "source acquisition is disabled")
	}
	return t.f.destFor(t.kind, refFor(t.version)), nil
}
func (t *searchTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	root, err := t.root()
	if err != nil {
		return core.Evidence{}, err
	}
	pattern := tool.Str(args, "pattern", "")
	opts := DefaultSearchOptions()
	opts.MaxMatches = tool.Int(args, "max_matches", 20)
	opts.CaseSensitive = tool.Bool(args, "case_sensitive", false)

	matches, err := Search(root, pattern, opts)
	if err != nil {
		return core.Evidence{}, err
	}
	summary := fmt.Sprintf("%q → %d matches in %s source", pattern, len(matches), t.kind)
	if len(matches) > 0 {
		summary += fmt.Sprintf("; first at %s:%d", matches[0].File, matches[0].Line)
	}
	return core.Evidence{
		Kind: core.EvidenceSourceRef, Source: "source",
		Query:     "search " + pattern,
		Payload:   map[string]any{"matches": matches, "count": len(matches), "root": root},
		Summary:   summary,
		Truncated: len(matches) >= opts.MaxMatches,
	}, nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
