package knowledge_test

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
)

// floor is the minimum a shipped pack must reach. It is deliberately written
// before the packs it governs: a floor derived from what the packs happen to
// contain would measure nothing.
//
// The numbers are justified in specs/002-middleware-packs/design-hld.md §3.
type floor struct {
	middleware    string
	minSignals    int
	minPatterns   int
	minModes      int
	minPlaybooks  int
	requiredModes []string
}

// floors covers every middleware this project ships knowledge for. Adding a
// pack without adding its floor fails TestEveryShippedPackHasAFloor, so the
// contract cannot be skipped by omission.
var floors = []floor{
	{
		middleware: "redis", minSignals: 10, minPatterns: 6, minModes: 6, minPlaybooks: 3,
		requiredModes: []string{
			"memory-pressure", "eviction-storm", "connection-saturation",
			"slow-commands", "persistence-failure", "replication-broken",
		},
	},
	{
		middleware: "kafka", minSignals: 10, minPatterns: 6, minModes: 6, minPlaybooks: 3,
		requiredModes: []string{
			"under-replicated-partitions", "offline-partitions", "consumer-lag-growth",
			"broker-loss", "controller-instability", "broker-disk-latency",
		},
	},
	{
		middleware: "mongodb", minSignals: 10, minPatterns: 6, minModes: 6, minPlaybooks: 3,
		requiredModes: []string{
			"replication-lag", "primary-election", "connection-saturation",
			"slow-queries", "lock-contention", "storage-pressure", "write-concern-stall",
		},
	},
	{
		middleware: "pulsar", minSignals: 10, minPatterns: 6, minModes: 6, minPlaybooks: 3,
		requiredModes: []string{
			"subscription-backlog", "bookie-storage-pressure", "ledger-write-failure",
			"broker-overload", "topic-unavailable", "consumer-stall",
		},
	},
	{
		middleware: "milvus", minSignals: 10, minPatterns: 6, minModes: 6, minPlaybooks: 3,
		requiredModes: []string{
			"query-node-latency", "compaction-backlog", "index-build-failure",
			"memory-pressure", "flush-lag", "dependency-failure",
		},
	},
	{
		middleware: "oceanbase", minSignals: 10, minPatterns: 6, minModes: 6, minPlaybooks: 3,
		requiredModes: []string{
			"tenant-memory-exhaustion", "tenant-cpu-throttling", "major-merge-delay",
			"slow-sql", "clog-sync-lag", "disk-pressure",
		},
	},
}

func loadLibrary(t *testing.T) *knowledge.Library {
	t.Helper()
	lib, err := knowledge.LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Problems()) > 0 {
		t.Fatalf("packs failed to load: %v", lib.Problems())
	}
	return lib
}

func packFor(t *testing.T, lib *knowledge.Library, middleware string) *knowledge.Pack {
	t.Helper()
	for _, p := range lib.All() {
		if p.Metadata.Middleware == middleware {
			return p
		}
	}
	t.Fatalf("no pack ships for %s", middleware)
	return nil
}

// TestEveryShippedPackHasAFloor prevents a pack from escaping the contract by
// simply not being listed.
func TestEveryShippedPackHasAFloor(t *testing.T) {
	governed := map[string]bool{}
	for _, f := range floors {
		governed[f.middleware] = true
	}
	for _, p := range loadLibrary(t).All() {
		if !governed[p.Metadata.Middleware] {
			t.Errorf("pack %s ships without a conformance floor; add one to floors", p.ID())
		}
	}
}

