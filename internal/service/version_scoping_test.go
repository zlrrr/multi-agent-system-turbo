package service_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/httpapi"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
)

// scopedTestPack has one playbook that applies to every version and one that
// applies only below 4.0, so a diagnosis at 4.0.1 must run the first and not
// the second.
const scopedTestPack = `
apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata: { middleware: versionware, name: versionware-core, version: 1.0.0 }
signals:
  - id: up
    promql: 'versionware_up{{.selector}}'
    unit: bool
    description: { en: "up", zh: "在线" }
  - id: legacy_sessions
    versionRange: "<4.0"
    promql: 'versionware_legacy_sessions{{.selector}}'
    unit: count
    description: { en: "legacy sessions", zh: "旧版会话" }
logPatterns:
  - id: legacy_session_lost
    versionRange: "<4.0"
    regex: 'legacy session expired'
    severity: major
    meaning: { en: "a legacy session was lost", zh: "一个旧版会话丢失了" }
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
  - id: versionware.availability
    title: { en: "Availability", zh: "可用性" }
    steps:
      - id: collect-up
        collect: { tool: promql.range, args: { query: "{{signal:up}}" }, as: up }
      - id: conclude-down
        conclude: { failureMode: down, when: "not up.empty and up.latest < 1" }
  - id: versionware.sessions
    title: { en: "Sessions", zh: "会话" }
    steps:
      - id: collect-sessions
        collect: { tool: promql.range, args: { query: "{{signal:legacy_sessions}}" }, as: sessions }
      - id: conclude-sessions
        conclude: { failureMode: session-loss, when: "not sessions.empty" }
`

func packDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "versionware.yaml"), []byte(scopedTestPack), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDiagnosisUsesTheResolvedPack is FR-012, checked from both ends.
//
// Behaviourally: a run at 4.0.1 must not reach a conclusion whose rule the
// version excludes, and must say what it skipped. Structurally: the pack the
// run holds must have come from Resolve, so scoping cannot be bypassed by a
// caller that forgets — which is the failure mode every alternative shape in
// plan.md §1 was rejected for.
func TestDiagnosisUsesTheResolvedPack(t *testing.T) {
	for _, c := range []struct {
		version    string
		wantMode   string
		bannedMode string
	}{
		{"3.9.0", "session-loss", ""},
		{"4.0.1", "", "session-loss"},
	} {
		t.Run(c.version, func(t *testing.T) {
			dir := packDir(t)
			svc := newService(t, newStubs(t, 0.99, true), func(cfg *config.Config) {
				cfg.Knowledge.PackDirs = []string{dir}
				cfg.Targets = append(cfg.Targets, config.TargetConfig{
					ID: "versionware-prod", Kind: "versionware", Version: c.version,
					Labels: map[string]string{"instance": "vw-0"},
				})
			})

			req := request()
			req.Target = "versionware-prod"
			req.Symptom = "sessions are being lost"
			rep, err := svc.Diagnose(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}

			concluded := strings.Join(rep.Conclusions, ",")
			if c.wantMode != "" && !strings.Contains(concluded, c.wantMode) {
				t.Errorf("version %s should have concluded %s, got %v",
					c.version, c.wantMode, rep.Conclusions)
			}
			if c.bannedMode != "" && strings.Contains(concluded, c.bannedMode) {
				t.Errorf("version %s concluded %s, whose rules it excludes",
					c.version, c.bannedMode)
			}

			// The skip has to be visible. A check that vanished without a trace
			// reads, in a report, exactly like a check that passed.
			if c.bannedMode == "" {
				return
			}
			var scoping *core.Gap
			for i := range rep.Gaps {
				if rep.Gaps[i].Code == "MAS-5019" {
					scoping = &rep.Gaps[i]
				}
			}
			if scoping == nil {
				t.Fatalf("the run skipped version-scoped rules and said nothing: %+v", rep.Gaps)
			}
			if !strings.Contains(scoping.Detail, "versionware.sessions") {
				t.Errorf("the gap does not name the skipped playbook: %q", scoping.Detail)
			}
			if scoping.Reason != core.GapNotApplicable {
				t.Errorf("reason %q, want %q", scoping.Reason, core.GapNotApplicable)
			}
		})
	}
}

