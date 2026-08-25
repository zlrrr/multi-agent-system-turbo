// Package knowledge loads middleware expertise as versioned data rather than
// code, so adding a middleware requires no recompilation (project goal G2.1).
//
// Governs: specs/001-mvp-core/design-lld.md §2.11
package knowledge

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// APIVersion is the pack schema version this build understands.
const APIVersion = "mas.turbo/v1"

// Kind is the pack document kind.
const Kind = "KnowledgePack"

// Text is a bilingual string. It is core.Text: knowledge packs and the topology
// registry need the same thing, so they share one type rather than two that
// drift. The alias keeps every pack, every YAML key and every caller unchanged.
type Text = core.Text

// Metadata identifies a pack and the versions it applies to.
type Metadata struct {
	Middleware   string `yaml:"middleware" json:"middleware"`
	Name         string `yaml:"name" json:"name"`
	Version      string `yaml:"version" json:"version"`
	VersionRange string `yaml:"versionRange" json:"version_range"`
	Source       string `yaml:"-" json:"source,omitempty"` // file the pack was loaded from
}

// Signal is a named, parameterised PromQL fragment. Playbooks reference signals
// by id so the query lives in one place and can be corrected once.
type Signal struct {
	ID          string `yaml:"id" json:"id"`
	PromQL      string `yaml:"promql" json:"promql"`
	Unit        string `yaml:"unit" json:"unit"`
	Description Text   `yaml:"description" json:"description"`
}

// LogPattern maps a recognisable log line to its meaning.
type LogPattern struct {
	ID       string `yaml:"id" json:"id"`
	Regex    string `yaml:"regex" json:"regex"`
	Severity string `yaml:"severity" json:"severity"`
	Meaning  Text   `yaml:"meaning" json:"meaning"`

	compiled *regexp.Regexp
}

// Match reports whether a log line exhibits this pattern.
func (p *LogPattern) Match(line string) bool {
	if p.compiled == nil {
		return false
	}
	return p.compiled.MatchString(line)
}

// Advice is one recommended action for a human operator.
type Advice struct {
	Risk      string `yaml:"risk" json:"risk"`
	Statement Text   `yaml:"statement" json:"statement"`
	Rationale Text   `yaml:"rationale" json:"rationale"`
}

// FailureMode is a named way this middleware goes wrong.
type FailureMode struct {
	ID              string   `yaml:"id" json:"id"`
	Title           Text     `yaml:"title" json:"title"`
	Explanation     Text     `yaml:"explanation" json:"explanation"`
	Symptoms        []string `yaml:"symptoms" json:"symptoms"`
	Indicators      []string `yaml:"indicators" json:"indicators"`
	Severity        string   `yaml:"severity" json:"severity"`
	Recommendations []Advice `yaml:"recommendations" json:"recommendations"`
}

// Finding is what a playbook step concludes when its expression holds.
type StepFinding struct {
	Severity   string  `yaml:"severity" json:"severity"`
	Confidence float64 `yaml:"confidence" json:"confidence"`
	Statement  Text    `yaml:"statement" json:"statement"`
	Detail     Text    `yaml:"detail" json:"detail"`
}

// Collect fetches evidence into a named slot.
type Collect struct {
	Tool string         `yaml:"tool" json:"tool"`
	Args map[string]any `yaml:"args" json:"args"`
	As   string         `yaml:"as" json:"as"`
}

// Branch is what a step does on one side of its condition.
type Branch struct {
	Finding *StepFinding `yaml:"finding" json:"finding,omitempty"`
	Pass    Text         `yaml:"pass" json:"pass,omitempty"`
	Stop    bool         `yaml:"stop" json:"stop,omitempty"`
}

// Conclude attaches a failure mode's recommendations to the run.
type Conclude struct {
	FailureMode string `yaml:"failureMode" json:"failure_mode"`
	When        string `yaml:"when" json:"when,omitempty"`
}

// Step is one ordered unit of a playbook: exactly one of Collect, Evaluate or
// Conclude.
type Step struct {
	ID       string    `yaml:"id" json:"id"`
	Collect  *Collect  `yaml:"collect" json:"collect,omitempty"`
	Evaluate string    `yaml:"evaluate" json:"evaluate,omitempty"`
	OnTrue   *Branch   `yaml:"onTrue" json:"on_true,omitempty"`
	OnFalse  *Branch   `yaml:"onFalse" json:"on_false,omitempty"`
	Conclude *Conclude `yaml:"conclude" json:"conclude,omitempty"`
	Optional bool      `yaml:"optional" json:"optional,omitempty"`
}

