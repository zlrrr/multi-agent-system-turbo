package sdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleTraceability = `# Cascade tracking (Constitution Art. II.2).
# ` + "`derived_from_version`" + ` records the upstream version this artifact was last reconciled
# against.
feature: 001-sample
artifacts:
  - id: hld
    path: design-hld.md
    version: 1.0.0
    upstream: plan
    derived_from_version: 1.0.0
  - id: lld
    # Re-reviewed against hld 1.0.0: the storage decision changes no module.
    path: design-lld.md
    version: 1.0.1
    upstream: hld
    derived_from_version: 1.0.0
`

func writeSample(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "specs", "001-sample")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "traceability.yaml"), []byte(sampleTraceability), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestAmendPreservesReviewerNotes is the point of the file it edits. The comment
// on an artifact records why a human judged the downstream still correct; a tool
// that drops it on every stamp erases the evidence that Article II.2 exists to
// collect.
func TestAmendPreservesReviewerNotes(t *testing.T) {
	root := writeSample(t)
	if err := Amend(root, []string{"--feature", "001-sample", "--artifact", "hld", "--version", "1.1.0"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "specs", "001-sample", "traceability.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	if !strings.Contains(got, "Re-reviewed against hld 1.0.0") {
		t.Errorf("the reviewer's note was deleted by the stamp:\n%s", got)
	}
	if !strings.Contains(got, "Cascade tracking (Constitution Art. II.2)") {
		t.Errorf("the file header was lost:\n%s", got)
	}
	if !strings.Contains(got, "version: 1.1.0") {
		t.Errorf("the artifact was not stamped:\n%s", got)
	}
	// The downstream must be marked as reviewed against the new upstream version.
	if !strings.Contains(got, "derived_from_version: 1.1.0") {
		t.Errorf("the downstream was not re-stamped:\n%s", got)
	}
	if strings.Contains(got, "upstream: \"\"") {
		t.Errorf("a null upstream was rewritten as an empty string:\n%s", got)
	}
}

// TestVerifyRejectsADeclaredTestThatDoesNotExist pins the check that found the
// gap it exists for. Feature 001's task table named six provider tests that had
// never been written, and both packages sat at 0% coverage — a checklist that
// can be ticked without its checks is worth less than no checklist.
func TestVerifyRejectsADeclaredTestThatDoesNotExist(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "specs", "001-sample")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("spec.md", "# Spec\n\n> **Version**: 1.0.0\n\n| ID | Requirement |\n|---|---|\n| FR-001 | do it |\n")
	write("tasks.md", "# Tasks\n\n> **Version**: 1.0.0\n\n| ID | Task | Satisfies | Test |\n|---|---|---|---|\n"+
		"| T001 | build it | FR-001 | `TestThatWasNeverWritten` |\n")

	rep, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range rep.Findings {
		if f.Check == "tests" && strings.Contains(f.Message, "TestThatWasNeverWritten") {
			found = true
			if !f.Fatal {
				t.Error("a task declaring a test that does not exist must fail the build")
			}
		}
	}
	if !found {
		t.Fatalf("the missing test was not reported: %+v", rep.Findings)
	}
}

// TestVerifyAcceptsProseCheckpoints: not every checkpoint is a test name, and
// flagging "smoke test per subcommand" or "`make ci` green" would push authors
// towards naming nothing at all.
func TestVerifyAcceptsProseCheckpoints(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "specs", "001-sample")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.md"),
		[]byte("# Spec\n\n> **Version**: 1.0.0\n\n| ID | Requirement |\n|---|---|\n| FR-001 | do it |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"),
		[]byte("# Tasks\n\n> **Version**: 1.0.0\n\n| ID | Task | Satisfies | Test |\n|---|---|---|---|\n"+
			"| T001 | build it | FR-001 | smoke test per subcommand; `make ci` green |\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Check == "tests" {
			t.Errorf("a prose checkpoint was reported as a missing test: %s", f.Message)
		}
	}
}
