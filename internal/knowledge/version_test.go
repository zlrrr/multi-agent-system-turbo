package knowledge

import (
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// scopedPack declares a version range on every rule kind, which is FR-001: the
// field has to exist in all six places or an author will find the one that is
// missing at the worst moment.
const scopedPack = `
apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata: { middleware: testware, name: testware-core, version: 1.0.0 }
signals:
  - id: up
    versionRange: ">=2.0"
    promql: 'testware_up{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }
logPatterns:
  - id: legacy_warning
    versionRange: "<2.0"
    regex: 'legacy subsystem'
    severity: minor
    meaning: { en: "legacy", zh: "旧版" }
failureModes:
  - id: down
    versionRange: ">=1.0"
    severity: critical
    title: { en: "Down", zh: "宕机" }
    recommendations:
      - risk: low
        statement: { en: "check it", zh: "检查一下" }
playbooks:
  - id: testware.availability
    versionRange: ">=2.0"
    title: { en: "Availability", zh: "可用性" }
    matches: ["down"]
    steps:
      - id: collect
        collect: { tool: promql.instant, args: { query: "{{signal:up}}" }, as: up }
      - id: eval
        versionRange: ">=2.1"
        evaluate: "up.latest < 1"
        onTrue:
          finding:
            severity: critical
            confidence: 0.9
            statement: { en: "It is down", zh: "它宕了" }
        onFalse:
          pass: { en: "It is up", zh: "它在线" }
      - id: conclude
        conclude: { failureMode: down, when: "up.latest < 1" }
inspect:
  - id: status
    versionRange: ">=2.0"
    binary: redis-cli
    args: ["-h", "{{.host}}", "INFO"]
    description: { en: "status", zh: "状态" }
`

// TestEveryRuleKindAcceptsAVersionRange is FR-001.
func TestEveryRuleKindAcceptsAVersionRange(t *testing.T) {
	p, err := Parse([]byte(scopedPack), "scoped.yaml")
	if err != nil {
		t.Fatalf("a pack with ranges on every rule kind did not load: %v", err)
	}

	got := map[string]string{
		"signal":      p.Signals[0].VersionRange,
		"logPattern":  p.LogPatterns[0].VersionRange,
		"failureMode": p.FailureModes[0].VersionRange,
		"playbook":    p.Playbooks[0].VersionRange,
		"step":        p.Playbooks[0].Steps[1].VersionRange,
		"inspect":     p.Inspect[0].VersionRange,
	}
	want := map[string]string{
		"signal": ">=2.0", "logPattern": "<2.0", "failureMode": ">=1.0",
		"playbook": ">=2.0", "step": ">=2.1", "inspect": ">=2.0",
	}
	for kind, w := range want {
		if got[kind] != w {
			t.Errorf("%s range is %q, want %q", kind, got[kind], w)
		}
	}
}

// TestRangeOverlapDetection is FR-004's foundation. The bias is deliberate:
// when the answer is unclear we report overlap, because a false "overlap" is a
// load error the author sees immediately and a false "disjoint" is an ambiguity
// that surfaces as the wrong metric name during an incident (plan.md RSK-2).
func TestRangeOverlapDetection(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
		why  string
	}{
		{">=3.3", "<3.3", false, "the classic split at a boundary"},
		{"<3.3", ">=3.3", false, "order must not matter"},
		{">=3.3", "<3.4", true, "3.3 satisfies both"},
		{">=4.0 <5.0", ">=5.0", false, "adjacent half-open windows"},
		{">=4.0 <5.0", ">=4.9", true, "4.9 is in both"},
		{"==2.0", "==2.0", true, "the same point"},
		{"==2.0", "==2.1", false, "different points"},
		{"==2.0", ">=1.0 <3.0", true, "a point inside a window"},
		{"", ">=3.0", true, "an unscoped declaration overlaps everything"},
		{"", "", true, "two unscoped declarations are just a duplicate"},
		{">3.3", "<=3.3", false, "strict and non-strict at the same point"},
		{">=3.3", "<=3.3", true, "both include 3.3"},
		{"!=3.3", ">=3.3", true, "!= is not modelled, so we report overlap"},
		{"nonsense", ">=1.0", true, "an unparseable range is treated as overlapping"},
		{">=1.2.3", "<1.2.3", false, "three components compare component-wise"},
		{">=1.2", "<1.2.3", true, "1.2.0 through 1.2.2 are in both"},
	}
	for _, c := range cases {
		if got := rangesOverlap(c.a, c.b); got != c.want {
			t.Errorf("rangesOverlap(%q, %q) = %v, want %v — %s", c.a, c.b, got, c.want, c.why)
		}
	}
}

