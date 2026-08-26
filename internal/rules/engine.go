package rules

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Input parameterises a playbook run.
type Input struct {
	Target   core.Target
	Selector string
	Language string
	MaxSteps int
}

// Output is everything one playbook produced.
type Output struct {
	Playbook     string
	Findings     []core.Finding
	Evidence     []core.Evidence
	Gaps         []core.Gap
	ChecksPassed []string
	Conclusions  []string // failure-mode ids the playbook concluded
	LLMCalls     int      // always 0; asserted by FR-008's test
	Truncated    bool
}

// Engine runs playbooks against the guarded tool layer.
type Engine struct {
	inv *tool.Invoker
	lib *knowledge.Library
}

// New builds an engine.
func New(inv *tool.Invoker, lib *knowledge.Library) *Engine { return &Engine{inv: inv, lib: lib} }

// Select returns the playbooks a symptom activates, most specific first.
func (e *Engine) Select(pack *knowledge.Pack, symptom string) []*knowledge.Playbook {
	if pack == nil {
		return nil
	}
	return pack.MatchingPlaybooks(symptom)
}

// Run executes one playbook. It never calls a model: the whole point of this
// layer is that routine cases are answered deterministically and for free.
func (e *Engine) Run(ctx context.Context, pack *knowledge.Pack, pb *knowledge.Playbook, in Input) (Output, error) {
	out := Output{Playbook: pb.ID}
	lang := in.Language
	if lang == "" {
		lang = "en"
	}
	slots := map[string]any{}
	env := helpers()

	maxSteps := in.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 32
	}

	for i := range pb.Steps {
		st := pb.Steps[i]
		if i >= maxSteps {
			out.Truncated = true
			out.Gaps = append(out.Gaps, core.Gap{
				Intent: "playbook " + pb.ID, Reason: core.GapTruncated,
				Code:   "MAS-5013",
				Detail: errs.New("MAS-5013", pb.ID, maxSteps).Message(lang),
				Impact: "later checks in this playbook did not run",
			})
			break
		}

		switch {
		case st.Collect != nil:
			stop := e.runCollect(ctx, pack, pb, st, in, slots, env, &out)
			if stop {
				return out, nil
			}

		case st.Evaluate != "":
			stop, err := e.runEvaluate(pb, st, lang, env, &out)
			if err != nil {
				return out, err
			}
			if stop {
				return out, nil
			}

		case st.Conclude != nil:
			if err := e.runConclude(pack, pb, st, lang, env, &out); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

func (e *Engine) runCollect(ctx context.Context, pack *knowledge.Pack, pb *knowledge.Playbook,
	st knowledge.Step, in Input, slots map[string]any, env map[string]any, out *Output) bool {

	args, err := pack.ExpandSignals(st.Collect.Args, in.Selector)
	if err != nil {
		out.Gaps = append(out.Gaps, core.Gap{
			Intent: pb.ID + "/" + st.ID, Reason: core.GapUnsupported,
			Code: errs.CodeOf(err), Detail: err.Error(),
			Impact: "this check could not be prepared",
		})
		return false
	}

	ev, gap := e.inv.Invoke(ctx, st.Collect.Tool, args)
	if gap != nil {
		gap.Intent = pb.ID + "/" + st.ID + ": " + gap.Intent
		out.Gaps = append(out.Gaps, *gap)
		// An absent slot makes dependent expressions unevaluable, which is
		// treated as "unknown", not as "false": a missing measurement must never
		// be mistaken for a healthy one.
		slots[st.Collect.As] = nil
		return false
	}
	out.Evidence = append(out.Evidence, ev)
	view := bindEvidence(ev)
	slots[st.Collect.As] = view
	env[st.Collect.As] = view
	return false
}

func (e *Engine) runEvaluate(pb *knowledge.Playbook, st knowledge.Step, lang string,
	env map[string]any, out *Output) (bool, error) {

	missing := missingReferences(st.Evaluate, env)
	if len(missing) > 0 {
		// Dependencies were not collected; skip rather than guess (FR-013).
		out.Gaps = append(out.Gaps, core.Gap{
			Intent: pb.ID + "/" + st.ID, Reason: core.GapUnavailable,
			Code:   "MAS-5015",
			Detail: "skipped: no data for " + strings.Join(missing, ", "),
			Impact: "this check was not performed, so its failure mode is neither confirmed nor ruled out",
		})
		return false, nil
	}

	program, err := expr.Compile(st.Evaluate, expr.Env(env), expr.AsBool())
	if err != nil {
		return false, errs.Wrap(err, "MAS-5010", pb.ID, st.ID, err.Error())
	}
	raw, err := vm.Run(program, env)
	if err != nil {
		return false, errs.Wrap(err, "MAS-5011", pb.ID, st.ID, err.Error())
	}
	held, ok := raw.(bool)
	if !ok {
		return false, errs.New("MAS-5011", pb.ID, st.ID, fmt.Sprintf("evaluated to %T", raw))
	}

	branch := st.OnFalse
	if held {
		branch = st.OnTrue
	}
	if !held {
		// A metric that returned no series compares as zero, which reads as
		// healthy. When such a slot took part in an expression that came out
		// false, the check did not run: reporting its false branch would state
		// as ruled out something that was never measured (Constitution Art. V,
		// FR-013). Emptiness that the author read deliberately — `up.empty or
		// up.latest < 1` — comes out true and is unaffected.
		if unmeasured := emptyMetricSlots(st.Evaluate, env); len(unmeasured) > 0 {
			out.Gaps = append(out.Gaps, core.Gap{
				Intent: pb.ID + "/" + st.ID, Reason: core.GapUnavailable,
				Code:   "MAS-5015",
				Detail: "skipped: no series returned for " + strings.Join(unmeasured, ", "),
				Impact: "this check was not performed, so its failure mode is neither confirmed nor ruled out",
			})
			return false, nil
		}
	}
	if branch == nil {
		return false, nil
	}
	if branch.Finding != nil {
		out.Findings = append(out.Findings, core.Finding{
			ID:         "",
			Origin:     "rule:" + pb.ID + "/" + st.ID,
			Severity:   core.Severity(orDefault(branch.Finding.Severity, string(core.SeverityMajor))),
			Statement:  branch.Finding.Statement.In(lang),
			Detail:     branch.Finding.Detail.In(lang),
			Evidence:   evidenceIDs(out.Evidence),
			Confidence: branch.Finding.Confidence,
		})
	}
	if s := branch.Pass.In(lang); s != "" {
		out.ChecksPassed = append(out.ChecksPassed, s)
	}
	return branch.Stop, nil
}

func (e *Engine) runConclude(pack *knowledge.Pack, pb *knowledge.Playbook, st knowledge.Step,
	lang string, env map[string]any, out *Output) error {

	if st.Conclude.When != "" {
		if len(missingReferences(st.Conclude.When, env)) > 0 {
			return nil
		}
		program, err := expr.Compile(st.Conclude.When, expr.Env(env), expr.AsBool())
		if err != nil {
			return errs.Wrap(err, "MAS-5010", pb.ID, st.ID, err.Error())
		}
		raw, err := vm.Run(program, env)
		if err != nil {
			return errs.Wrap(err, "MAS-5011", pb.ID, st.ID, err.Error())
		}
		if held, _ := raw.(bool); !held {
			return nil
		}
	}
	if _, ok := pack.FailureMode(st.Conclude.FailureMode); !ok {
		return errs.New("MAS-5014", pb.ID, st.ID, "unknown failure mode "+st.Conclude.FailureMode)
	}
	out.Conclusions = append(out.Conclusions, st.Conclude.FailureMode)
	return nil
}

// missingReferences reports which identifiers an expression needs that the
// environment does not hold, so a skipped collection skips its checks instead of
// failing the playbook.
// emptyMetricSlots names the metric slots an expression reads that collected no
// series. Log slots are excluded on purpose: a log query returning nothing is a
// real observation ("nothing was logged in the window"), while a metric query
// returning nothing means the signal does not exist in this deployment.
func emptyMetricSlots(expression string, env map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	for _, ident := range identifiers(expression) {
		if seen[ident] || isBuiltin(ident) {
			continue
		}
		if view, ok := env[ident].(MetricView); ok && view.Empty {
			seen[ident] = true
			out = append(out, ident)
		}
	}
	sort.Strings(out)
	return out
}

func missingReferences(expression string, env map[string]any) []string {
	var missing []string
	seen := map[string]bool{}
	for _, ident := range identifiers(expression) {
		v, present := env[ident]
		if !present || v == nil {
			if !seen[ident] && !isBuiltin(ident) {
				missing = append(missing, ident)
				seen[ident] = true
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// identifiers extracts the leading identifier of each dotted path in an
// expression. It is intentionally simple: it only needs to find slot names.
//
// Quoted string literals are skipped. Playbooks pass regular expressions as
// literals — countMatching(logs.lines, 'NotEnoughBookies|Not enough bookies') —
// and the bare words inside them are not slot references. Reading them as
// references made every log-pattern check look like it depended on slots that
// were never collected, so the engine skipped it and reported a gap instead of
// running it.
// identifiers and isBuiltin delegate to the one scanner in internal/core.
// There is deliberately no copy here: see the note in internal/core/expr.go for
// the defect a second copy reintroduces.
func identifiers(s string) []string { return core.Identifiers(s) }

func isBuiltin(ident string) bool { return core.IsExprBuiltin(ident) }

func evidenceIDs(evs []core.Evidence) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		if e.ID != "" {
			out = append(out, e.ID)
		}
	}
	return out
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// RunAll runs every playbook a symptom activates and merges the results,
// stamping finding ids and recording how long the deterministic phase took.
func (e *Engine) RunAll(ctx context.Context, pack *knowledge.Pack, symptom string, in Input) Output {
	started := time.Now()
	merged := Output{Playbook: "deterministic-phase"}
	if pack == nil {
		return merged
	}
	seq := 0
	for _, pb := range e.Select(pack, symptom) {
		out, err := e.Run(ctx, pack, pb, in)
		if err != nil {
			obs.Log(ctx).Warn("playbook failed",
				"playbook", pb.ID, "code", errs.CodeOf(err), "error", err.Error())
			merged.Gaps = append(merged.Gaps, core.Gap{
				Intent: "playbook " + pb.ID, Reason: core.GapUnsupported,
				Code: errs.CodeOf(err), Detail: err.Error(),
				Impact: "this deterministic check could not run; the agents may still investigate it",
			})
			continue
		}
		for i := range out.Findings {
			seq++
			out.Findings[i].ID = fmt.Sprintf("f-%d", seq)
		}
		merged.Findings = append(merged.Findings, out.Findings...)
		merged.Evidence = append(merged.Evidence, out.Evidence...)
		merged.Gaps = append(merged.Gaps, out.Gaps...)
		merged.ChecksPassed = append(merged.ChecksPassed, out.ChecksPassed...)
		merged.Conclusions = append(merged.Conclusions, out.Conclusions...)
		merged.Truncated = merged.Truncated || out.Truncated
	}
	obs.Log(ctx).Info("deterministic phase complete",
		"findings", len(merged.Findings), "evidence", len(merged.Evidence),
		"gaps", len(merged.Gaps), "llm_calls", 0, "duration_ms", time.Since(started).Milliseconds())
	return merged
}

// TopConfidence returns the highest confidence among findings, which the service
// compares against the short-circuit threshold.
func (o Output) TopConfidence() float64 {
	best := 0.0
	for _, f := range o.Findings {
		if f.Confidence > best {
			best = f.Confidence
		}
	}
	return best
}
