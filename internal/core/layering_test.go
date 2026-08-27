package core_test

import (
	"go/build"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/zlrrr/multi-agent-system-turbo"

// TestNoUpwardImports enforces the layering rule from design-hld.md §3:
// core may import only pkg/errs from this repository, and the foundation
// packages may never import the layers above them.
func TestNoUpwardImports(t *testing.T) {
	rules := map[string][]string{
		"internal/core":   {module + "/pkg/errs"},
		"pkg/errs":        {},
		"internal/config": {module + "/pkg/errs", module + "/internal/core"},
	}
	root := repoRoot(t)
	for pkg, allowed := range rules {
		dir := filepath.Join(root, pkg)
		p, err := build.ImportDir(dir, 0)
		if err != nil {
			if strings.Contains(err.Error(), "no buildable Go source files") ||
				strings.Contains(err.Error(), "cannot find package") {
				t.Logf("%s not present yet; skipping", pkg)
				continue
			}
			t.Fatalf("%s: %v", pkg, err)
		}
		for _, imp := range append(p.Imports, p.TestImports...) {
			if !strings.HasPrefix(imp, module) {
				continue // standard library or third party
			}
			if imp == module+"/"+pkg {
				continue
			}
			if !contains(allowed, imp) {
				t.Errorf("%s imports %s, which breaks the layering rule (allowed: %v)", pkg, imp, allowed)
			}
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