// Playbook is a deterministic diagnostic procedure. Running one makes no model
// calls at all (FR-008).
type Playbook struct {
	ID          string   `yaml:"id" json:"id"`
	Title       Text     `yaml:"title" json:"title"`
	Matches     []string `yaml:"matches" json:"matches"`
	Description Text     `yaml:"description" json:"description"`
	Steps       []Step   `yaml:"steps" json:"steps"`
}

// Inspect is a read-only command an adapter may run for this middleware. The
// guard re-validates it at call time regardless of what a pack claims.
type Inspect struct {
	ID          string   `yaml:"id" json:"id"`
	Binary      string   `yaml:"binary" json:"binary"`
	Args        []string `yaml:"args" json:"args"`
	Description Text     `yaml:"description" json:"description"`
}

// SourceHints tell the source fetcher where this middleware's code lives.
type SourceHints struct {
	Repos []string `yaml:"repos" json:"repos"`
}

// Pack is one middleware's expertise.
type Pack struct {
	APIVersion   string        `yaml:"apiVersion" json:"api_version"`
	Kind         string        `yaml:"kind" json:"kind"`
	Metadata     Metadata      `yaml:"metadata" json:"metadata"`
	Signals      []Signal      `yaml:"signals" json:"signals"`
	LogPatterns  []LogPattern  `yaml:"logPatterns" json:"log_patterns"`
	FailureModes []FailureMode `yaml:"failureModes" json:"failure_modes"`
	Playbooks    []Playbook    `yaml:"playbooks" json:"playbooks"`
	Inspect      []Inspect     `yaml:"inspect" json:"inspect"`
	Source       SourceHints   `yaml:"source" json:"source"`
}

// ID is the pack's unique identifier across all pack directories.
func (p *Pack) ID() string { return p.Metadata.Middleware + "/" + p.Metadata.Name }

// Signal returns a signal by id.
func (p *Pack) Signal(id string) (Signal, bool) {
	for _, s := range p.Signals {
		if s.ID == id {
			return s, true
		}
	}
	return Signal{}, false
}

// FailureMode returns a failure mode by id.
func (p *Pack) FailureMode(id string) (FailureMode, bool) {
	for _, f := range p.FailureModes {
		if f.ID == id {
			return f, true
		}
	}
	return FailureMode{}, false
}

// Playbook returns a playbook by id.
func (p *Pack) Playbook(id string) (*Playbook, bool) {
	for i := range p.Playbooks {
		if p.Playbooks[i].ID == id {
			return &p.Playbooks[i], true
		}
	}
	return nil, false
}