func TestPackConformance(t *testing.T) {
	lib := loadLibrary(t)
	for _, f := range floors {
		t.Run(f.middleware, func(t *testing.T) {
			p := packFor(t, lib, f.middleware)

			if n := len(p.Signals); n < f.minSignals {
				t.Errorf("%d signals, floor is %d: below this a playbook cannot separate cause from effect",
					n, f.minSignals)
			}
			if n := len(p.LogPatterns); n < f.minPatterns {
				t.Errorf("%d log patterns, floor is %d: logs are where a middleware states its own diagnosis",
					n, f.minPatterns)
			}
			if n := len(p.FailureModes); n < f.minModes {
				t.Errorf("%d failure modes, floor is %d: fewer cannot express the alternatives a critic must weigh",
					n, f.minModes)
			}
			if n := len(p.Playbooks); n < f.minPlaybooks {
				t.Errorf("%d playbooks, floor is %d", n, f.minPlaybooks)
			}

			for _, id := range f.requiredModes {
				if _, ok := p.FailureMode(id); !ok {
					t.Errorf("the specification names failure mode %q, which this pack does not declare", id)
				}
			}

			// Exactly one always-on playbook, so something runs whatever the
			// operator typed (design-hld.md §4).
			alwaysOn := 0
			for _, pb := range p.Playbooks {
				if len(pb.Matches) == 0 {
					alwaysOn++
				}
			}
			if alwaysOn != 1 {
				t.Errorf("%d always-on playbooks, want exactly 1: one health check must run for any symptom", alwaysOn)
			}
		})
	}
}

func TestPackAdviceIsPresentAndGraded(t *testing.T) {
	lib := loadLibrary(t)
	for _, f := range floors {
		t.Run(f.middleware, func(t *testing.T) {
			p := packFor(t, lib, f.middleware)
			for _, mode := range p.FailureModes {
				if len(mode.Recommendations) == 0 {
					t.Errorf("failure mode %q has no recommendation: it tells an operator they have a problem and nothing else",
						mode.ID)
				}
				for i, r := range mode.Recommendations {
					switch r.Risk {
					case "low", "medium", "high":
					default:
						t.Errorf("%s.recommendations[%d] risk %q is not low, medium or high", mode.ID, i, r.Risk)
					}
				}
			}
		})
	}
}