// TestVariantsWithDisjointRangesAreAccepted is FR-003. This is what expresses a
// rename: one id, two expressions, and the run picks by version.
func TestVariantsWithDisjointRangesAreAccepted(t *testing.T) {
	body := strings.Replace(minimalPack, `signals:
  - id: up
    promql: 'testware_up{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }`, `signals:
  - id: up
    versionRange: "<2.0"
    promql: 'testware_alive{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }
  - id: up
    versionRange: ">=2.0"
    promql: 'testware_up{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }`, 1)

	p, err := Parse([]byte(body), "variants.yaml")
	if err != nil {
		t.Fatalf("disjoint variants of one id were rejected: %v", err)
	}
	if len(p.Signals) != 2 {
		t.Fatalf("both variants should survive parsing, got %d", len(p.Signals))
	}
}

// TestOverlappingVariantsAreRejected is FR-004. A pack that is ambiguous for
// some version is broken for everyone, and finding out during an incident is
// the worst possible time (plan.md D-4).
func TestOverlappingVariantsAreRejected(t *testing.T) {
	for _, c := range []struct {
		name          string
		first, second string
		wantCode      string
	}{
		{"overlapping ranges", `versionRange: ">=1.0"`, `versionRange: ">=2.0"`, "MAS-5016"},
		{"one unscoped", ``, `versionRange: ">=2.0"`, "MAS-5001"},
		{"both unscoped", ``, ``, "MAS-5001"},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := strings.Replace(minimalPack, `signals:
  - id: up
    promql: 'testware_up{{.selector}}'`, `signals:
  - id: up
    `+c.first+`
    promql: 'testware_alive{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }
  - id: up
    `+c.second+`
    promql: 'testware_up{{.selector}}'`, 1)

			_, err := Parse([]byte(body), "ambiguous.yaml")
			if err == nil {
				t.Fatal("an ambiguous pack loaded")
			}
			if got := errs.CodeOf(err); got != c.wantCode {
				t.Errorf("code %s, want %s: %v", got, c.wantCode, err)
			}
			if !strings.Contains(err.Error(), "up") {
				t.Errorf("the error does not name the id: %v", err)
			}
		})
	}
}

// resolvedIDs is a reading aid: the ids of one rule kind, in order.
func signalIDsOf(p *Pack) []string {
	out := make([]string, 0, len(p.Signals))
	for _, s := range p.Signals {
		out = append(out, s.ID)
	}
	return out
}

func playbookIDsOf(p *Pack) []string {
	out := make([]string, 0, len(p.Playbooks))
	for _, pb := range p.Playbooks {
		out = append(out, pb.ID)
	}
	return out
}

func gapCodes(gaps []core.Gap) []string {
	out := make([]string, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, g.Code)
	}
	return out
}