// MatchingPlaybooks selects the playbooks whose match terms appear in the
// symptom, ordered deterministically by how specifically they match.
//
// A playbook with no match terms is a general check and always runs, last.
func (p *Pack) MatchingPlaybooks(symptom string) []*Playbook {
	sym := strings.ToLower(symptom)
	type scored struct {
		pb    *Playbook
		score int
	}
	var out []scored
	for i := range p.Playbooks {
		pb := &p.Playbooks[i]
		if len(pb.Matches) == 0 {
			out = append(out, scored{pb, 0})
			continue
		}
		score := 0
		for _, m := range pb.Matches {
			if m != "" && strings.Contains(sym, strings.ToLower(m)) {
				score++
			}
		}
		if score > 0 {
			out = append(out, scored{pb, score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].pb.ID < out[j].pb.ID
	})
	res := make([]*Playbook, 0, len(out))
	for _, s := range out {
		res = append(res, s.pb)
	}
	return res
}

// MatchLogPatterns returns the patterns a log line exhibits.
func (p *Pack) MatchLogPatterns(line string) []LogPattern {
	var out []LogPattern
	for i := range p.LogPatterns {
		if p.LogPatterns[i].Match(line) {
			out = append(out, p.LogPatterns[i])
		}
	}
	return out
}

// InspectCommands returns the pack's declared inspection commands.
func (p *Pack) InspectCommands() []Inspect { return p.Inspect }

// Summary renders a compact description for a model prompt: enough domain
// grounding to reason with, small enough not to dominate the context.
func (p *Pack) Summary(lang string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Middleware: %s (knowledge pack %s v%s)\n",
		p.Metadata.Middleware, p.Metadata.Name, p.Metadata.Version)

	if len(p.FailureModes) > 0 {
		b.WriteString("\nKnown failure modes:\n")
		for _, f := range p.FailureModes {
			fmt.Fprintf(&b, "- %s (%s): %s\n", f.ID, f.Severity, f.Title.In(lang))
			if s := f.Explanation.In(lang); s != "" {
				fmt.Fprintf(&b, "  %s\n", s)
			}
		}
	}
	if len(p.Signals) > 0 {
		b.WriteString("\nAvailable metric signals (reference by id in PromQL, or use the expression directly):\n")
		for _, s := range p.Signals {
			fmt.Fprintf(&b, "- %s [%s]: %s\n    %s\n", s.ID, s.Unit, s.Description.In(lang), s.PromQL)
		}
	}
	if len(p.LogPatterns) > 0 {
		b.WriteString("\nSignificant log patterns:\n")
		for _, l := range p.LogPatterns {
			fmt.Fprintf(&b, "- %s (%s): /%s/ — %s\n", l.ID, l.Severity, l.Regex, l.Meaning.In(lang))
		}
	}
	return b.String()
}

// versionRange is a parsed constraint such as ">=5.0 <8.0".
type versionRange struct {
	raw    string
	checks []versionCheck
}

type versionCheck struct {
	op      string
	version []int
}

func parseVersionRange(raw string) (versionRange, error) {
	vr := versionRange{raw: strings.TrimSpace(raw)}
	if vr.raw == "" {
		return vr, nil
	}
	for _, part := range strings.Fields(vr.raw) {
		op := ""
		for _, candidate := range []string{">=", "<=", "!=", "==", ">", "<", "="} {
			if strings.HasPrefix(part, candidate) {
				op = candidate
				break
			}
		}
		if op == "" {
			op = "=="
		}
		num := strings.TrimPrefix(part, op)
		if op == "=" {
			op = "=="
		}
		v, err := parseVersion(num)
		if err != nil {
			return vr, err
		}
		vr.checks = append(vr.checks, versionCheck{op: op, version: v})
	}
	return vr, nil
}

