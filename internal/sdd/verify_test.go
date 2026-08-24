package sdd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/sdd"
)

// scaffold builds a minimal, valid SDD tree so each test can break exactly one
// thing and see whether the verifier notices.
func scaffold(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"docs/en/project-goals.md":           "# Goals\n",
		"docs/zh/project-goals.md":           "# 目标\n",
		".specify/memory/constitution.md":    "# Constitution\n",
		".specify/memory/constitution.zh.md": "# 宪章\n",
		"specs/001-demo/spec.md": `# Spec

> **Version**: 1.0.0

| ID | Requirement |
|---|---|
| FR-001 | The system MUST work |
| NFR-001 | It MUST be fast |
`,
		"specs/001-demo/spec.zh.md": "# 规格\n\n> **版本**：1.0.0\n",
		"specs/001-demo/tasks.md": `# Tasks

> **Version**: 1.0.0

| ID | Task | Satisfies |
|---|---|---|
| T001 | build it | FR-001, NFR-001 |
`,
		"specs/001-demo/tasks.zh.md": "# 任务\n\n> **版本**：1.0.0\n",
		"specs/001-demo/traceability.yaml": `feature: 001-demo
artifacts:
  - id: spec
    path: spec.md
    version: 1.0.0
    upstream: null
    derived_from_version: null
  - id: tasks
    path: tasks.md
    version: 1.0.0
    upstream: spec
    derived_from_version: 1.0.0
`,
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func verify(t *testing.T, root string) *sdd.Report {
	t.Helper()
	r, err := sdd.Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func findings(r *sdd.Report, check string) []sdd.Finding {
	var out []sdd.Finding
	for _, f := range r.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func TestCleanTreePasses(t *testing.T) {
	r := verify(t, scaffold(t))
	if r.Failed() {
		var b bytes.Buffer
		r.Print(&b)
		t.Fatalf("a valid tree failed verification:\n%s", b.String())
	}
	for _, check := range []string{"parity", "cascade", "coverage"} {
		if r.Checked[check] == 0 {
			t.Errorf("the %s check inspected nothing", check)
		}
	}
}

func TestParityDetectsMissingZH(t *testing.T) {
	root := scaffold(t)
	if err := os.Remove(filepath.Join(root, "specs/001-demo/spec.zh.md")); err != nil {
		t.Fatal(err)
	}
	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("a missing Chinese counterpart was not detected")
	}
	found := false
	for _, f := range findings(r, "parity") {
		if strings.Contains(f.Path, "spec.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestParityDetectsMissingEN(t *testing.T) {
	root := scaffold(t)
	if err := os.Remove(filepath.Join(root, "docs/en/project-goals.md")); err != nil {
		t.Fatal(err)
	}
	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("a missing English counterpart in docs/ was not detected")
	}
}

func TestStalenessDetected(t *testing.T) {
	root := scaffold(t)
	// The spec advances; the tasks were never re-reviewed against it.
	tr := filepath.Join(root, "specs/001-demo/traceability.yaml")
	body, err := os.ReadFile(tr)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(body), `    version: 1.0.0
    upstream: null`, `    version: 2.0.0
    upstream: null`, 1)
	if err := os.WriteFile(tr, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(root, "specs/001-demo/spec.md")
	sb, _ := os.ReadFile(spec)
	if err := os.WriteFile(spec, []byte(strings.Replace(string(sb), "1.0.0", "2.0.0", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("a stale downstream artifact was not detected")
	}
	found := false
	for _, f := range findings(r, "cascade") {
		if strings.Contains(f.Message, "stale") && strings.Contains(f.Message, "tasks") {
			found = true
			if !strings.Contains(f.Message, "amend") {
				t.Error("the staleness finding does not say how to resolve it")
			}
		}
	}
	if !found {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestVersionMismatchDetected(t *testing.T) {
	root := scaffold(t)
	spec := filepath.Join(root, "specs/001-demo/spec.md")
	body, _ := os.ReadFile(spec)
	if err := os.WriteFile(spec, []byte(strings.Replace(string(body), "1.0.0", "1.4.0", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("a document whose version disagrees with traceability.yaml was accepted")
	}
}

func TestCoverageGap(t *testing.T) {
	root := scaffold(t)
	tasks := filepath.Join(root, "specs/001-demo/tasks.md")
	body, _ := os.ReadFile(tasks)
	if err := os.WriteFile(tasks, []byte(strings.Replace(string(body), ", NFR-001", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("an uncovered requirement was not detected")
	}
	found := false
	for _, f := range findings(r, "coverage") {
		if strings.Contains(f.Message, "NFR-001") {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestUnknownRequirementDetected(t *testing.T) {
	root := scaffold(t)
	tasks := filepath.Join(root, "specs/001-demo/tasks.md")
	body, _ := os.ReadFile(tasks)
	if err := os.WriteFile(tasks, []byte(strings.Replace(string(body), "FR-001,", "FR-001, FR-999,", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("a task citing a non-existent requirement was accepted")
	}
	found := false
	for _, f := range findings(r, "coverage") {
		if strings.Contains(f.Message, "FR-999") {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestMissingTraceabilityDetected(t *testing.T) {
	root := scaffold(t)
	if err := os.Remove(filepath.Join(root, "specs/001-demo/traceability.yaml")); err != nil {
		t.Fatal(err)
	}
	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("a feature without traceability.yaml was accepted")
	}
}

func TestMissingTasksDetected(t *testing.T) {
	root := scaffold(t)
	if err := os.Remove(filepath.Join(root, "specs/001-demo/tasks.md")); err != nil {
		t.Fatal(err)
	}
	r := verify(t, root)
	if !r.Failed() {
		t.Fatal("a spec with no tasks was accepted")
	}
}

func TestAmendStampsAndRestamps(t *testing.T) {
	root := scaffold(t)
	if err := sdd.Amend(root, []string{"--feature", "001-demo", "--artifact", "spec", "--version", "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "specs/001-demo/traceability.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, "version: 2.0.0") {
		t.Fatalf("the artifact was not stamped:\n%s", s)
	}
	if !strings.Contains(s, "derived_from_version: 2.0.0") {
		t.Fatalf("the downstream was not re-stamped:\n%s", s)
	}
	if !strings.Contains(s, "Cascade tracking") {
		t.Error("the explanatory header was lost")
	}
}

func TestAmendRejectsUnknownArtifact(t *testing.T) {
	root := scaffold(t)
	err := sdd.Amend(root, []string{"--feature", "001-demo", "--artifact", "ghost", "--version", "2.0.0"})
	if err == nil {
		t.Fatal("amending an unknown artifact was accepted")
	}
	if !strings.Contains(err.Error(), "have:") {
		t.Errorf("the error does not list the valid artifacts: %v", err)
	}
}

func TestAmendRequiresAllFlags(t *testing.T) {
	root := scaffold(t)
	if err := sdd.Amend(root, []string{"--feature", "001-demo"}); err == nil {
		t.Fatal("amend ran without --artifact and --version")
	}
}

// TestThisRepositoryPasses is the check that actually gates CI: the project's
// own specification chain must satisfy its own rules.
func TestThisRepositoryPasses(t *testing.T) {
	root := sdd.RepoRoot()
	if _, err := os.Stat(filepath.Join(root, ".specify")); err != nil {
		t.Skip("not running inside the repository")
	}
	r := verify(t, root)
	if r.Failed() {
		var b bytes.Buffer
		r.Print(&b)
		t.Fatalf("this repository fails its own SDD checks:\n%s", b.String())
	}
}