// TestOutOfRangeRulesAreDropped is FR-002.
func TestOutOfRangeRulesAreDropped(t *testing.T) {
	p, err := Parse([]byte(scopedPack), "scoped.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// 1.5 excludes the signal (>=2.0), the playbook (>=2.0) and the inspect
	// command (>=2.0); it includes the log pattern (<2.0) and the mode (>=1.0).
	old, _ := p.Resolve("1.5")
	if len(old.Signals) != 0 {
		t.Errorf("signal scoped >=2.0 survived version 1.5: %v", signalIDsOf(old))
	}
	if len(old.LogPatterns) != 1 {
		t.Errorf("log pattern scoped <2.0 was dropped at version 1.5")
	}
	if len(old.Inspect) != 0 {
		t.Errorf("inspect command scoped >=2.0 survived version 1.5")
	}

	// 2.5 is the mirror image.
	recent, _ := p.Resolve("2.5")
	if len(recent.Signals) != 1 {
		t.Errorf("signal scoped >=2.0 was dropped at version 2.5")
	}
	if len(recent.LogPatterns) != 0 {
		t.Errorf("log pattern scoped <2.0 survived version 2.5")
	}
	if len(recent.Inspect) != 1 {
		t.Errorf("inspect command scoped >=2.0 was dropped at version 2.5")
	}

	// The receiver is shared by every target of this middleware, so resolving
	// must never touch it.
	if len(p.Signals) != 1 || len(p.LogPatterns) != 1 || len(p.Inspect) != 1 {
		t.Error("Resolve mutated the pack it was called on")
	}
}

// TestUnscopedPackResolvesToItself is NFR-004: every pack written before this
// feature must behave exactly as it did.
func TestUnscopedPackResolvesToItself(t *testing.T) {
	// A pack with no ranges anywhere: resolution must be the identity, for
	// every version and for none.
	plain, err := Parse([]byte(minimalPack), "plain.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "0.1", "1.0.0", "99.99.99"} {
		got, gaps := plain.Resolve(version)
		if len(gaps) != 0 {
			t.Errorf("an unscoped pack at %q recorded %d gap(s): %v",
				version, len(gaps), gapCodes(gaps))
		}
		if len(got.Signals) != len(plain.Signals) ||
			len(got.FailureModes) != len(plain.FailureModes) ||
			len(got.Playbooks) != len(plain.Playbooks) ||
			len(got.Inspect) != len(plain.Inspect) {
			t.Errorf("an unscoped pack at %q lost rules", version)
		}
		for i, pb := range got.Playbooks {
			if len(pb.Steps) != len(plain.Playbooks[i].Steps) {
				t.Errorf("an unscoped pack at %q lost steps from %s", version, pb.ID)
			}
		}
	}

	lib, err := LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range lib.All() {
		for _, version := range []string{"", "1.0.0", "7.2.4", "99.0"} {
			got, gaps := p.Resolve(version)
			if len(got.Signals) != len(p.Signals) ||
				len(got.LogPatterns) != len(p.LogPatterns) ||
				len(got.FailureModes) != len(p.FailureModes) ||
				len(got.Inspect) != len(p.Inspect) {
				continue // a shipped pack may legitimately scope some rules
			}
			if len(got.Playbooks) == len(p.Playbooks) && len(gaps) > 0 {
				t.Errorf("%s at %q: nothing was dropped but %d gap(s) were recorded: %v",
					p.ID(), version, len(gaps), gapCodes(gaps))
			}
		}
	}
}

// TestVariantMatchingTheVersionIsChosen is FR-005: one id, two expressions,
// picked by version. This is what a rename looks like in a pack.
func TestVariantMatchingTheVersionIsChosen(t *testing.T) {
	p, err := Parse([]byte(variantPack), "variants.yaml")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ version, wantPromQL string }{
		{"1.9", "testware_alive{{.selector}}"},
		{"2.0", "testware_up{{.selector}}"},
		{"3.4.1", "testware_up{{.selector}}"},
	} {
		got, gaps := p.Resolve(c.version)
		sig, ok := got.Signal("up")
		if !ok {
			t.Errorf("version %s: signal up was dropped (gaps %v)", c.version, gapCodes(gaps))
			continue
		}
		if sig.PromQL != c.wantPromQL {
			t.Errorf("version %s: promql %q, want %q", c.version, sig.PromQL, c.wantPromQL)
		}
	}
}

