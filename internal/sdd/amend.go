package sdd

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const cascadeHeader = "# Cascade tracking (Constitution Art. II.2).\n" +
	"# `derived_from_version` records the upstream version this artifact was last\n" +
	"# reconciled against. When the upstream's `version` exceeds it, `sddctl verify`\n" +
	"# marks this artifact stale and CI fails until it is re-reviewed and re-stamped.\n"

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
	if len(doc.Content) == 0 {
		return fmt.Errorf("%s is empty", path)
	}

	artifacts := mapValue(doc.Content[0], "artifacts")
	if artifacts == nil || artifacts.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s declares no artifacts sequence", path)
	}

	// The edit is made on the parsed nodes rather than on a re-marshalled
	// struct, so the comments recording *why* each downstream was re-reviewed
	// survive. Those notes are the substance of cascade tracking; a tool that
	// deletes them on every amendment defeats the article it implements.
	found := false
	ids := make([]string, 0, len(artifacts.Content))
	for _, item := range artifacts.Content {
		id := scalarValue(mapValue(item, "id"))
		ids = append(ids, id)
		if id == *artifact {
			if err := setScalar(item, "version", *version); err != nil {
				return fmt.Errorf("%s/%s: %w", *feature, id, err)
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("feature %s has no artifact %q (have: %s)",
			*feature, *artifact, strings.Join(ids, ", "))
	}
	for _, item := range artifacts.Content {
		if scalarValue(mapValue(item, "upstream")) == *artifact {
			if err := setScalar(item, "derived_from_version", *version); err != nil {
				return fmt.Errorf("%s/%s: %w", *feature, scalarValue(mapValue(item, "id")), err)
			}
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	out := buf.Bytes()
	// A file scaffolded from the template already carries the header; one that
	// does not (an older or hand-written file) gets it, so every traceability
	// file explains itself without any of them losing its reviewer notes.
	if !bytes.Contains(out, []byte("Cascade tracking")) {
		out = append([]byte(cascadeHeader), out...)
	}
	if err := os.WriteFile(path, out, 0o640); err != nil {
		return err
	}
	fmt.Printf("stamped %s/%s at %s and marked its downstream reviewed\n", *feature, *artifact, *version)
	return nil
}

// mapValue returns the value node for a key in a mapping node, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalarValue(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

// setScalar rewrites a key's value in place, keeping the node's comments and
// its position in the file.
func setScalar(m *yaml.Node, key, value string) error {
	target := mapValue(m, key)
	if target == nil {
		return fmt.Errorf("no %s field to stamp", key)
	}
	target.Value = value
	target.Tag = "!!str"
	target.Style = 0
	return nil
}