// performedPhrasing catches advice written as something already done. A report
// that reads as an action log would misrepresent a read-only system (CON-003).
var performedPhrasing = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(restarted|scaled|applied|deleted|removed|fixed|increased|decreased|disabled|enabled|updated|set|configured|rebalanced|failed over)\s`),
	regexp.MustCompile(`(?i)\b(we|i) (have |already )?(restarted|scaled|applied|fixed|changed|updated)\b`),
	regexp.MustCompile(`已(经)?(重启|扩容|缩容|应用|删除|修复|调整|禁用|启用|设置|配置)`),
	regexp.MustCompile(`^(我们|系统)(已|已经)`),
}

func TestPackRecommendationsAreAdvisory(t *testing.T) {
	lib := loadLibrary(t)
	for _, f := range floors {
		t.Run(f.middleware, func(t *testing.T) {
			p := packFor(t, lib, f.middleware)
			for _, mode := range p.FailureModes {
				for i, r := range mode.Recommendations {
					for _, lang := range []string{"en", "zh"} {
						text := strings.TrimSpace(r.Statement.In(lang))
						for _, re := range performedPhrasing {
							if re.MatchString(text) {
								t.Errorf("%s.recommendations[%d] (%s) reads as an action already taken, "+
									"which a read-only system must never claim: %q", mode.ID, i, lang, text)
							}
						}
					}
				}
			}
		})
	}
}

// TestPackCoverageIsHonest enforces NFR-002: a pack may not advertise a failure
// mode it has nothing to detect with.
func TestPackCoverageIsHonest(t *testing.T) {
	lib := loadLibrary(t)
	for _, f := range floors {
		t.Run(f.middleware, func(t *testing.T) {
			p := packFor(t, lib, f.middleware)

			known := map[string]bool{}
			for _, s := range p.Signals {
				known[s.ID] = true
			}
			for _, l := range p.LogPatterns {
				known[l.ID] = true
			}

			for _, mode := range p.FailureModes {
				if len(mode.Indicators) == 0 {
					t.Errorf("failure mode %q declares no indicators, so nothing detects it", mode.ID)
					continue
				}
				grounded := false
				for _, ind := range mode.Indicators {
					for id := range known {
						if strings.Contains(ind, id) {
							grounded = true
							break
						}
					}
					if grounded {
						break
					}
				}
				if !grounded {
					t.Errorf("failure mode %q names indicators %v, none of which reference a declared "+
						"signal or log pattern: the pack advertises coverage it does not have",
						mode.ID, mode.Indicators)
				}
			}
		})
	}
}

// TestPackInspectCommandsPassTheGuard proves a shipped pack never declares a
// command the guard would refuse. A pack that quietly needs a wider guard is the
// failure this check exists to prevent.
func TestPackInspectCommandsPassTheGuard(t *testing.T) {
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lib := loadLibrary(t)

	for _, p := range lib.All() {
		t.Run(p.Metadata.Middleware, func(t *testing.T) {
			for _, in := range p.InspectCommands() {
				// Substitute the templates the adapters fill in at call time.
				args := make([]string, 0, len(in.Args))
				for _, a := range in.Args {
					a = strings.ReplaceAll(a, "{{.host}}", "10.0.0.1")
					a = strings.ReplaceAll(a, "{{.port}}", "6379")
					args = append(args, a)
				}
				err := g.Authorize(ctx, safety.Call{
					Tool: "local.inspect", Class: safety.ClassReadOnly,
					Command: &safety.CommandEffect{Binary: in.Binary, Args: args},
				})
				if err != nil {
					t.Errorf("inspect %q (%s %s) is refused by the guard: %v",
						in.ID, in.Binary, strings.Join(args, " "), err)
				}
			}
		})
	}
}

// exprEnv mirrors the sandbox rules.Engine builds, so an expression that
// compiles here compiles at run time.
func exprEnv(pb knowledge.Playbook) map[string]any {
	type slot struct {
		Empty     bool               `expr:"empty"`
		Series    int                `expr:"series"`
		Count     int                `expr:"count"`
		Latest    float64            `expr:"latest"`
		Last      float64            `expr:"last"`
		LatestMin float64            `expr:"latestMin"`
		Min       float64            `expr:"min"`
		Max       float64            `expr:"max"`
		Avg       float64            `expr:"avg"`
		Sum       float64            `expr:"sum"`
		Delta     float64            `expr:"delta"`
		ByLabel   map[string]float64 `expr:"byLabel"`
		Summary   string             `expr:"summary"`
		Lines     []string           `expr:"lines"`
		Text      string             `expr:"text"`
	}
	env := map[string]any{
		"contains":      func(string, string) bool { return false },
		"matches":       func(string, string) bool { return false },
		"countMatching": func([]string, string) int { return 0 },
		"lower":         strings.ToLower,
		"ratio":         func(float64, float64) float64 { return 0 },
		"pct":           func(float64, float64) float64 { return 0 },
		"isNaN":         func(float64) bool { return false },
		"finite":        func(float64) bool { return true },
	}
	for _, st := range pb.Steps {
		if st.Collect != nil {
			env[st.Collect.As] = slot{}
		}
	}
	return env
}

func TestPackExpressionsCompile(t *testing.T) {
	lib := loadLibrary(t)
	for _, p := range lib.All() {
		t.Run(p.Metadata.Middleware, func(t *testing.T) {
			for _, pb := range p.Playbooks {
				env := exprEnv(pb)
				for _, st := range pb.Steps {
					for label, code := range map[string]string{
						"evaluate":      st.Evaluate,
						"conclude.when": concludeWhen(st),
					} {
						if strings.TrimSpace(code) == "" {
							continue
						}
						if _, err := expr.Compile(code, expr.Env(env), expr.AsBool()); err != nil {
							t.Errorf("%s/%s %s does not compile: %v\n  %s", pb.ID, st.ID, label, err, code)
						}
					}
				}
			}
		})
	}
}

func concludeWhen(st knowledge.Step) string {
	if st.Conclude == nil {
		return ""
	}
	return st.Conclude.When
}

// TestPackPlaybooksAreReachable catches a playbook that collects nothing, or
// that collects but never concludes — either way it burns tool calls for no
// diagnostic value.
func TestPackPlaybooksAreReachable(t *testing.T) {
	lib := loadLibrary(t)
	for _, p := range lib.All() {
		t.Run(p.Metadata.Middleware, func(t *testing.T) {
			declaredModes := map[string]bool{}
			for _, m := range p.FailureModes {
				declaredModes[m.ID] = true
			}
			for _, pb := range p.Playbooks {
				collects, outcomes := 0, 0
				for _, st := range pb.Steps {
					if st.Collect != nil {
						collects++
					}
					if st.Conclude != nil {
						outcomes++
						if !declaredModes[st.Conclude.FailureMode] {
							t.Errorf("%s/%s concludes undeclared failure mode %q",
								pb.ID, st.ID, st.Conclude.FailureMode)
						}
					}
					if st.OnTrue != nil && st.OnTrue.Finding != nil {
						outcomes++
					}
					if st.OnFalse != nil && st.OnFalse.Finding != nil {
						outcomes++
					}
				}
				if collects == 0 {
					t.Errorf("playbook %s collects no evidence", pb.ID)
				}
				if outcomes == 0 {
					t.Errorf("playbook %s reaches no finding or conclusion: it spends tool calls for nothing", pb.ID)
				}
			}
		})
	}
}

// TestPackThresholdsGuardAgainstEmpty is the subtle correctness check. A slot
// that collected zero series compares as 0, which reads as healthy. Every
// numeric comparison must therefore be guarded by an emptiness check on the same
// slot (design-lld.md §5).
func TestPackThresholdsGuardAgainstEmpty(t *testing.T) {
	comparison := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.(latest|last|latestMin|min|max|avg|sum|delta|count)\s*[<>]`)
	lib := loadLibrary(t)

	for _, p := range lib.All() {
		t.Run(p.Metadata.Middleware, func(t *testing.T) {
			for _, pb := range p.Playbooks {
				for _, st := range pb.Steps {
					for _, code := range []string{st.Evaluate, concludeWhen(st)} {
						if strings.TrimSpace(code) == "" {
							continue
						}
						for _, m := range comparison.FindAllStringSubmatch(code, -1) {
							slot := m[1]
							// The requirement is that the author considered
							// emptiness, not which way they resolved it:
							// `not up.empty and up.latest < 1` reads an empty
							// result as unknown, `up.empty or up.latest < 1`
							// reads it as down. Both are deliberate. A bare
							// comparison is not.
							if !strings.Contains(code, slot+".empty") {
								t.Errorf("%s/%s compares %s numerically without mentioning %s.empty; "+
									"a result with no series compares as zero and would read as healthy:\n  %s",
									pb.ID, st.ID, slot, slot, code)
							}
						}
					}
				}
			}
		})
	}
}