// TestServiceResolvesBeforeUse is the structural half of FR-012: the identifier
// bound from the library must be reassigned from a Resolve call in the same
// function, so no later use can reach an unresolved pack.
func TestServiceResolvesBeforeUse(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var boundFrom string // the identifier `library.For` assigns to
	var resolvedInto []string

	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "For":
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "library" {
				if id, ok := assign.Lhs[0].(*ast.Ident); ok {
					boundFrom = id.Name
				}
			}
		case "Resolve":
			for _, lhs := range assign.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					resolvedInto = append(resolvedInto, id.Name)
				}
			}
		}
		return true
	})

	if boundFrom == "" {
		t.Fatal("no assignment from s.library.For was found; this test needs updating")
	}
	for _, name := range resolvedInto {
		if name == boundFrom {
			return
		}
	}
	t.Errorf("%q comes from s.library.For and is never reassigned from Resolve, "+
		"so a later use reaches an unresolved pack and version scoping is bypassed",
		boundFrom)
}

// TestDoctorReportsAPIExposure is FR-014 of feature 009. An operator should be
// able to see what protects the API before they try to start the server, not
// after it refuses.
func TestDoctorReportsAPIExposure(t *testing.T) {
	find := func(results []service.CheckResult) service.CheckResult {
		t.Helper()
		for _, r := range results {
			if r.Name == "api exposure" {
				return r
			}
		}
		t.Fatal("mas doctor does not report the API's exposure at all")
		return service.CheckResult{}
	}

	// Loopback with nothing configured is fine, and says why.
	svc := newService(t, newStubs(t, 0.5, false), func(cfg *config.Config) {
		cfg.Server.Addr = "127.0.0.1:8080"
	})
	got := find(svc.Doctor(context.Background()))
	if got.Status != service.CheckOK {
		t.Errorf("a loopback bind was graded %q: %s", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "loopback") {
		t.Errorf("the detail does not say the API is loopback-only: %q", got.Detail)
	}

	// Off-host with nothing configured is a failure, not a warning: it is the
	// same judgement that will stop the server from starting.
	svc = newService(t, newStubs(t, 0.5, false), func(cfg *config.Config) {
		cfg.Server.Addr = "0.0.0.0:8080"
	})
	got = find(svc.Doctor(context.Background()))
	if got.Status != service.CheckWarn {
		t.Errorf("an unauthenticated public bind was graded %q, want warn: %s",
			got.Status, got.Detail)
	}
	// Warn rather than fail, because most runs never open a listener — but the
	// detail has to say plainly that serving is what will be refused, or the
	// warning reads as optional.
	if !strings.Contains(got.Detail, "refuse") {
		t.Errorf("the warning does not say `mas serve` will refuse: %q", got.Detail)
	}

	// Configured properly, it says who may do what — by name and scope, never
	// by credential.
	svc = newService(t, newStubs(t, 0.5, false), func(cfg *config.Config) {
		cfg.Server.Addr = "0.0.0.0:8080"
		cfg.Server.TLS = config.ServerTLS{TerminatedByProxy: true}
		cfg.Server.Auth = config.ServerAuth{Tokens: []config.APIToken{
			{Name: "dashboard", Token: "s3cret-value", Scopes: []string{"read"}},
			{Name: "oncall", Token: "other-s3cret", Scopes: []string{"read", "diagnose"}},
		}}
	})
	got = find(svc.Doctor(context.Background()))
	if got.Status != service.CheckOK {
		t.Errorf("a properly configured API was graded %q: %s", got.Status, got.Detail)
	}
	for _, want := range []string{"dashboard", "oncall", "diagnose", "proxy"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("the detail does not mention %s: %q", want, got.Detail)
		}
	}
	for _, banned := range []string{"s3cret-value", "other-s3cret"} {
		if strings.Contains(got.Detail, banned) {
			t.Errorf("mas doctor printed a credential: %q", got.Detail)
		}
	}
}

// TestDoctorAndAdmitAgreeOnLoopback pins the one duplication feature 009
// leaves behind. `internal/httpapi` imports `internal/service`, so the address
// test cannot be shared without a cycle — and two copies of a security
// predicate that disagree is worse than one copy in the wrong place.
func TestDoctorAndAdmitAgreeOnLoopback(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.9:1",
		"0.0.0.0:8080", ":8080", "[::]:8080", "10.0.0.1:8080", "",
	} {
		svc := newService(t, newStubs(t, 0.5, false), func(cfg *config.Config) {
			cfg.Server.Addr = addr
		})
		var detail string
		for _, r := range svc.Doctor(context.Background()) {
			if r.Name == "api exposure" {
				detail = r.Detail
			}
		}
		doctorSaysLoopback := strings.Contains(detail, "loopback only")

		// Admit permits an unauthenticated bind exactly when it is loopback.
		admitAllows := httpapi.Admit(config.ServerConfig{Addr: addr}) == nil

		if doctorSaysLoopback != admitAllows {
			t.Errorf("%q: doctor says loopback=%v but Admit allows unauthenticated=%v",
				addr, doctorSaysLoopback, admitAllows)
		}
	}
}