func parseVersion(s string) ([]int, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if s == "" {
		return nil, fmt.Errorf("empty version")
	}
	// Drop any pre-release or build suffix: 7.2.4-rc1 compares as 7.2.4.
	if i := strings.IndexAny(s, "-+"); i > 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%q is not a numeric version component", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Applies reports whether the pack covers a deployed version. An unparseable or
// absent version is treated as "applies": refusing to help because a version
// string is unusual would be the wrong trade in an incident.
func (vr versionRange) Applies(version string) bool {
	if len(vr.checks) == 0 || strings.TrimSpace(version) == "" {
		return true
	}
	v, err := parseVersion(version)
	if err != nil {
		return true
	}
	for _, c := range vr.checks {
		cmp := compareVersions(v, c.version)
		ok := false
		switch c.op {
		case ">=":
			ok = cmp >= 0
		case "<=":
			ok = cmp <= 0
		case ">":
			ok = cmp > 0
		case "<":
			ok = cmp < 0
		case "==":
			ok = cmp == 0
		case "!=":
			ok = cmp != 0
		}
		if !ok {
			return false
		}
	}
	return true
}

// AppliesTo reports whether this pack covers the given middleware version.
func (p *Pack) AppliesTo(version string) bool {
	vr, err := parseVersionRange(p.Metadata.VersionRange)
	if err != nil {
		return true
	}
	return vr.Applies(version)
}

// MiddlewareKind returns the pack's middleware as a domain kind.
func (p *Pack) MiddlewareKind() core.MiddlewareKind {
	return core.MiddlewareKind(p.Metadata.Middleware)
}

// severities and risks accepted by the schema.
var (
	validSeverities = map[string]bool{"critical": true, "major": true, "minor": true, "info": true}
	validRisks      = map[string]bool{"low": true, "medium": true, "high": true}
)

// Validate checks a pack against the published schema, reporting the first
// problem with the path that caused it (FR-007).
func (p *Pack) Validate() error {
	bad := func(path, msg string) error {
		return errs.New("MAS-5001", p.sourceLabel(), path, msg)
	}
	if p.APIVersion != APIVersion {
		return bad("apiVersion", fmt.Sprintf("%q is not %q", p.APIVersion, APIVersion))
	}
	if p.Kind != Kind {
		return bad("kind", fmt.Sprintf("%q is not %q", p.Kind, Kind))
	}
	if p.Metadata.Middleware == "" {
		return bad("metadata.middleware", "must be set")
	}
	if p.Metadata.Name == "" {
		return bad("metadata.name", "must be set")
	}
	if p.Metadata.Version == "" {
		return bad("metadata.version", "must be set")
	}
	if _, err := parseVersionRange(p.Metadata.VersionRange); err != nil {
		return errs.New("MAS-5004", p.sourceLabel(), p.Metadata.VersionRange)
	}

	signalIDs := map[string]bool{}
	for i, s := range p.Signals {
		path := fmt.Sprintf("signals[%d]", i)
		if s.ID == "" {
			return bad(path+".id", "must be set")
		}
		if signalIDs[s.ID] {
			return bad(path+".id", "duplicate signal id "+s.ID)
		}
		signalIDs[s.ID] = true
		if s.PromQL == "" {
			return bad(path+".promql", "must be set")
		}
		if !s.Description.Complete() {
			return bad(path+".description", "must provide both en and zh text")
		}
	}

	patternIDs := map[string]bool{}
	for i := range p.LogPatterns {
		lp := &p.LogPatterns[i]
		path := fmt.Sprintf("logPatterns[%d]", i)
		if lp.ID == "" {
			return bad(path+".id", "must be set")
		}
		if patternIDs[lp.ID] {
			return bad(path+".id", "duplicate log pattern id "+lp.ID)
		}
		patternIDs[lp.ID] = true
		re, err := regexp.Compile(lp.Regex)
		if err != nil {
			return bad(path+".regex", err.Error())
		}
		lp.compiled = re
		if lp.Severity != "" && !validSeverities[lp.Severity] {
			return bad(path+".severity", fmt.Sprintf("%q is not critical, major, minor or info", lp.Severity))
		}
		if !lp.Meaning.Complete() {
			return bad(path+".meaning", "must provide both en and zh text")
		}
	}

	modeIDs := map[string]bool{}
	for i, f := range p.FailureModes {
		path := fmt.Sprintf("failureModes[%d]", i)
		if f.ID == "" {
			return bad(path+".id", "must be set")
		}
		if modeIDs[f.ID] {
			return bad(path+".id", "duplicate failure mode id "+f.ID)
		}
		modeIDs[f.ID] = true
		if !f.Title.Complete() {
			return bad(path+".title", "must provide both en and zh text")
		}
		if !f.Explanation.Empty() && !f.Explanation.Complete() {
			return bad(path+".explanation", "must provide both en and zh text")
		}
		if f.Severity != "" && !validSeverities[f.Severity] {
			return bad(path+".severity", fmt.Sprintf("%q is not critical, major, minor or info", f.Severity))
		}
		for j, r := range f.Recommendations {
			rp := fmt.Sprintf("%s.recommendations[%d]", path, j)
			if !validRisks[r.Risk] {
				return bad(rp+".risk", fmt.Sprintf("%q is not low, medium or high", r.Risk))
			}
			if !r.Statement.Complete() {
				return bad(rp+".statement", "must provide both en and zh text")
			}
		}
	}

	playbookIDs := map[string]bool{}
	for i, pb := range p.Playbooks {
		path := fmt.Sprintf("playbooks[%d]", i)
		if pb.ID == "" {
			return bad(path+".id", "must be set")
		}
		if playbookIDs[pb.ID] {
			return bad(path+".id", "duplicate playbook id "+pb.ID)
		}
		playbookIDs[pb.ID] = true
		if !pb.Title.Complete() {
			return bad(path+".title", "must provide both en and zh text")
		}
		if len(pb.Steps) == 0 {
			return bad(path+".steps", "a playbook must have at least one step")
		}
		stepIDs := map[string]bool{}
		for j, st := range pb.Steps {
			sp := fmt.Sprintf("%s.steps[%d]", path, j)
			if st.ID == "" {
				return bad(sp+".id", "must be set")
			}
			if stepIDs[st.ID] {
				return bad(sp+".id", "duplicate step id "+st.ID)
			}
			stepIDs[st.ID] = true

			declared := 0
			for _, present := range []bool{st.Collect != nil, st.Evaluate != "", st.Conclude != nil} {
				if present {
					declared++
				}
			}
			if declared != 1 {
				return errs.New("MAS-5014", pb.ID, st.ID,
					fmt.Sprintf("must declare exactly one of collect, evaluate or conclude, got %d", declared))
			}
			if st.Collect != nil {
				if st.Collect.Tool == "" {
					return bad(sp+".collect.tool", "must be set")
				}
				if st.Collect.As == "" {
					return bad(sp+".collect.as", "must name the slot the result binds to")
				}
				for _, ref := range signalRefs(st.Collect.Args) {
					if !signalIDs[ref] {
						return errs.New("MAS-5012", pb.ID, ref)
					}
				}
			}
			if st.Conclude != nil && !modeIDs[st.Conclude.FailureMode] {
				return bad(sp+".conclude.failureMode", "unknown failure mode "+st.Conclude.FailureMode)
			}
			for name, br := range map[string]*Branch{"onTrue": st.OnTrue, "onFalse": st.OnFalse} {
				if br == nil || br.Finding == nil {
					continue
				}
				fp := sp + "." + name + ".finding"
				if !br.Finding.Statement.Complete() {
					return bad(fp+".statement", "must provide both en and zh text")
				}
				if br.Finding.Severity != "" && !validSeverities[br.Finding.Severity] {
					return bad(fp+".severity", fmt.Sprintf("%q is not critical, major, minor or info", br.Finding.Severity))
				}
				if br.Finding.Confidence < 0 || br.Finding.Confidence > 1 {
					return bad(fp+".confidence", "must be within 0..1")
				}
			}
			for name, br := range map[string]*Branch{"onTrue": st.OnTrue, "onFalse": st.OnFalse} {
				if br == nil || br.Pass.Empty() {
					continue
				}
				if !br.Pass.Complete() {
					return bad(sp+"."+name+".pass", "must provide both en and zh text")
				}
			}
		}
	}

	inspectIDs := map[string]bool{}
	for i, in := range p.Inspect {
		path := fmt.Sprintf("inspect[%d]", i)
		if in.ID == "" {
			return bad(path+".id", "must be set")
		}
		if inspectIDs[in.ID] {
			return bad(path+".id", "duplicate inspect id "+in.ID)
		}
		inspectIDs[in.ID] = true
		if in.Binary == "" {
			return bad(path+".binary", "must be set")
		}
		if !in.Description.Complete() {
			return bad(path+".description", "must provide both en and zh text")
		}
	}
	return nil
}

func (p *Pack) sourceLabel() string {
	if p.Metadata.Source != "" {
		return p.Metadata.Source
	}
	return p.ID()
}

// signalRefTemplate matches the {{signal:id}} reference form used in playbook
// arguments.
var signalRefTemplate = regexp.MustCompile(`\{\{signal:([A-Za-z0-9_.-]+)\}\}`)

func signalRefs(args map[string]any) []string {
	var out []string
	for _, v := range args {
		s, ok := v.(string)
		if !ok {
			continue
		}
		for _, m := range signalRefTemplate.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// ExpandSignals substitutes {{signal:id}} references and {{.selector}} into a
// playbook step's arguments.
func (p *Pack) ExpandSignals(args map[string]any, selector string) (map[string]any, error) {
	out := make(map[string]any, len(args))
	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		var missing string
		s = signalRefTemplate.ReplaceAllStringFunc(s, func(match string) string {
			id := signalRefTemplate.FindStringSubmatch(match)[1]
			sig, found := p.Signal(id)
			if !found {
				missing = id
				return match
			}
			return sig.PromQL
		})
		if missing != "" {
			return nil, errs.New("MAS-5012", p.ID(), missing)
		}
		out[k] = strings.ReplaceAll(s, "{{.selector}}", selector)
	}
	return out, nil
}
