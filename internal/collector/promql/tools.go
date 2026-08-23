package promql

import (
	"context"
	"fmt"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
)

// Tools returns the metric-domain capabilities backed by this client.
func Tools(c *Client) []tool.Tool {
	return []tool.Tool{
		&instantTool{c: c},
		&rangeTool{c: c},
		&seriesTool{c: c},
	}
}

// base carries the parts every metrics tool shares, including the effect it
// declares to the guard.
type base struct{ c *Client }

func (b base) Domain() tool.Domain  { return tool.DomainMetrics }
func (b base) Safety() safety.Class { return safety.ClassReadOnly }
func (b base) plan(path string) safety.Call {
	return safety.Call{
		Class:   safety.ClassReadOnly,
		HTTP:    &safety.HTTPEffect{Method: "POST", URL: b.c.URLFor(path)},
		Timeout: b.c.Timeout(),
	}
}

type instantTool struct {
	base
	c *Client
}

func (t *instantTool) Name() string { return "promql.instant" }
func (t *instantTool) Description() string {
	return "Evaluate a PromQL expression at a single instant. Use for current values such as memory usage, " +
		"connection counts or replication offsets. Returns the value per series with no history."
}
func (t *instantTool) Domain() tool.Domain  { return tool.DomainMetrics }
func (t *instantTool) Safety() safety.Class { return safety.ClassReadOnly }
func (t *instantTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"query": {Type: tool.TypeString, Description: "PromQL expression, e.g. redis_memory_used_bytes{instance=\"redis-0\"}"},
		"at":    {Type: tool.TypeString, Description: "RFC3339 evaluation instant; defaults to the end of the run's window"},
	}, "query")
}
func (t *instantTool) Plan(map[string]any) (safety.Call, error) {
	return base{c: t.c}.plan("/api/v1/query"), nil
}
func (t *instantTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	query := tool.Str(args, "query", "")
	at, err := parseInstant(tool.Str(args, "at", ""))
	if err != nil {
		return core.Evidence{}, err
	}
	res, err := t.c.Instant(ctx, query, at)
	if err != nil {
		return core.Evidence{}, err
	}
	return evidenceFrom(t.c, query, res), nil
}

type rangeTool struct {
	base
	c *Client
}

func (t *rangeTool) Name() string { return "promql.range" }
func (t *rangeTool) Description() string {
	return "Evaluate a PromQL expression over a time range. Use to see how a signal moved: when a spike began, " +
		"whether growth is gradual or a step change, whether a symptom correlates with a deploy. " +
		"The step is chosen automatically to stay within the source's sample limit."
}
func (t *rangeTool) Domain() tool.Domain  { return tool.DomainMetrics }
func (t *rangeTool) Safety() safety.Class { return safety.ClassReadOnly }
func (t *rangeTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"query": {Type: tool.TypeString, Description: "PromQL expression"},
		"from":  {Type: tool.TypeString, Description: "RFC3339 start; defaults to the run's window start"},
		"to":    {Type: tool.TypeString, Description: "RFC3339 end; defaults to the run's window end"},
		"step":  {Type: tool.TypeString, Description: "Go duration such as 30s or 5m; chosen automatically when omitted"},
	}, "query")
}
func (t *rangeTool) Plan(map[string]any) (safety.Call, error) {
	return base{c: t.c}.plan("/api/v1/query_range"), nil
}
func (t *rangeTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	query := tool.Str(args, "query", "")
	w, err := windowFrom(ctx, args)
	if err != nil {
		return core.Evidence{}, err
	}
	var step time.Duration
	if s := tool.Str(args, "step", ""); s != "" {
		if step, err = time.ParseDuration(s); err != nil {
			return core.Evidence{}, errsInvalidStep(s)
		}
	}
	res, err := t.c.Range(ctx, query, w, step)
	if err != nil {
		return core.Evidence{}, err
	}
	return evidenceFrom(t.c, query, res), nil
}

type seriesTool struct {
	base
	c *Client
}

func (t *seriesTool) Name() string { return "promql.series" }
func (t *seriesTool) Description() string {
	return "List the label sets that exist for a metric selector. Use to discover which instances, pods or topics " +
		"actually report a metric before querying it, or to confirm a target is exporting metrics at all."
}
func (t *seriesTool) Domain() tool.Domain  { return tool.DomainMetrics }
func (t *seriesTool) Safety() safety.Class { return safety.ClassReadOnly }
func (t *seriesTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"match": {Type: tool.TypeArray, Description: "Series selectors, e.g. [\"redis_up\"]",
			Items: &tool.Property{Type: tool.TypeString}},
		"from": {Type: tool.TypeString, Description: "RFC3339 start; defaults to the run's window start"},
		"to":   {Type: tool.TypeString, Description: "RFC3339 end; defaults to the run's window end"},
	}, "match")
}
func (t *seriesTool) Plan(map[string]any) (safety.Call, error) {
	return base{c: t.c}.plan("/api/v1/series"), nil
}
func (t *seriesTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	matchers := tool.Strings(args, "match")
	w, err := windowFrom(ctx, args)
	if err != nil {
		return core.Evidence{}, err
	}
	sets, err := t.c.Series(ctx, matchers, w)
	if err != nil {
		return core.Evidence{}, err
	}
	return core.Evidence{
		Kind: core.EvidenceMetricSeries, Source: "promql",
		Query:   fmt.Sprintf("series%v", matchers),
		Payload: map[string]any{"label_sets": sets, "count": len(sets)},
		Summary: fmt.Sprintf("%d label sets match %v", len(sets), matchers),
	}, nil
}

func evidenceFrom(c *Client, query string, res Result) core.Evidence {
	return core.Evidence{
		Kind:      core.EvidenceMetricSeries,
		Source:    "promql:" + c.Name(),
		Query:     query,
		Payload:   res,
		Summary:   res.Summary(),
		Truncated: res.Truncated,
	}
}