// TestUnknownVersionDropsVariantsWithAGap is FR-006. Picking a variant without
// a version would query a metric name that may not exist and read its absence
// as data; the gap says so and names the one-line remedy.
func TestUnknownVersionDropsVariantsWithAGap(t *testing.T) {
	p, err := Parse([]byte(variantPack), "variants.yaml")
	if err != nil {
		t.Fatal(err)
	}

	got, gaps := p.Resolve("")
	if _, ok := got.Signal("up"); ok {
		t.Error("a variant was chosen with no version to choose by")
	}

	var found *core.Gap
	for i := range gaps {
		if gaps[i].Code == "MAS-5018" {
			found = &gaps[i]
		}
	}
	if found == nil {
		t.Fatalf("no MAS-5018 gap for the unplaceable variant: %v", gapCodes(gaps))
	}
	if !strings.Contains(found.Intent, "up") {
		t.Errorf("the gap does not name the rule: %+v", *found)
	}
	if found.Impact == "" {
		t.Errorf("the gap does not state its effect on the analysis: %+v", *found)
	}
	def, ok := errs.Lookup(found.Code)
	if !ok {
		t.Fatalf("%s is not a registered code", found.Code)
	}
	if !strings.Contains(def.RemedyEN, "targets[].version") {
		t.Errorf("the remedy does not tell the operator what to set: %q", def.RemedyEN)
	}
}

// variantPack renames one signal at 2.0, which is the case rule-level scoping
// exists for: a pack can only give a signal one PromQL, so before this feature
// a rename forced a second pack and a duplicate of every playbook using it.
const variantPack = `
apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata: { middleware: testware, name: testware-core, version: 1.0.0 }
signals:
  - id: up
    versionRange: "<2.0"
    promql: 'testware_alive{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }
  - id: up
    versionRange: ">=2.0"
    promql: 'testware_up{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }
failureModes:
  - id: down
    severity: critical
    title: { en: "Down", zh: "宕机" }
    recommendations:
      - risk: low
        statement: { en: "check it", zh: "检查一下" }
playbooks:
  - id: testware.availability
    title: { en: "Availability", zh: "可用性" }
    matches: ["down"]
    steps:
      - id: collect
        collect: { tool: promql.instant, args: { query: "{{signal:up}}" }, as: up }
      - id: eval
        evaluate: "up.latest < 1"
        onTrue:
          finding:
            severity: critical
            confidence: 0.9
            statement: { en: "It is down", zh: "它宕了" }
        onFalse:
          pass: { en: "It is up", zh: "它在线" }
      - id: conclude
        conclude: { failureMode: down, when: "up.latest < 1" }
`

// cascadePack scopes one signal, so resolving it for an excluded version has to
// take the step that reads it, the step that evaluates its slot, and finally the
// playbook that can no longer conclude anything.
const cascadePack = `
apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata: { middleware: testware, name: testware-core, version: 1.0.0 }
signals:
  - id: up
    promql: 'testware_up{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }
  - id: legacy_sessions
    versionRange: "<4.0"
    promql: 'testware_legacy_sessions{{.selector}}'
    unit: count
    description: { en: "sessions", zh: "会话" }
failureModes:
  - id: down
    severity: critical
    title: { en: "Down", zh: "宕机" }
    recommendations:
      - risk: low
        statement: { en: "check it", zh: "检查一下" }
  - id: session-loss
    versionRange: "<4.0"
    severity: major
    title: { en: "Session loss", zh: "会话丢失" }
    recommendations:
      - risk: low
        statement: { en: "check the coordinator", zh: "检查协调层" }
playbooks:
  - id: testware.availability
    title: { en: "Availability", zh: "可用性" }
    matches: ["down"]
    steps:
      - id: collect-up
        collect: { tool: promql.instant, args: { query: "{{signal:up}}" }, as: up }
      - id: conclude-down
        conclude: { failureMode: down, when: "up.latest < 1" }
  - id: testware.sessions
    title: { en: "Sessions", zh: "会话" }
    matches: ["session"]
    steps:
      - id: collect-sessions
        collect: { tool: promql.range, args: { query: "{{signal:legacy_sessions}}" }, as: sessions }
      - id: eval-sessions
        evaluate: "sessions.delta < 0 and sessions.summary matches 'legacy'"
        onTrue:
          finding:
            severity: major
            confidence: 0.7
            statement: { en: "Sessions dropped", zh: "会话数下降" }
        onFalse:
          pass: { en: "Sessions steady", zh: "会话数平稳" }
      - id: conclude-sessions
        conclude: { failureMode: session-loss, when: "sessions.delta < 0" }
`

