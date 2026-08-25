package audit_test

import (
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
)

const module = "github.com/zlrrr/multi-agent-system-turbo"

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// goPackages walks the repository and returns each package's import path
// (module-relative) and its non-test imports.
func goPackages(t *testing.T) map[string][]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string][]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		name := info.Name()
		if name == ".git" || name == "bin" || name == "dist" || name == "vendor" || name == "testdata" {
			return filepath.SkipDir
		}
		p, ierr := build.ImportDir(path, 0)
		if ierr != nil {
			return nil // not a Go package
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = p.Imports
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// reasoningPackages must reach the outside world only through the guarded tool
// path. If any of them opened a socket or ran a process directly, the guard
// would no longer be the single choke point Art. IV.1 requires.
var reasoningPackages = []string{
	"internal/core",
	"internal/knowledge",
	"internal/rules",
	"internal/agent",
	"internal/orchestrator",
	"internal/report",
	"internal/safety",
}

var forbiddenIOImports = []string{"net/http", "os/exec", "net", "net/url"}

// TestNoUnguardedIO proves the reasoning layer performs no I/O of its own.
func TestNoUnguardedIO(t *testing.T) {
	pkgs := goPackages(t)
	// net/url is parsing, not I/O; safety needs it to inspect URLs.
	allowedException := map[string]map[string]bool{
		"internal/safety": {"net/url": true},
		"internal/rules":  {"net/url": true},
	}
	for _, pkg := range reasoningPackages {
		imports, ok := pkgs[pkg]
		if !ok {
			t.Logf("%s not present yet; skipping", pkg)
			continue
		}
		for _, imp := range imports {
			for _, forbidden := range forbiddenIOImports {
				if imp != forbidden {
					continue
				}
				if allowedException[pkg][imp] {
					continue
				}
				t.Errorf("%s imports %s: the reasoning layer must reach the outside world only through internal/tool", pkg, imp)
			}
		}
	}
}

// TestReasoningLayerUsesToolsNotCollectors proves the reasoning layer cannot
// bypass the invoker by calling a collector client directly.
func TestReasoningLayerUsesToolsNotCollectors(t *testing.T) {
	pkgs := goPackages(t)
	effectPrefixes := []string{
		module + "/internal/collector/",
		module + "/internal/envadapter/",
		module + "/internal/source",
	}
	for _, pkg := range reasoningPackages {
		imports, ok := pkgs[pkg]
		if !ok {
			continue
		}
		for _, imp := range imports {
			for _, prefix := range effectPrefixes {
				if strings.HasPrefix(imp, prefix) {
					t.Errorf("%s imports %s directly; evidence must be requested through tool.Invoker", pkg, imp)
				}
			}
		}
	}
}

// TestOnlyDesignatedPackagesRunProcesses keeps process execution confined to the
// two packages whose designs declare it (LLD §2.9, §2.10).
func TestOnlyDesignatedPackagesRunProcesses(t *testing.T) {
	// The two adapters below are the only packages a diagnosis can reach that
	// run a process, and both do so through the guard. internal/sdd and
	// internal/audit are repository tooling: they never run during a diagnosis
	// and never touch a target environment.
	allowed := map[string]bool{
		"internal/envadapter/local": true,
		"internal/source":           true,
		"internal/sdd":              true,
		"internal/audit":            true,
	}
	for pkg, imports := range goPackages(t) {
		if allowed[pkg] {
			continue
		}
		for _, imp := range imports {
			if imp == "os/exec" {
				t.Errorf("%s imports os/exec; process execution belongs to the guarded adapters only", pkg)
			}
		}
	}
}

// TestNoShellInvocation proves the claim in design-hld.md §7.3.2: commands are
// built as argument vectors and never handed to a shell, so argument content
// cannot become executable syntax.
func TestNoShellInvocation(t *testing.T) {
	root := repoRoot(t)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`exec\.Command(?:Context)?\(\s*(?:ctx\s*,\s*)?"(?:/bin/)?(?:sh|bash|zsh|dash|cmd|powershell)"`),
		regexp.MustCompile(`"/bin/sh"`),
		regexp.MustCompile(`"-c",\s*fmt\.Sprintf`),
		regexp.MustCompile(`os/exec".*Shell`),
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "bin", "dist", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "structural_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, re := range patterns {
			if loc := re.FindIndex(b); loc != nil {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s invokes a shell: %q", rel, string(b[loc[0]:loc[1]]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestGuardIsTheOnlyAuthorizer proves nothing outside internal/safety defines an
// alternative authorisation entry point that callers might use instead.
func TestGuardIsTheOnlyAuthorizer(t *testing.T) {
	root := repoRoot(t)
	re := regexp.MustCompile(`func\s+(?:\([^)]*\)\s*)?Authorize\(`)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			if info != nil && info.IsDir() && (info.Name() == ".git" || info.Name() == "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(filepath.ToSlash(rel), "internal/safety/") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if re.Match(b) {
			t.Errorf("%s defines its own Authorize; there must be exactly one guard", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTopologiesOnlyOrchestrate is feature 003's structural guarantee
// (NFR-001, NFR-005). A topology is a control flow over roles: it decides who
// acts and in what order. If it could reach a tool directly, or build its own
// registry or invoker, then two topologies could differ in *capability* — and a
// difference in their output would no longer be attributable to the
// architecture, which is the only reason to make them switchable.
func TestTopologiesOnlyOrchestrate(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "internal", "orchestrator")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Constructing a registry or an invoker, or invoking a tool by name, are the
	// three ways a topology could give itself capability its peers lack.
	forbidden := map[string]string{
		`tool\.NewRegistry\(`: "a topology must use the registry the run handed it, not build its own",
		`tool\.NewInvoker\(`:  "a topology must use the invoker the run handed it, not build its own",
		`\.Invoke\(`:          "a topology must reach tools through a role, never call one itself",
		`safety\.NewGuard\(`:  "no topology may construct a guard; the run's guard is the only one",
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // repository path
		if rerr != nil {
			t.Fatal(rerr)
		}
		for pattern, why := range forbidden {
			if regexp.MustCompile(pattern).Match(body) {
				t.Errorf("internal/orchestrator/%s matches %q: %s", e.Name(), pattern, why)
			}
		}
	}
}

// TestExecIsReachableOnlyThroughTheGuard is feature 004's structural guarantee.
//
// The Kubernetes exec subresource is deliberately absent from the guard's
// general path allow-list, so it can be reached only by declaring an
// ExecEffect — the effect the guard also checks against the command allow-list.
// A package that built the exec URL for itself would sidestep that, so no
// package outside the kube adapter may name the subresource at all, and the
// adapter may name it only in the one file that owns the client.
func TestExecIsReachableOnlyThroughTheGuard(t *testing.T) {
	root := repoRoot(t)
	// Where the exec path may legitimately appear: the client that builds it,
	// the guard that validates it, and tests.
	allowed := map[string]bool{
		"internal/envadapter/kube/exec.go": true,
		"internal/safety/guard.go":         true,
	}
	re := regexp.MustCompile(`pods/[^"]*?/exec|"/exec"|\+ "/exec"`)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() &&
				(info.Name() == ".git" || info.Name() == "bin" || info.Name() == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(mustRel(t, root, path))
		if allowed[rel] {
			return nil
		}
		body, rerr := os.ReadFile(path) //nolint:gosec // repository path
		if rerr != nil {
			return nil
		}
		if re.Match(body) {
			t.Errorf("%s builds a Kubernetes exec URL; exec must be reached through an "+
				"ExecEffect so the guard checks the command as well as the endpoint", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoKubectlInTheAllowList pins CON-002. Allow-listing kubectl would put the
// whole Kubernetes API behind one binary name, which is the failure
// deny-by-default exists to prevent — and it is the cheap shortcut a future
// contributor will reach for the next time exec is inconvenient.
func TestNoKubectlInTheAllowList(t *testing.T) {
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range g.AllowedCommands() {
		for _, banned := range []string{"kubectl", "oc", "helm", "kubeadm", "crictl", "docker", "nerdctl"} {
			if rule.Binary == banned {
				t.Errorf("%q is allow-listed; one binary must not stand in for a whole API", banned)
			}
		}
	}
}

func mustRel(t *testing.T, base, target string) string {
	t.Helper()
	rel, err := filepath.Rel(base, target)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}
