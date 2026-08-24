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