// TestStepsFollowTheRulesTheyDependOn is FR-007. A step referencing a signal
// that no longer resolves fails at run time with a template error, in the
// middle of a diagnosis. It has to go with the signal.
func TestStepsFollowTheRulesTheyDependOn(t *testing.T) {
	p, err := Parse([]byte(cascadePack), "cascade.yaml")
	if err != nil {
		t.Fatal(err)
	}

	got, gaps := p.Resolve("4.0.1")

	if _, ok := got.Signal("legacy_sessions"); ok {
		t.Error("a signal scoped <4.0 survived version 4.0.1")
	}
	if _, ok := got.FailureMode("session-loss"); ok {
		t.Error("a failure mode scoped <4.0 survived version 4.0.1")
	}

	// The whole sessions playbook depended on it, so nothing of it should
	// remain — and the availability playbook, which depended on none of it,
	// must be untouched.
	if ids := playbookIDsOf(got); len(ids) != 1 || ids[0] != "testware.availability" {
		t.Errorf("playbooks after resolution: %v, want only testware.availability", ids)
	}

	// Every reference in what survives must still resolve, or the run would
	// fail on the first expansion.
	for _, pb := range got.Playbooks {
		for _, st := range pb.Steps {
			if st.Collect == nil {
				continue
			}
			if _, err := got.ExpandSignals(st.Collect.Args, `{job="x"}`); err != nil {
				t.Errorf("surviving step %s/%s does not expand: %v", pb.ID, st.ID, err)
			}
		}
	}
	if len(gaps) == 0 {
		t.Error("a cascade this large recorded no gap at all")
	}
}

