package tool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Domain groups tools by the kind of evidence they produce, so a topology can
// hand each specialised investigator only the tools it needs.
type Domain string

const (
	DomainMetrics Domain = "metrics"
	DomainLogs    Domain = "logs"
	DomainCluster Domain = "cluster"
	DomainHost    Domain = "host"
	DomainSource  Domain = "source"
	DomainRules   Domain = "rules"
)

// Tool is a capability. Implementations must perform no I/O outside Invoke, and
// Invoke is only ever reached through Invoker, which authorises first.
type Tool interface {
	Name() string
	Description() string
	Domain() Domain
	ArgsSchema() Schema
	Safety() safety.Class

	// Plan declares the effect Invoke will have, so the guard can rule on it
	// before anything happens. Plan must not perform I/O.
	Plan(args map[string]any) (safety.Call, error)

	// Invoke performs the read and wraps the result as evidence.
	Invoke(ctx context.Context, args map[string]any) (core.Evidence, error)
}

// RequiresMode lets a tool declare that it may only run in online mode. Tools
// that read the live environment implement it; telemetry tools do not.
type RequiresMode interface {
	RequiredMode() core.Mode
}

// Registry holds the tools available to a run.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds a tool. Duplicate names are a programming error.
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if name == "" {
		return errs.New("MAS-9001", "tool registered with an empty name")
	}
	if _, exists := r.tools[name]; exists {
		return errs.New("MAS-9001", "duplicate tool registration: "+name)
	}
	if t.Safety() != safety.ClassReadOnly {
		return errs.New("MAS-8001", "tool "+name+" declares itself mutating")
	}
	r.tools[name] = t
	return nil
}

