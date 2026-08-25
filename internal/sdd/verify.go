// Package sdd implements the machine checks that keep the specification chain
// honest. Constitution Article I.3 requires traceability to be verified rather
// than asserted; this is that verification.
package sdd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Finding is one problem the verifier found.
type Finding struct {
	Check   string
	Path    string
	Message string
	Fatal   bool
}

// Report is the outcome of a verification pass.
type Report struct {
	Findings []Finding
	Checked  map[string]int
}

// Failed reports whether any fatal finding was recorded.
func (r *Report) Failed() bool {
	for _, f := range r.Findings {
		if f.Fatal {
			return true
		}
	}
	return false
}

// Print renders the report.
func (r *Report) Print(w io.Writer) {
	names := make([]string, 0, len(r.Checked))
	for k := range r.Checked {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-12s %d checked\n", name, r.Checked[name])
	}
	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "\nsdd: all checks passed")
		return
	}
	fmt.Fprintln(w)
	for _, f := range r.Findings {
		level := "warning"
		if f.Fatal {
			level = "FAIL"
		}
		fmt.Fprintf(w, "%s  [%s] %s: %s\n", level, f.Check, f.Path, f.Message)
	}
	if r.Failed() {
		fmt.Fprintln(w, "\nsdd: verification failed")
	} else {
		fmt.Fprintln(w, "\nsdd: passed with warnings")
	}
}

// RepoRoot locates the repository root.
func RepoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	wd, _ := os.Getwd()
	return wd
}

// bilingualDirs are the trees Article III governs.
var bilingualDirs = []string{"docs", "specs", ".specify"}

// exemptFromParity lists files that are intentionally single-language: machine
// data with no prose, and generated artifacts.
var exemptFromParity = map[string]bool{
	"traceability.yaml": true,
}

// Verify runs every check against a repository.
func Verify(root string) (*Report, error) {
	r := &Report{Checked: map[string]int{}}
	if err := checkParity(root, r); err != nil {
		return nil, err
	}
	if err := checkTraceability(root, r); err != nil {
		return nil, err
	}
	if err := checkCoverage(root, r); err != nil {
		return nil, err
	}
	if err := checkDeclaredTests(root, r); err != nil {
		return nil, err
	}
	return r, nil
}

// declaredTest matches a test name a task's checkpoint column names, e.g.
// `TestPlaybookHappyPath`. Only backtick-quoted identifiers beginning with Test
// are checked: a checkpoint may legitimately be prose ("smoke test per
// subcommand") or a command ("`make ci` green").
var declaredTest = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")

// goTestFunc matches a test function's definition.
var goTestFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)

// checkDeclaredTests enforces Article VI.1 mechanically: a task that names its
// test must have that test.
//
// This check exists because it was needed. T032 and T033 of feature 001 were
// marked done while the tests they declared did not exist and their packages
// were at 0% coverage — the two providers a real operator runs were the least
// verified code in the repository. Nothing caught it, because coverage checking
// asks whether a requirement is claimed by a task, not whether a task's claim
// is true. A checklist that can be ticked without its checks is worth less than
// no checklist.
func checkDeclaredTests(root string, r *Report) error {
	existing, err := goTestNames(root)
	if err != nil {
		return err
	}

	specs := filepath.Join(root, "specs")
	entries, err := os.ReadDir(specs)
	if err != nil {
		return nil //nolint:nilerr // no specs directory is not this check's business
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := "specs/" + e.Name() + "/tasks.md"
		body, err := os.ReadFile(filepath.Join(specs, e.Name(), "tasks.md")) //nolint:gosec // repository path
		if err != nil {
			continue
		}
		var missing []string
		seen := map[string]bool{}
		for _, m := range declaredTest.FindAllSubmatch(body, -1) {
			name := string(m[1])
			if seen[name] {
				continue
			}
			seen[name] = true
			r.Checked["tests"]++
			if !existing[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			r.Findings = append(r.Findings, Finding{
				Check: "tests", Path: rel, Fatal: true,
				Message: "declares test(s) that do not exist: " + strings.Join(missing, ", ") +
					" (Constitution Art. VI.1: a task is done only when its test passes, " +
					"which requires the test to be written)",
			})
		}
	}
	return nil
}

// goTestNames collects every test function defined in the repository.
func goTestNames(root string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is not a finding here
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "bin", "dist", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, rerr := os.ReadFile(path) //nolint:gosec // repository path
		if rerr != nil {
			return nil //nolint:nilerr // skip what cannot be read
		}
		for _, m := range goTestFunc.FindAllSubmatch(body, -1) {
			out[string(m[1])] = true
		}
		return nil
	})
	return out, err
}