// TestStepsFollowTheSlotsTheyRead is FR-008, including the case that has caught
// this project out before: a slot name appearing inside a regex literal is
// text, not a reference.
func TestStepsFollowTheSlotsTheyRead(t *testing.T) {
	p, err := Parse([]byte(cascadePack), "cascade.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := p.Resolve("4.0.1")

	for _, pb := range got.Playbooks {
		for _, st := range pb.Steps {
			if st.ID == "eval-sessions" || st.ID == "conclude-sessions" {
				t.Errorf("step %s reads a slot nothing produces and survived", st.ID)
			}
		}
	}

	// The literal 'legacy' inside the expression must not have been read as a
	// reference to the dropped `legacy_sessions` signal — that confusion is
	// exactly the defect feature 002 fixed in the rule engine.
	kept, _ := p.Resolve("3.9")
	if len(kept.Playbooks) != 2 {
		t.Errorf("version 3.9 includes everything, yet %d playbook(s) survived", len(kept.Playbooks))
	}
}

// TestEmptyPlaybooksAreDropped is FR-009: a playbook that collects evidence and
// concludes nothing spends queries and returns findings without a verdict.
func TestEmptyPlaybooksAreDropped(t *testing.T) {
	p, err := Parse([]byte(cascadePack), "cascade.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := p.Resolve("4.0.1")
	for _, pb := range got.Playbooks {
		concludes := false
		for _, st := range pb.Steps {
			if st.Conclude != nil {
				concludes = true
			}
		}
		if !concludes {
			t.Errorf("playbook %s survived with no step that can reach a conclusion", pb.ID)
		}
	}
}

// TestSkippedRulesAreRecordedAsGaps is FR-010 and CON-002 — and the volume
// rule from design-hld.md §3.1: everything that follows from a *known* version
// is one gap, because a dozen entries for a correctly-scoped pack would teach
// operators to scroll past the gap list.
func TestSkippedRulesAreRecordedAsGaps(t *testing.T) {
	p, err := Parse([]byte(cascadePack), "cascade.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, gaps := p.Resolve("4.0.1")

	notApplicable := 0
	for _, g := range gaps {
		if g.Code != "MAS-5019" {
			continue
		}
		notApplicable++
		for _, want := range []string{"legacy_sessions", "session-loss", "testware.sessions"} {
			if !strings.Contains(g.Detail, want) {
				t.Errorf("the aggregate gap does not name %s: %q", want, g.Detail)
			}
		}
		if !strings.Contains(g.Detail, "4.0.1") {
			t.Errorf("the aggregate gap does not name the version: %q", g.Detail)
		}
		if g.Impact == "" {
			t.Error("the aggregate gap does not state its effect on the analysis")
		}
		if g.Reason != core.GapNotApplicable {
			t.Errorf("reason %q, want %q — 'unavailable' would claim the evidence "+
				"could not be obtained, which is a different and more alarming thing",
				g.Reason, core.GapNotApplicable)
		}
	}
	if notApplicable != 1 {
		t.Errorf("%d aggregate gaps, want exactly 1: %v", notApplicable, gapCodes(gaps))
	}
}

// TestResolutionNeverWidens is FR-011 and CON-001. Resolution narrows; it has
// no branch that adds a rule, and in particular none that adds an inspect
// command, which is the one rule kind that reaches a live system.
func TestResolutionNeverWidens(t *testing.T) {
	lib, err := LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	packs := lib.All()

	scoped, err := Parse([]byte(scopedPack), "scoped.yaml")
	if err != nil {
		t.Fatal(err)
	}
	variants, err := Parse([]byte(variantPack), "variants.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cascade, err := Parse([]byte(cascadePack), "cascade.yaml")
	if err != nil {
		t.Fatal(err)
	}
	packs = append(packs, scoped, variants, cascade)

	versions := []string{"", "0.1", "1.0", "1.9", "2.0", "3.3", "3.9", "4.0.1", "7.2.4", "99.99.99"}
	for _, p := range packs {
		before := ruleSet(p)
		for _, v := range versions {
			got, _ := p.Resolve(v)
			for rule := range ruleSet(got) {
				if !before[rule] {
					t.Errorf("%s at version %q: resolution produced %s, which the "+
						"unresolved pack does not contain", p.ID(), v, rule)
				}
			}
		}
	}
}

// ruleSet is every rule in a pack, identified in a way that survives
// resolution: kind, id and the range it was declared with.
func ruleSet(p *Pack) map[string]bool {
	out := map[string]bool{}
	for _, s := range p.Signals {
		out["signal/"+s.ID+"@"+s.VersionRange] = true
	}
	for _, lp := range p.LogPatterns {
		out["logPattern/"+lp.ID+"@"+lp.VersionRange] = true
	}
	for _, f := range p.FailureModes {
		out["failureMode/"+f.ID+"@"+f.VersionRange] = true
	}
	for _, in := range p.Inspect {
		out["inspect/"+in.ID+"@"+in.VersionRange] = true
	}
	for _, pb := range p.Playbooks {
		out["playbook/"+pb.ID+"@"+pb.VersionRange] = true
		for _, st := range pb.Steps {
			out["step/"+pb.ID+"/"+st.ID+"@"+st.VersionRange] = true
		}
	}
	return out
}

// TestKafkaPackScopesZooKeeperRules is FR-014, and D-9 in one test: the shipped
// packs are scoped only where the boundary is a documented fact. ZooKeeper was
// removed in Kafka 4.0, so a ZooKeeper pattern matching against a 4.x cluster
// can only be matching a line from something else — and a false match on a
// critical pattern is worse than no pattern at all.
func TestKafkaPackScopesZooKeeperRules(t *testing.T) {
	lib, err := LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := lib.For("kafka", "3.7.0")
	if err != nil {
		t.Fatal(err)
	}

	zk := ""
	for _, lp := range pack.LogPatterns {
		if strings.Contains(strings.ToLower(lp.Regex), "zookeeper") {
			zk = lp.ID
			if lp.VersionRange == "" {
				t.Errorf("log pattern %s mentions ZooKeeper and applies to every version, "+
					"including the ones that removed it", lp.ID)
			}
		}
	}
	if zk == "" {
		t.Skip("the kafka pack no longer carries a ZooKeeper pattern")
	}

	line := "Unable to reconnect to ZooKeeper, giving up"
	for _, c := range []struct {
		version string
		want    bool
	}{
		{"3.7.0", true},
		{"3.9.9", true},
		{"4.0.0", false},
		{"4.1.2", false},
	} {
		resolved, _ := pack.Resolve(c.version)
		matched := len(resolved.MatchLogPatterns(line)) > 0
		if matched != c.want {
			t.Errorf("version %s: ZooKeeper pattern matched=%v, want %v", c.version, matched, c.want)
		}
	}
}
