package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

const minimalPack = `
apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata: { middleware: testware, name: testware-core, version: 1.0.0 }
signals:
  - id: up
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
inspect:
  - id: status
    binary: redis-cli
    args: ["-h", "{{.host}}", "INFO"]
    description: { en: "status", zh: "状态" }
`

// TestEmbeddedPacksValid is the conformance gate for everything this project
// ships: a malformed pack must never reach a release.
func TestEmbeddedPacksValid(t *testing.T) {
	lib, err := LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Problems()) > 0 {
		for _, p := range lib.Problems() {
			t.Errorf("embedded pack failed to load: %v", p)
		}
	}
	if lib.Len() < 2 {
		t.Fatalf("expected at least the redis and kafka packs, got %d", lib.Len())
	}
	got := lib.Middlewares()
	for _, want := range []string{"redis", "kafka"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no pack for %s; middlewares = %v", want, got)
		}
	}
}

// TestBilingualPackFields enforces Constitution Art. III on shipped knowledge:
// an operator reading in Chinese must get the same expertise, not a fallback.
func TestBilingualPackFields(t *testing.T) {
	lib, err := LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range lib.All() {
		check := func(path string, txt Text) {
			if strings.TrimSpace(txt.EN) == "" {
				t.Errorf("%s: %s has no English text", p.ID(), path)
			}
			if strings.TrimSpace(txt.ZH) == "" {
				t.Errorf("%s: %s has no Chinese text", p.ID(), path)
			}
			if txt.EN == txt.ZH && len(txt.EN) > 12 {
				t.Errorf("%s: %s is identical in both languages; it is probably untranslated", p.ID(), path)
			}
		}
		for _, s := range p.Signals {
			check("signals."+s.ID+".description", s.Description)
		}
		for _, l := range p.LogPatterns {
			check("logPatterns."+l.ID+".meaning", l.Meaning)
		}
		for _, f := range p.FailureModes {
			check("failureModes."+f.ID+".title", f.Title)
			if !f.Explanation.Empty() {
				check("failureModes."+f.ID+".explanation", f.Explanation)
			}
			for i, r := range f.Recommendations {
				check(f.ID+".recommendations["+itoa(i)+"].statement", r.Statement)
			}
		}
		for _, pb := range p.Playbooks {
			check("playbooks."+pb.ID+".title", pb.Title)
			for _, st := range pb.Steps {
				for name, br := range map[string]*Branch{"onTrue": st.OnTrue, "onFalse": st.OnFalse} {
					if br == nil {
						continue
					}
					if br.Finding != nil {
						check(pb.ID+"/"+st.ID+"."+name+".finding.statement", br.Finding.Statement)
					}
					if !br.Pass.Empty() {
						check(pb.ID+"/"+st.ID+"."+name+".pass", br.Pass)
					}
				}
			}
		}
		for _, in := range p.Inspect {
			check("inspect."+in.ID+".description", in.Description)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestRedisPackConformance(t *testing.T) {
	lib, _ := LoadDefault(nil)
	p, err := lib.For(core.KindRedis, "7.2.4")
	if err != nil {
		t.Fatal(err)
	}
	// The pack must cover the failure modes an operator actually meets.
	for _, id := range []string{
		"memory-pressure", "eviction-storm", "connection-saturation", "slow-commands",
		"persistence-failure", "fork-latency", "replication-broken", "fragmentation",
		"cpu-saturation", "instance-down",
	} {
		if _, ok := p.FailureMode(id); !ok {
			t.Errorf("redis pack is missing failure mode %s", id)
		}
	}
	if len(p.Playbooks) < 5 {
		t.Errorf("redis pack has only %d playbooks", len(p.Playbooks))
	}
	if len(p.Signals) < 15 {
		t.Errorf("redis pack has only %d signals", len(p.Signals))
	}
	// The always-on playbook must run regardless of symptom wording.
	if got := p.MatchingPlaybooks("something nobody wrote a rule for"); len(got) == 0 {
		t.Error("no playbook runs for an unrecognised symptom; availability should always run")
	}
}

func TestKafkaPackConformance(t *testing.T) {
	lib, _ := LoadDefault(nil)
	p, err := lib.For(core.KindKafka, "3.7.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"under-replicated-partitions", "offline-partitions", "consumer-lag-growth",
		"broker-loss", "controller-instability", "broker-disk-latency",
	} {
		if _, ok := p.FailureMode(id); !ok {
			t.Errorf("kafka pack is missing failure mode %s", id)
		}
	}
	if len(p.Playbooks) < 3 {
		t.Errorf("kafka pack has only %d playbooks", len(p.Playbooks))
	}
}

// TestPackInspectCommandsAreGuardClean proves shipped packs declare only
// commands the guard will actually allow, so the product works out of the box.
func TestPackInspectCommandsAreGuardClean(t *testing.T) {
	lib, _ := LoadDefault(nil)
	for _, p := range lib.All() {
		for _, in := range p.Inspect {
			if in.Binary == "" {
				t.Errorf("%s: inspect %s has no binary", p.ID(), in.ID)
			}
			for _, a := range in.Args {
				if strings.ContainsAny(a, ";&|`$><") {
					t.Errorf("%s: inspect %s argument %q contains shell metacharacters", p.ID(), in.ID, a)
				}
			}
		}
	}
}

func TestSchemaViolations(t *testing.T) {
	cases := map[string]struct {
		mutate func(string) string
		want   string
	}{
		"wrong apiVersion": {func(s string) string { return strings.Replace(s, "mas.turbo/v1", "mas.turbo/v2", 1) }, "MAS-5001"},
		"wrong kind":       {func(s string) string { return strings.Replace(s, "kind: KnowledgePack", "kind: Other", 1) }, "MAS-5001"},
		"no middleware":    {func(s string) string { return strings.Replace(s, "middleware: testware, ", "", 1) }, "MAS-5001"},
		"no signal promql": {func(s string) string {
			return strings.Replace(s, "promql: 'testware_up{{.selector}}'", "promql: ''", 1)
		}, "MAS-5001"},
		"signal without zh": {func(s string) string {
			return strings.Replace(s, `description: { en: "up", zh: "在线" }`, `description: { en: "up" }`, 1)
		}, "MAS-5001"},
		"bad severity": {func(s string) string {
			return strings.Replace(s, "severity: critical\n    title:", "severity: catastrophic\n    title:", 1)
		}, "MAS-5001"},
		"bad risk": {func(s string) string { return strings.Replace(s, "risk: low", "risk: extreme", 1) }, "MAS-5001"},
		"bad version range": {func(s string) string {
			return strings.Replace(s, "version: 1.0.0 }", "version: 1.0.0, versionRange: \">=abc\" }", 1)
		}, "MAS-5004"},
		"unknown signal ref":   {func(s string) string { return strings.Replace(s, "{{signal:up}}", "{{signal:ghost}}", 1) }, "MAS-5012"},
		"unknown failure mode": {func(s string) string { return strings.Replace(s, "failureMode: down", "failureMode: ghost", 1) }, "MAS-5001"},
		"confidence too high":  {func(s string) string { return strings.Replace(s, "confidence: 0.9", "confidence: 1.4", 1) }, "MAS-5001"},
		"bad regex": {func(s string) string {
			return s + "\nlogPatterns:\n  - id: bad\n    regex: '([unclosed'\n    meaning: { en: \"x\", zh: \"y\" }\n"
		}, "MAS-5001"},
		"empty playbook": {func(s string) string {
			return strings.Replace(s, "    steps:\n      - id: collect", "    steps: []\n    ignored:\n      - id: collect", 1)
		}, "MAS-5001"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.mutate(minimalPack)), "test.yaml")
			if err == nil {
				t.Fatal("invalid pack accepted")
			}
			if errs.CodeOf(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, errs.CodeOf(err), tc.want)
			}
		})
	}
}