// MustRegister panics on error; for use in package initialisation.
func (r *Registry) MustRegister(ts ...Tool) {
	for _, t := range ts {
		if err := r.Register(t); err != nil {
			panic(err)
		}
	}
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns every tool, sorted by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// InDomains returns the tools belonging to any of the given domains.
func (r *Registry) InDomains(domains ...Domain) []Tool {
	want := map[Domain]bool{}
	for _, d := range domains {
		want[d] = true
	}
	var out []Tool
	for _, t := range r.List() {
		if want[t.Domain()] {
			out = append(out, t)
		}
	}
	return out
}

// Names returns the registered tool names, sorted.
func (r *Registry) Names() []string {
	out := []string{}
	for _, t := range r.List() {
		out = append(out, t.Name())
	}
	return out
}

// StepSink receives the append-only record of every invocation. The run store
// implements it; tests use a slice.
type StepSink interface {
	AppendStep(ctx context.Context, step core.Step)
}

// Invoker is the only path to Tool.Invoke. It validates arguments, authorises
// the planned effect, enforces the timeout, records a step, and converts every
// failure into a Gap so that a missing datum degrades the run instead of ending
// it (FR-013).
type Invoker struct {
	registry *Registry
	guard    *safety.Guard
	sink     StepSink
	redactor *safety.Redactor
	mode     core.Mode
	timeout  time.Duration

	seqEvidence atomic.Int64
	seqGap      atomic.Int64
	calls       atomic.Int64
	maxCalls    int64
}

// InvokerOptions configures an Invoker.
type InvokerOptions struct {
	Guard        *safety.Guard
	Sink         StepSink
	Redactor     *safety.Redactor
	Mode         core.Mode
	Timeout      time.Duration
	MaxToolCalls int
}

// NewInvoker builds an invoker. A nil guard is refused: there is no unguarded
// construction path (Art. IV.1).
func NewInvoker(r *Registry, opts InvokerOptions) (*Invoker, error) {
	if r == nil {
		return nil, errs.New("MAS-9001", "invoker requires a registry")
	}
	if opts.Guard == nil {
		return nil, errs.New("MAS-9001", "invoker requires a safety guard")
	}
	if opts.Redactor == nil {
		opts.Redactor = safety.NewRedactor(nil, nil)
	}
	if opts.Mode == "" {
		opts.Mode = core.ModeOffline
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	return &Invoker{
		registry: r, guard: opts.Guard, sink: opts.Sink, redactor: opts.Redactor,
		mode: opts.Mode, timeout: opts.Timeout, maxCalls: int64(opts.MaxToolCalls),
	}, nil
}

// Registry exposes the underlying registry.
func (in *Invoker) Registry() *Registry { return in.registry }

// Mode reports the run mode this invoker enforces.
func (in *Invoker) Mode() core.Mode { return in.mode }

// Calls reports how many invocations have been attempted.
func (in *Invoker) Calls() int { return int(in.calls.Load()) }

// Invoke runs a tool. Exactly one of the returned values is non-nil-ish: on
// success a populated Evidence and a nil Gap; on any failure a zero Evidence and
// a Gap explaining what is missing and why.
func (in *Invoker) Invoke(ctx context.Context, name string, args map[string]any) (core.Evidence, *core.Gap) {
	started := time.Now()
	stepID := "step"
	if rc := obs.FromContext(ctx); rc != nil {
		stepID = rc.NextStepID()
	}

	record := func(out any, code, errText string) {
		if in.sink == nil {
			return
		}
		in.sink.AppendStep(ctx, core.Step{
			ID: stepID, Kind: core.StepToolCall, At: started,
			DurationMillis: time.Since(started).Milliseconds(),
			Actor:          "tool", Name: name,
			Input:  in.redactor.RedactAny(args),
			Output: in.redactor.RedactAny(out),
			Code:   code, Err: in.redactor.Redact(errText),
		})
	}

	t, ok := in.registry.Get(name)
	if !ok {
		return in.gap(ctx, record, name, args, core.GapRefused,
			errs.New("MAS-8006", name), "the requested capability does not exist")
	}

	if in.maxCalls > 0 && in.calls.Load() >= in.maxCalls {
		return in.gap(ctx, record, name, args, core.GapTruncated,
			errs.New("MAS-3007", int(in.maxCalls)), "evidence collection stopped at the configured budget")
	}
	in.calls.Add(1)

	if rm, ok := t.(RequiresMode); ok && rm.RequiredMode() == core.ModeOnline && in.mode != core.ModeOnline {
		return in.gap(ctx, record, name, args, core.GapNotConfigured,
			errs.New("MAS-1011", string(in.mode)),
			"this check reads the live environment and the run is offline; re-run with --mode online")
	}

	validated, err := t.ArgsSchema().Validate(args)
	if err != nil {
		return in.gap(ctx, record, name, args, core.GapRefused, err, "the request was malformed")
	}

	call, err := t.Plan(validated)
	if err != nil {
		return in.gap(ctx, record, name, validated, core.GapRefused, err, "the request could not be planned")
	}
	call.Tool = name
	if call.Class == "" {
		call.Class = t.Safety()
	}
	if call.Timeout == 0 {
		call.Timeout = in.timeout
	}

	if err := in.guard.Authorize(ctx, call); err != nil {
		obs.MetricsOf(ctx).IncCounter("mas_tool_refusals_total",
			map[string]string{"tool": name, "code": errs.CodeOf(err)})
		return in.gap(ctx, record, name, validated, core.GapRefused, err,
			"refused by the read-only safety guard")
	}

	callCtx, cancel := context.WithTimeout(ctx, call.Timeout)
	defer cancel()

	ev, err := t.Invoke(callCtx, validated)
	dur := time.Since(started)
	obs.MetricsOf(ctx).Observe("mas_tool_duration_seconds", dur.Seconds(), map[string]string{"tool": name})

	if err != nil {
		reason := core.GapUnavailable
		if callCtx.Err() != nil {
			err = errs.Wrap(err, "MAS-8010", "tool "+name+" timed out", call.Timeout.String())
			reason = core.GapUnavailable
		}
		obs.MetricsOf(ctx).IncCounter("mas_tool_calls_total", map[string]string{"tool": name, "outcome": "error"})
		return in.gap(ctx, record, name, validated, reason, err, "this evidence is missing from the analysis")
	}

	if ev.ID == "" {
		ev.ID = fmt.Sprintf("ev-%d", in.seqEvidence.Add(1))
	}
	if ev.CollectedAt.IsZero() {
		ev.CollectedAt = time.Now().UTC()
	}
	ev.Query = in.redactor.Redact(ev.Query)
	ev.Summary = in.redactor.Redact(ev.Summary)
	ev.Payload = in.redactor.RedactAny(ev.Payload)
	ev.ComputeDigest()

	obs.MetricsOf(ctx).IncCounter("mas_tool_calls_total", map[string]string{"tool": name, "outcome": "ok"})
	record(map[string]any{"evidence_id": ev.ID, "summary": ev.Summary, "truncated": ev.Truncated}, "", "")
	obs.Log(ctx).Debug("evidence collected",
		"tool", name, "evidence_id", ev.ID, "duration_ms", dur.Milliseconds(), "truncated", ev.Truncated)
	return ev, nil
}

func (in *Invoker) gap(ctx context.Context, record func(any, string, string), name string,
	args map[string]any, reason core.GapReason, err error, impact string) (core.Evidence, *core.Gap) {
	code := errs.CodeOf(err)
	gap := &core.Gap{
		ID:     fmt.Sprintf("gap-%d", in.seqGap.Add(1)),
		Intent: describeIntent(name, args),
		Reason: reason,
		Code:   code,
		Detail: in.redactor.Redact(errStr(err)),
		Impact: impact,
	}
	record(nil, code, errStr(err))
	obs.MetricsOf(ctx).IncCounter("mas_gaps_total", map[string]string{"reason": string(reason)})
	obs.Log(ctx).Warn("evidence not collected",
		"tool", name, "reason", string(reason), "code", code, "detail", gap.Detail)
	return core.Evidence{}, gap
}

func describeIntent(name string, args map[string]any) string {
	if len(args) == 0 {
		return name
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, args[k]))
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Definition is the model-facing description of a tool.
type Definition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Schema      Schema `json:"schema"`
}

// Definitions renders the registry for a model provider.
func (r *Registry) Definitions() []Definition {
	out := []Definition{}
	for _, t := range r.List() {
		out = append(out, Definition{Name: t.Name(), Description: t.Description(), Schema: t.ArgsSchema()})
	}
	return out
}

// DefinitionsFor renders only the named tools.
func (r *Registry) DefinitionsFor(names []string) []Definition {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := []Definition{}
	for _, t := range r.List() {
		if want[t.Name()] {
			out = append(out, Definition{Name: t.Name(), Description: t.Description(), Schema: t.ArgsSchema()})
		}
	}
	return out
}