// TestEmbeddedPackSizeBudget keeps knowledge growth honest about its cost
// (NFR-003).
func TestEmbeddedPackSizeBudget(t *testing.T) {
	const budget = 512 * 1024
	total := 0
	fsys := knowledge.EmbeddedFS()
	entries, err := fs.ReadDir(fsys, "packs")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := fs.ReadFile(fsys, "packs/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
	}
	if total > budget {
		t.Fatalf("embedded packs total %d bytes, above the %d-byte budget", total, budget)
	}
	t.Logf("embedded packs: %d files, %d bytes (%.0f%% of budget)",
		len(entries), total, float64(total)/float64(budget)*100)
}

func TestSelectionRemainsDeterministicWithAllPacks(t *testing.T) {
	lib := loadLibrary(t)
	for _, p := range lib.All() {
		symptom := "latency spike with errors and backlog"
		first := playbookIDs(p.MatchingPlaybooks(symptom))
		for i := 0; i < 5; i++ {
			if got := playbookIDs(p.MatchingPlaybooks(symptom)); got != first {
				t.Fatalf("%s: selection is not deterministic: %q then %q", p.ID(), first, got)
			}
		}
	}
}

func playbookIDs(pbs []*knowledge.Playbook) string {
	out := make([]string, 0, len(pbs))
	for _, p := range pbs {
		out = append(out, p.ID)
	}
	return strings.Join(out, ",")
}