// checkParity enforces Article III: every document exists in both languages.
func checkParity(root string, r *Report) error {
	for _, dir := range bilingualDirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			name := info.Name()
			if exemptFromParity[name] || filepath.Ext(name) != ".md" {
				return nil
			}
			rel, _ := filepath.Rel(root, path)

			// docs/ splits by language directory; elsewhere the .zh.md suffix pairs files.
			if strings.HasPrefix(filepath.ToSlash(rel), "docs/") {
				parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
				if len(parts) < 3 {
					return nil
				}
				lang := parts[1]
				other := "zh"
				if lang == "zh" {
					other = "en"
				}
				if lang != "en" && lang != "zh" {
					return nil
				}
				counterpart := filepath.Join(root, "docs", other, parts[2])
				r.Checked["parity"]++
				if _, err := os.Stat(counterpart); err != nil {
					r.Findings = append(r.Findings, Finding{
						Check: "parity", Path: rel, Fatal: true,
						Message: "has no counterpart at docs/" + other + "/" + parts[2] +
							" (Constitution Art. III.2: both languages in the same commit)",
					})
				}
				return nil
			}

			if strings.HasSuffix(name, ".zh.md") {
				r.Checked["parity"]++
				counterpart := strings.TrimSuffix(path, ".zh.md") + ".md"
				if _, err := os.Stat(counterpart); err != nil {
					r.Findings = append(r.Findings, Finding{
						Check: "parity", Path: rel, Fatal: true,
						Message: "has no English counterpart",
					})
				}
				return nil
			}
			r.Checked["parity"]++
			counterpart := strings.TrimSuffix(path, ".md") + ".zh.md"
			if _, err := os.Stat(counterpart); err != nil {
				r.Findings = append(r.Findings, Finding{
					Check: "parity", Path: rel, Fatal: true,
					Message: "has no Chinese counterpart (expected " + filepath.Base(counterpart) + ")",
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// traceability mirrors specs/*/traceability.yaml.
type traceability struct {
	Feature   string `yaml:"feature"`
	Artifacts []struct {
		ID                 string `yaml:"id"`
		Path               string `yaml:"path"`
		Version            string `yaml:"version"`
		Upstream           string `yaml:"upstream"`
		DerivedFromVersion string `yaml:"derived_from_version"`
	} `yaml:"artifacts"`
}

var versionInDoc = regexp.MustCompile(`(?m)^>.*\*\*(?:Version|版本)\*\*[：:]\s*([0-9]+\.[0-9]+\.[0-9]+)`)

// checkTraceability enforces Article II.2: an artifact derived from an older
// upstream version than the one in the repository is stale.
func checkTraceability(root string, r *Report) error {
	specs := filepath.Join(root, "specs")
	entries, err := os.ReadDir(specs)
	if err != nil {
		return nil // no features yet
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tPath := filepath.Join(specs, e.Name(), "traceability.yaml")
		body, err := os.ReadFile(tPath)
		if err != nil {
			r.Findings = append(r.Findings, Finding{
				Check: "cascade", Path: "specs/" + e.Name(), Fatal: true,
				Message: "has no traceability.yaml; the cascade cannot be tracked",
			})
			continue
		}
		var tr traceability
		if err := yaml.Unmarshal(body, &tr); err != nil {
			r.Findings = append(r.Findings, Finding{
				Check: "cascade", Path: relOf(root, tPath), Fatal: true,
				Message: "is not valid YAML: " + err.Error(),
			})
			continue
		}

		versions := map[string]string{}
		for _, a := range tr.Artifacts {
			versions[a.ID] = a.Version
		}
		for _, a := range tr.Artifacts {
			r.Checked["cascade"]++
			docPath := filepath.Join(specs, e.Name(), a.Path)
			if !filepath.IsAbs(docPath) {
				docPath = filepath.Clean(docPath)
			}
			// The version recorded in traceability must match the document.
			if declared, ok := docVersion(docPath); ok && declared != a.Version {
				r.Findings = append(r.Findings, Finding{
					Check: "cascade", Path: relOf(root, docPath), Fatal: true,
					Message: fmt.Sprintf("declares version %s but traceability.yaml records %s", declared, a.Version),
				})
			}
			if a.Upstream == "" {
				continue
			}
			upstreamVersion, ok := versions[a.Upstream]
			if !ok {
				r.Findings = append(r.Findings, Finding{
					Check: "cascade", Path: relOf(root, tPath), Fatal: true,
					Message: fmt.Sprintf("artifact %q names unknown upstream %q", a.ID, a.Upstream),
				})
				continue
			}
			if compareVersions(a.DerivedFromVersion, upstreamVersion) < 0 {
				r.Findings = append(r.Findings, Finding{
					Check: "cascade", Path: relOf(root, tPath), Fatal: true,
					Message: fmt.Sprintf(
						"%s is stale: derived from %s %s, but %s is now %s. "+
							"Re-review it and re-stamp with `.specify/scripts/sdd.sh amend %s %s %s`",
						a.ID, a.Upstream, a.DerivedFromVersion, a.Upstream, upstreamVersion,
						tr.Feature, a.ID, a.Version),
				})
			}
		}
	}
	return nil
}

func docVersion(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	m := versionInDoc.FindSubmatch(b)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

func compareVersions(a, b string) int {
	if a == "" || a == "null" {
		if b == "" || b == "null" {
			return 0
		}
		return -1
	}
	if b == "" || b == "null" {
		return 1
	}
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(v string) [3]int {
	var out [3]int
	for i, part := range strings.SplitN(strings.TrimSpace(v), ".", 3) {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(part)
		out[i] = n
	}
	return out
}

var (
	requirementDecl = regexp.MustCompile(`\|\s*((?:FR|NFR)-\d{3})\s*\|`)
	requirementRef  = regexp.MustCompile(`(?:FR|NFR)-\d{3}`)
)

// checkCoverage enforces Article I.3: every requirement is claimed by a task,
// and no task cites a requirement that does not exist.
func checkCoverage(root string, r *Report) error {
	specs := filepath.Join(root, "specs")
	entries, err := os.ReadDir(specs)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		specPath := filepath.Join(specs, e.Name(), "spec.md")
		tasksPath := filepath.Join(specs, e.Name(), "tasks.md")
		specBody, err := os.ReadFile(specPath)
		if err != nil {
			continue
		}
		tasksBody, err := os.ReadFile(tasksPath)
		if err != nil {
			r.Findings = append(r.Findings, Finding{
				Check: "coverage", Path: "specs/" + e.Name(), Fatal: true,
				Message: "has a spec but no tasks.md; requirements cannot be traced to work",
			})
			continue
		}

		declared := map[string]bool{}
		for _, m := range requirementDecl.FindAllSubmatch(specBody, -1) {
			declared[string(m[1])] = true
		}
		claimed := map[string]bool{}
		for _, m := range requirementRef.FindAll(tasksBody, -1) {
			claimed[string(m)] = true
		}

		var uncovered, unknown []string
		for id := range declared {
			r.Checked["coverage"]++
			if !claimed[id] {
				uncovered = append(uncovered, id)
			}
		}
		for id := range claimed {
			if !declared[id] {
				unknown = append(unknown, id)
			}
		}
		sort.Strings(uncovered)
		sort.Strings(unknown)

		if len(uncovered) > 0 {
			r.Findings = append(r.Findings, Finding{
				Check: "coverage", Path: relOf(root, tasksPath), Fatal: true,
				Message: "no task claims " + strings.Join(uncovered, ", ") +
					" (Constitution Art. I.3: every requirement is realised by a task)",
			})
		}
		if len(unknown) > 0 {
			r.Findings = append(r.Findings, Finding{
				Check: "coverage", Path: relOf(root, tasksPath), Fatal: true,
				Message: "cites requirements that do not exist in spec.md: " + strings.Join(unknown, ", "),
			})
		}
	}
	return nil
}

func relOf(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