func TestStepMustDeclareExactlyOneAction(t *testing.T) {
	body := strings.Replace(minimalPack,
		"      - id: eval\n        evaluate: \"up.latest < 1\"",
		"      - id: eval\n        evaluate: \"up.latest < 1\"\n        conclude: { failureMode: down }", 1)
	_, err := Parse([]byte(body), "test.yaml")
	if errs.CodeOf(err) != "MAS-5014" {
		t.Fatalf("got %v, want MAS-5014", err)
	}
}

func TestMalformedYAMLIsCoded(t *testing.T) {
	if _, err := Parse([]byte("apiVersion: [unclosed"), "bad.yaml"); errs.CodeOf(err) != "MAS-5001" {
		t.Fatalf("got %v, want MAS-5001", err)
	}
}

func TestUserDirOverrides(t *testing.T) {
	dir := t.TempDir()
	override := strings.Replace(minimalPack, "middleware: testware, name: testware-core", "middleware: redis, name: redis-core", 1)
	override = strings.Replace(override, "testware_up", "redis_up", 1)
	if err := os.WriteFile(filepath.Join(dir, "redis.yaml"), []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}

	lib, err := LoadDefault([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	p, err := lib.For(core.KindRedis, "7.2.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Signals) != 1 || p.Signals[0].PromQL != "redis_up{{.selector}}" {
		t.Fatalf("the user pack did not replace the embedded one: %d signals", len(p.Signals))
	}
	if !strings.HasPrefix(p.Metadata.Source, dir) {
		t.Fatalf("source = %q, want the user directory", p.Metadata.Source)
	}
}

// TestPackOnlyMiddlewareAddition is the NFR-007 test: a new middleware requires
// only a data file, never a recompilation.
func TestPackOnlyMiddlewareAddition(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "testware.yaml"), []byte(minimalPack), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := LoadDefault([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	p, err := lib.For(core.MiddlewareKind("testware"), "1.0")
	if err != nil {
		t.Fatalf("a pack-only middleware was not usable: %v", err)
	}
	if len(p.MatchingPlaybooks("it is down")) != 1 {
		t.Fatal("the new middleware's playbook did not activate")
	}
	if _, ok := p.FailureMode("down"); !ok {
		t.Fatal("the new middleware's failure mode is missing")
	}
}

func TestOneBadPackDoesNotBlockTheRest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("apiVersion: wrong\nkind: KnowledgePack\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := LoadDefault([]string{dir})
	if err != nil {
		t.Fatalf("a broken user pack must not fail the whole load: %v", err)
	}
	if len(lib.Problems()) != 1 {
		t.Fatalf("problems = %d, want 1 recorded for doctor", len(lib.Problems()))
	}
	if _, err := lib.For(core.KindRedis, ""); err != nil {
		t.Fatalf("the embedded packs should still be usable: %v", err)
	}
}

func TestVersionRange(t *testing.T) {
	cases := []struct {
		rng     string
		version string
		want    bool
	}{
		{">=5.0", "7.2.4", true},
		{">=5.0", "4.0.14", false},
		{">=5.0 <8.0", "7.2.4", true},
		{">=5.0 <8.0", "8.0.1", false},
		{"<7", "6.2.0", true},
		{"==7.2.4", "7.2.4", true},
		{"==7.2.4", "7.2.5", false},
		{"!=7.0.0", "7.2.4", true},
		{">=5.0", "v7.2.4", true},
		{">=5.0", "7.2.4-rc1", true},
		{">=5.0", "", true},            // no version known: help anyway
		{">=5.0", "weird-build", true}, // unparseable: help anyway
		{"", "1.0.0", true},
	}
	for _, tc := range cases {
		vr, err := parseVersionRange(tc.rng)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.rng, err)
		}
		if got := vr.Applies(tc.version); got != tc.want {
			t.Errorf("range %q version %q = %v, want %v", tc.rng, tc.version, got, tc.want)
		}
	}
}

func TestForPrefersNarrowerRange(t *testing.T) {
	dir := t.TempDir()
	broad := strings.Replace(minimalPack, "name: testware-core, version: 1.0.0",
		"name: broad, version: 1.0.0, versionRange: \">=1.0\"", 1)
	narrow := strings.Replace(minimalPack, "name: testware-core, version: 1.0.0",
		"name: narrow, version: 1.0.0, versionRange: \">=2.0 <3.0\"", 1)
	for name, body := range map[string]string{"broad.yaml": broad, "narrow.yaml": narrow} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := Load(nil, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	p, err := lib.For(core.MiddlewareKind("testware"), "2.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Name != "narrow" {
		t.Fatalf("chose %q; the more specific range should win", p.Metadata.Name)
	}
	p2, err := lib.For(core.MiddlewareKind("testware"), "5.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Metadata.Name != "broad" {
		t.Fatalf("chose %q; only the broad range applies at 5.0.0", p2.Metadata.Name)
	}
}

func TestNoPackIsCoded(t *testing.T) {
	lib, _ := LoadDefault(nil)
	if _, err := lib.For(core.MiddlewareKind("nothing-here"), ""); errs.CodeOf(err) != "MAS-5003" {
		t.Fatalf("got %v, want MAS-5003", err)
	}
}

func TestMatchingPlaybooksRanksSpecificity(t *testing.T) {
	lib, _ := LoadDefault(nil)
	p, _ := lib.For(core.KindRedis, "")
	got := p.MatchingPlaybooks("p99 latency spike with evictions and oom errors")
	if len(got) < 2 {
		t.Fatalf("expected several playbooks, got %d", len(got))
	}
	// The memory playbook matches three terms; it must outrank single matches.
	if got[0].ID != "redis.memory-pressure" {
		t.Fatalf("first playbook = %s, want redis.memory-pressure", got[0].ID)
	}
	// Ordering must be stable so identical inputs give identical reports.
	again := p.MatchingPlaybooks("p99 latency spike with evictions and oom errors")
	for i := range got {
		if got[i].ID != again[i].ID {
			t.Fatal("playbook selection is not deterministic")
		}
	}
}

func TestMatchingPlaybooksHandlesChineseSymptoms(t *testing.T) {
	lib, _ := LoadDefault(nil)
	p, _ := lib.For(core.KindRedis, "")
	got := p.MatchingPlaybooks("内存告警，出现驱逐")
	found := false
	for _, pb := range got {
		if pb.ID == "redis.memory-pressure" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a Chinese symptom did not select the memory playbook: %v", ids(got))
	}
}

func ids(pbs []*Playbook) []string {
	out := make([]string, 0, len(pbs))
	for _, p := range pbs {
		out = append(out, p.ID)
	}
	return out
}

func TestExpandSignals(t *testing.T) {
	lib, _ := LoadDefault(nil)
	p, _ := lib.For(core.KindRedis, "")

	out, err := p.ExpandSignals(map[string]any{"query": "{{signal:memory_used}}"}, `{instance="redis-0"}`)
	if err != nil {
		t.Fatal(err)
	}
	got := out["query"].(string)
	if got != `redis_memory_used_bytes{instance="redis-0"}` {
		t.Fatalf("expanded to %q", got)
	}

	empty, err := p.ExpandSignals(map[string]any{"query": "{{signal:memory_used}}"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if empty["query"].(string) != "redis_memory_used_bytes" {
		t.Fatalf("an empty selector should leave a bare metric name, got %q", empty["query"])
	}

	if _, err := p.ExpandSignals(map[string]any{"query": "{{signal:ghost}}"}, ""); errs.CodeOf(err) != "MAS-5012" {
		t.Fatalf("got %v, want MAS-5012", err)
	}
}

func TestLogPatternMatching(t *testing.T) {
	lib, _ := LoadDefault(nil)
	p, _ := lib.For(core.KindRedis, "")
	hits := p.MatchLogPatterns("1:M 23 Aug 2026 10:00:00.000 # OOM command not allowed when used memory > 'maxmemory'.")
	if len(hits) == 0 || hits[0].ID != "oom_command_not_allowed" {
		t.Fatalf("log pattern not matched: %+v", hits)
	}
	if len(p.MatchLogPatterns("nothing interesting here")) != 0 {
		t.Error("an ordinary line matched a pattern")
	}
}

func TestSummaryGroundsAModel(t *testing.T) {
	lib, _ := LoadDefault(nil)
	p, _ := lib.For(core.KindRedis, "")
	for _, lang := range []string{"en", "zh"} {
		s := p.Summary(lang)
		for _, want := range []string{"memory-pressure", "redis_memory_used_bytes", "oom_command_not_allowed"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s summary is missing %q", lang, want)
			}
		}
	}
	if p.Summary("en") == p.Summary("zh") {
		t.Error("the Chinese summary is identical to the English one")
	}
}

func TestTextFallsBackToEnglish(t *testing.T) {
	tx := Text{EN: "only english"}
	if tx.In("zh") != "only english" {
		t.Fatal("a partially translated third-party pack should still render")
	}
	if (Text{}).In("en") != "" {
		t.Fatal("an empty text should render empty")
	}
}
