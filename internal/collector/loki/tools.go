package loki

import (
	"context"
	"fmt"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Tools returns the log-domain capabilities backed by this client.
func Tools(c *Client) []tool.Tool {
	return []tool.Tool{&queryTool{c: c}, &labelsTool{c: c}}
}

type queryTool struct{ c *Client }

func (t *queryTool) Name() string { return "loki.query" }
func (t *queryTool) Description() string {
	return "Run a LogQL query over the run's time window and return matching log lines, newest first. " +
		"Use to find error messages, restart notices, election events or slow-operation warnings. " +
		"Example: {job=\"redis\"} |= \"OOM\"."
}
func (t *queryTool) Domain() tool.Domain  { return tool.DomainLogs }
func (t *queryTool) Safety() safety.Class { return safety.ClassReadOnly }
func (t *queryTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"query": {Type: tool.TypeString, Description: "LogQL query, e.g. {job=\"redis\"} |= \"error\""},
		"limit": {Type: tool.TypeInteger, Description: "Maximum lines to return",
			Default: 200, Minimum: tool.Float(1), Maximum: tool.Float(5000)},
		"direction": {Type: tool.TypeString, Description: "backward (newest first) or forward",
			Enum: []string{"backward", "forward"}, Default: "backward"},
		"from": {Type: tool.TypeString, Description: "RFC3339 start; defaults to the run's window start"},
		"to":   {Type: tool.TypeString, Description: "RFC3339 end; defaults to the run's window end"},
	}, "query")
}
func (t *queryTool) Plan(map[string]any) (safety.Call, error) {
	return safety.Call{
		Class:   safety.ClassReadOnly,
		HTTP:    &safety.HTTPEffect{Method: "GET", URL: t.c.URLFor("/loki/api/v1/query_range")},
		Timeout: t.c.Timeout(),
	}, nil
}
func (t *queryTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	query := tool.Str(args, "query", "")
	w, err := windowFrom(ctx, args)
	if err != nil {
		return core.Evidence{}, err
	}
	res, err := t.c.Query(ctx, query, w, tool.Int(args, "limit", 200), Direction(tool.Str(args, "direction", "backward")))
	if err != nil {
		return core.Evidence{}, err
	}
	return core.Evidence{
		Kind: core.EvidenceLogLines, Source: "loki:" + t.c.Name(), Query: query,
		Payload: res, Summary: res.Summary(), Truncated: res.Truncated,
	}, nil
}

type labelsTool struct{ c *Client }

func (t *labelsTool) Name() string { return "loki.labels" }
func (t *labelsTool) Description() string {
	return "List log label names, or the values of one label, within the run's window. " +
		"Use to discover how logs are labelled before writing a LogQL query, or to confirm a target is shipping logs at all."
}
func (t *labelsTool) Domain() tool.Domain  { return tool.DomainLogs }
func (t *labelsTool) Safety() safety.Class { return safety.ClassReadOnly }
func (t *labelsTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"label": {Type: tool.TypeString, Description: "Label name whose values to list; omit to list label names"},
		"from":  {Type: tool.TypeString, Description: "RFC3339 start; defaults to the run's window start"},
		"to":    {Type: tool.TypeString, Description: "RFC3339 end; defaults to the run's window end"},
	})
}
func (t *labelsTool) Plan(args map[string]any) (safety.Call, error) {
	path := "/loki/api/v1/labels"
	if l := tool.Str(args, "label", ""); l != "" {
		path = "/loki/api/v1/label/" + l + "/values"
	}
	return safety.Call{
		Class:   safety.ClassReadOnly,
		HTTP:    &safety.HTTPEffect{Method: "GET", URL: t.c.URLFor(path)},
		Timeout: t.c.Timeout(),
	}, nil
}
func (t *labelsTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	w, err := windowFrom(ctx, args)
	if err != nil {
		return core.Evidence{}, err
	}
	label := tool.Str(args, "label", "")
	var values []string
	if label == "" {
		values, err = t.c.Labels(ctx, w)
	} else {
		values, err = t.c.LabelValues(ctx, label, w)
	}
	if err != nil {
		return core.Evidence{}, err
	}
	what := "label names"
	if label != "" {
		what = "values of label " + label
	}
	return core.Evidence{
		Kind: core.EvidenceLogLines, Source: "loki:" + t.c.Name(), Query: what,
		Payload: map[string]any{"values": values, "count": len(values)},
		Summary: fmt.Sprintf("%d %s: %s", len(values), what, joinCapped(values, 12)),
	}, nil
}

func joinCapped(vals []string, n int) string {
	if len(vals) <= n {
		return sliceJoin(vals)
	}
	return sliceJoin(vals[:n]) + fmt.Sprintf(", … %d more", len(vals)-n)
}

func sliceJoin(v []string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// windowKey mirrors the metrics collector: the run's window travels in the
// context so a model need not restate it and cannot widen it.
type windowKey struct{}

// WithWindow attaches the run's window.
func WithWindow(ctx context.Context, w core.Window) context.Context {
	return context.WithValue(ctx, windowKey{}, w)
}

// WindowFromContext reads the attached window.
func WindowFromContext(ctx context.Context) (core.Window, bool) {
	w, ok := ctx.Value(windowKey{}).(core.Window)
	return w, ok
}

func windowFrom(ctx context.Context, args map[string]any) (core.Window, error) {
	w, ok := WindowFromContext(ctx)
	if !ok {
		now := time.Now().UTC()
		w = core.Window{From: now.Add(-time.Hour), To: now}
	}
	if s := tool.Str(args, "from", ""); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return w, errs.New("MAS-1010", "from must be RFC3339, got "+s)
		}
		w.From = t
	}
	if s := tool.Str(args, "to", ""); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return w, errs.New("MAS-1010", "to must be RFC3339, got "+s)
		}
		w.To = t
	}
	if err := w.Validate(); err != nil {
		return w, err
	}
	return w, nil
}
