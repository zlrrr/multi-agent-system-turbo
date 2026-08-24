package sdd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Amend stamps an artifact's version in traceability.yaml and re-stamps its
// direct downstream as reviewed against it.
//
// Re-stamping is deliberately a separate, explicit act (Constitution Art. II.2):
// the point of cascade tracking is that a human decides the downstream is still
// correct, rather than the tooling assuming it.
func Amend(root string, args []string) error {
	fs := flag.NewFlagSet("amend", flag.ContinueOnError)
	feature := fs.String("feature", "", "feature directory, e.g. 001-mvp-core")
	artifact := fs.String("artifact", "", "artifact id, e.g. hld")
	version := fs.String("version", "", "new version, e.g. 1.1.0")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *feature == "" || *artifact == "" || *version == "" {
		return fmt.Errorf("amend requires --feature, --artifact and --version")
	}

	path := filepath.Join(root, "specs", *feature, "traceability.yaml")
	body, err := os.ReadFile(path) //nolint:gosec // repository-relative path
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	var tr traceability
	if err := yaml.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	found := false
	for i := range tr.Artifacts {
		if tr.Artifacts[i].ID == *artifact {
			tr.Artifacts[i].Version = *version
			found = true
		}
	}
	if !found {
		ids := make([]string, 0, len(tr.Artifacts))
		for _, a := range tr.Artifacts {
			ids = append(ids, a.ID)
		}
		return fmt.Errorf("feature %s has no artifact %q (have: %s)",
			*feature, *artifact, strings.Join(ids, ", "))
	}
	for i := range tr.Artifacts {
		if tr.Artifacts[i].Upstream == *artifact {
			tr.Artifacts[i].DerivedFromVersion = *version
		}
	}

	out, err := yaml.Marshal(tr)
	if err != nil {
		return err
	}
	header := "# Cascade tracking (Constitution Art. II.2).\n" +
		"# `derived_from_version` records the upstream version this artifact was last\n" +
		"# reconciled against. When the upstream's `version` exceeds it, `sddctl verify`\n" +
		"# marks this artifact stale and CI fails until it is re-reviewed and re-stamped.\n"
	if err := os.WriteFile(path, append([]byte(header), out...), 0o640); err != nil {
		return err
	}
	fmt.Printf("stamped %s/%s at %s and marked its downstream reviewed\n", *feature, *artifact, *version)
	return nil
}
