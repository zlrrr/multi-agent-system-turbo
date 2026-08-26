package knowledge

import (
	"sort"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Resolve returns the pack as it applies to one deployed version, together with
// the gaps for everything it dropped.
//
// It is called once per run, at admission, and what it returns is the pack the
// rest of the run sees. That is deliberate: filtering at each lookup would make
// version scoping something five call sites have to remember, and a caller that
// forgot would silently get unscoped behaviour — the class of defect this
// project has now found four times (specs/007-version-scoped-rules/plan.md §1).
//
// Resolution only ever narrows. There is no branch here that adds a rule, and
// in particular none that adds an inspect command, which is the one rule kind
// that reaches a live system (CON-001).
//
// The receiver is never mutated: one *Pack in the library is shared by every
// target of that middleware.
func (p *Pack) Resolve(version string) (*Pack, []core.Gap) {
	out := *p // shallow copy; every slice below is rebuilt
	var gaps []core.Gap
	var skipped []string

	note := func(kind, id string) { skipped = append(skipped, kind+" "+id) }

	// ── 1. rules, with variants resolved ─────────────────────────────────────
	out.Signals, gaps = resolveRules(p.Signals, version, "signal", gaps, note,
		func(s Signal) (string, string) { return s.ID, s.VersionRange })
	out.LogPatterns, gaps = resolveRules(p.LogPatterns, version, "logPattern", gaps, note,
		func(lp LogPattern) (string, string) { return lp.ID, lp.VersionRange })
	out.FailureModes, gaps = resolveRules(p.FailureModes, version, "failureMode", gaps, note,
		func(f FailureMode) (string, string) { return f.ID, f.VersionRange })
	out.Inspect, gaps = resolveRules(p.Inspect, version, "inspect", gaps, note,
		func(in Inspect) (string, string) { return in.ID, in.VersionRange })

	playbooks, gaps := resolveRules(p.Playbooks, version, "playbook", gaps, note,
		func(pb Playbook) (string, string) { return pb.ID, pb.VersionRange })

	// ── 2. steps, following what they depend on ──────────────────────────────
	signals := idsOf(out.Signals, func(s Signal) string { return s.ID })
	modes := idsOf(out.FailureModes, func(f FailureMode) string { return f.ID })

	out.Playbooks = nil
	for _, pb := range playbooks {
		steps, dropped := resolveSteps(pb, version, signals, modes)
		for _, id := range dropped {
			note("step", pb.ID+"/"+id)
		}
		if !canConclude(steps) {
			note("playbook", pb.ID)
			continue
		}
		pb.Steps = steps
		out.Playbooks = append(out.Playbooks, pb)
	}

	// ── 3. accounting, at two volumes (design-hld.md §3.1) ───────────────────
	if len(skipped) > 0 {
		sort.Strings(skipped)
		gaps = append(gaps, core.Gap{
			Intent: "version scoping for " + p.ID(),
			Reason: core.GapNotApplicable,
			Code:   "MAS-5019",
			Detail: errs.New("MAS-5019", len(skipped), describeVersion(version),
				strings.Join(skipped, ", ")).Message("en"),
			Impact: "these checks do not exist for this version and were not run; " +
				"nothing was lost, and they are listed so this is not read as a check that failed",
		})
	}

	out.Metadata.ResolvedFor = version
	return &out, gaps
}

// resolveRules filters one rule kind, resolving variants by version.
//
// A group of one is kept when its range applies. A group of more than one is a
// rename: exactly one declaration may apply to any version, which validation
// has already guaranteed. With no version to choose by there is no default —
// only a choice between metric names, one of which may not exist — so the id is
// dropped and itemised, because the operator can fix it in one line (D-5).
func resolveRules[T any](rules []T, version, kind string, gaps []core.Gap,
	note func(kind, id string), key func(T) (id, versionRange string),
) ([]T, []core.Gap) {

	counts := map[string]int{}
	for _, r := range rules {
		id, _ := key(r)
		counts[id]++
	}

	var out []T
	placed := map[string]bool{}
	for _, r := range rules {
		id, vr := key(r)

		if counts[id] == 1 {
			// A singly-declared scoped rule is kept when the version is
			// unknown: there is nothing to choose between, and if it turns out
			// not to apply, its query returns nothing and the engine already
			// records that as a gap (D-6).
			if appliesToVersion(vr, version) {
				out = append(out, r)
			} else {
				note(kind, id)
			}
			continue
		}

		if strings.TrimSpace(version) == "" {
			if !placed[id] {
				placed[id] = true
				gaps = append(gaps, core.Gap{
					Intent: kind + " " + id,
					Reason: core.GapNotApplicable,
					Code:   "MAS-5018",
					Detail: errs.New("MAS-5018", kind, id).Message("en"),
					Impact: "the version-specific form of this rule could not be chosen, " +
						"so every check that depends on it was skipped",
				})
			}
			continue
		}
		if appliesToVersion(vr, version) && !placed[id] {
			placed[id] = true
			out = append(out, r)
		}
	}

	// A rename with a hole in it: the pack covers this version but cannot place
	// one of its own rules there. The author needs the id.
	for id, n := range counts {
		if n > 1 && !placed[id] && strings.TrimSpace(version) != "" {
			gaps = append(gaps, core.Gap{
				Intent: kind + " " + id,
				Reason: core.GapNotApplicable,
				Code:   "MAS-5017",
				Detail: errs.New("MAS-5017", kind, id, version).Message("en"),
				Impact: "no variant of this rule covers the deployed version, " +
					"so every check that depends on it was skipped",
			})
		}
	}
	return out, gaps
}

// resolveSteps drops the steps a playbook can no longer run, and reports their
// ids.
//
// Two passes, because the reasons are different. A step can lose a rule it
// names — a signal it expands or a failure mode it concludes — and a step can
// lose a slot no surviving step produces. The first pass creates the second's
// input, and dropping a step in the second pass unbinds nothing further,
// because only a collect binds.
func resolveSteps(pb Playbook, version string, signals, modes map[string]bool) ([]Step, []string) {
	var dropped []string
	unbound := map[string]bool{}

	var kept []Step
	for _, st := range pb.Steps {
		switch {
		case !appliesToVersion(st.VersionRange, version):
		case st.Collect != nil && !refsResolve(st.Collect.Args, signals):
		case st.Conclude != nil && !modes[st.Conclude.FailureMode]:
		default:
			kept = append(kept, st)
			continue
		}
		if st.Collect != nil && st.Collect.As != "" {
			unbound[st.Collect.As] = true
		}
		dropped = append(dropped, st.ID)
	}

	if len(unbound) == 0 {
		return kept, dropped
	}

	var final []Step
	for _, st := range kept {
		if readsUnbound(st, unbound) {
			dropped = append(dropped, st.ID)
			continue
		}
		final = append(final, st)
	}
	return final, dropped
}

// readsUnbound reports whether a step's expressions read a slot nothing
// produces any more.
//
// The identifiers come from core.Identifiers, the same scanner the rule engine
// evaluates with, and not from a substring search. A substring search would
// read a slot name inside a regex literal as a reference — the defect feature
// 002 fixed, which would otherwise be reintroduced here in a new place
// (design-lld.md §5.3).
func readsUnbound(st Step, unbound map[string]bool) bool {
	exprs := []string{st.Evaluate}
	if st.Conclude != nil {
		exprs = append(exprs, st.Conclude.When)
	}
	for _, e := range exprs {
		for _, ident := range core.Identifiers(e) {
			if unbound[ident] && !core.IsExprBuiltin(ident) {
				return true
			}
		}
	}
	return false
}

// refsResolve reports whether every {{signal:id}} in a step's arguments still
// names a signal the resolved pack has.
func refsResolve(args map[string]any, signals map[string]bool) bool {
	for _, ref := range signalRefs(args) {
		if !signals[ref] {
			return false
		}
	}
	return true
}

// canConclude reports whether a playbook can still reach a failure mode. One
// that cannot would spend queries and return findings without a verdict.
func canConclude(steps []Step) bool {
	for _, st := range steps {
		if st.Conclude != nil {
			return true
		}
	}
	return false
}

// appliesToVersion reports whether a rule's range covers a version. An empty
// range covers everything, which is what every rule written before this feature
// says by saying nothing.
func appliesToVersion(raw, version string) bool {
	vr, err := parseVersionRange(raw)
	if err != nil {
		return true
	}
	return vr.Applies(version)
}

func idsOf[T any](rules []T, id func(T) string) map[string]bool {
	out := make(map[string]bool, len(rules))
	for _, r := range rules {
		out[id(r)] = true
	}
	return out
}

func describeVersion(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unknown)"
	}
	return v
}
