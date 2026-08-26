// Package eval measures what a diagnosis concluded against a corpus of cases
// with known causes.
//
// It exists because three features in a row ended with the same sentence: this
// project compares topologies but does not score them, because scoring needs a
// corpus. It does not end with a score. It ends with four facts kept apart —
// what was concluded, what was missed, what was concluded that should not have
// been, and what gaps were declared — because collapsing them would let a
// change that trades a miss for a confident wrong answer look like an
// improvement.
//
// Governs: specs/006-eval-harness/design-lld.md
package eval

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

//go:embed cases/*.yaml
var embeddedCases embed.FS

// shippedBaseline is this repository's own baseline for its own corpus,
// recorded under the deterministic provider. It is embedded so a test can
// compare against it without depending on the working directory.
//
//go:embed baseline.json
var shippedBaseline []byte

// APIVersion and Kind identify a case document.
const (
	APIVersion = "mas.turbo/v1"
	Kind       = "DiagnosticCase"
)

// Case is one incident with a known cause.
//
// What it asserts is deliberately narrow: ids the pipeline already commits to,
// in a vocabulary the knowledge packs define. There is no reference answer text
// and scoring never reads prose — a similarity scorer would reward a model that
// restates the prompt, and would produce a number whose meaning nobody could
// state precisely (plan.md §1).
type Case struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   Metadata    `yaml:"metadata"`
	Symptom    core.Text   `yaml:"symptom"`
	Telemetry  Telemetry   `yaml:"telemetry"`
	Expect     Expectation `yaml:"expect"`

	source string // where it was loaded from, for messages
}

// Metadata identifies a case.
type Metadata struct {
	ID          string    `yaml:"id"`
	Middleware  string    `yaml:"middleware"`
	Version     string    `yaml:"version"`
	Title       core.Text `yaml:"title"`
	Description core.Text `yaml:"description"`
}

// Telemetry is what the stub servers will serve.
type Telemetry struct {
	// Metrics maps a substring of the expanded PromQL to the series a query
	// matching it returns. Substring rather than exact query so a case does not
	// restate a pack's exact expression — otherwise renaming a signal would
	// break every case for no diagnostic reason.
	Metrics map[string][]float64 `yaml:"metrics"`
	Logs    []string             `yaml:"logs"`

	// Withhold names sources this case denies the run: "metrics" or "logs".
	// It is what lets a case test honesty rather than only correctness — take a
	// source away, then require the run to say it is missing.
	Withhold []string `yaml:"withhold"`
}

// Expectation is the outcome a correct diagnosis reaches.
type Expectation struct {
	FailureModes    []string `yaml:"failure_modes"`
	NotFailureModes []string `yaml:"not_failure_modes"`
	Gaps            []string `yaml:"gaps"`
}

// ID identifies the case.
func (c *Case) ID() string { return c.Metadata.ID }

// Source reports where the case was loaded from.
func (c *Case) Source() string { return c.source }

// Withholds reports whether a source is denied to the run.
func (c *Case) Withholds(source string) bool {
	for _, w := range c.Telemetry.Withhold {
		if strings.EqualFold(strings.TrimSpace(w), source) {
			return true
		}
	}
	return false
}

// Validate checks the case against the packs it refers to.
//
// A case that names a failure mode no pack declares cannot be satisfied by any
// diagnosis, so it would fail forever and teach nobody anything. Catching that
// at load is the difference between a corpus and a pile of aspirations.
func (c *Case) Validate(lib *knowledge.Library) error {
	where := c.source
	if c.Metadata.ID != "" {
		where = c.Metadata.ID
	}

	if c.APIVersion != APIVersion || c.Kind != Kind {
		return errs.New("MAS-9100", where,
			fmt.Sprintf("apiVersion/kind must be %s/%s", APIVersion, Kind))
	}
	if strings.TrimSpace(c.Metadata.ID) == "" {
		return errs.New("MAS-9100", where, "metadata.id is required")
	}
	if strings.TrimSpace(c.Metadata.Middleware) == "" {
		return errs.New("MAS-9100", where, "metadata.middleware is required")
	}
	for name, text := range map[string]core.Text{
		"metadata.title": c.Metadata.Title, "metadata.description": c.Metadata.Description,
		"symptom": c.Symptom,
	} {
		if !text.Complete() {
			return errs.New("MAS-9100", where, name+" must be present in both languages")
		}
	}

	// An expectation is the whole point: a case asserting nothing measures
	// nothing, and would sit in the corpus looking like coverage.
	if len(c.Expect.FailureModes) == 0 && len(c.Expect.NotFailureModes) == 0 &&
		len(c.Expect.Gaps) == 0 {
		return errs.New("MAS-9100", where,
			"expect declares no outcome, so the case cannot pass or fail")
	}
	if len(c.Telemetry.Metrics) == 0 && len(c.Telemetry.Logs) == 0 {
		return errs.New("MAS-9100", where, "telemetry is empty, so there is nothing to diagnose")
	}

	// Withholding a source without expecting the resulting gap would test
	// nothing: the run would simply have less evidence.
	if len(c.Telemetry.Withhold) > 0 && len(c.Expect.Gaps) == 0 {
		return errs.New("MAS-9100", where,
			"withholds a source but expects no gap; a withheld source must be declared missing")
	}

	pack, err := packFor(lib, c.Metadata.Middleware, c.Metadata.Version)
	if err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, m := range pack.FailureModes {
		declared[m.ID] = true
	}
	for _, list := range [][]string{c.Expect.FailureModes, c.Expect.NotFailureModes} {
		for _, id := range list {
			if !declared[id] {
				return errs.New("MAS-9101", where, id, pack.ID())
			}
		}
	}
	return nil
}

func packFor(lib *knowledge.Library, middleware, version string) (*knowledge.Pack, error) {
	pack, err := lib.For(core.MiddlewareKind(middleware), version)
	if err != nil {
		return nil, errs.New("MAS-9102", middleware)
	}
	return pack, nil
}

// Corpus is a set of cases.
type Corpus struct {
	cases []*Case
}

// LoadCorpus reads the embedded cases and any extra directories, validating
// each against the knowledge packs it refers to.
func LoadCorpus(lib *knowledge.Library, extraDirs []string) (*Corpus, error) {
	c := &Corpus{}

	entries, err := fs.ReadDir(embeddedCases, "cases")
	if err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && isCaseFile(e.Name()) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			b, rerr := fs.ReadFile(embeddedCases, "cases/"+name)
			if rerr != nil {
				return nil, errs.Wrap(rerr, "MAS-9100", name, rerr.Error())
			}
			parsed, perr := ParseCase(b, "embedded:"+name, lib)
			if perr != nil {
				return nil, perr
			}
			c.cases = append(c.cases, parsed)
		}
	}

	for _, dir := range extraDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		// A mistyped path must not silently fall through to the shipped
		// corpus: an operator would see their own cases "pass" without one of
		// them ever having been read.
		if _, err := os.Stat(dir); err != nil {
			return nil, errs.Wrap(err, "MAS-9104", dir, err.Error())
		}
		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !isCaseFile(d.Name()) {
				return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
			}
			b, rerr := os.ReadFile(path) //nolint:gosec // operator-supplied directory
			if rerr != nil {
				return errs.Wrap(rerr, "MAS-9100", path, rerr.Error())
			}
			parsed, perr := ParseCase(b, path, lib)
			if perr != nil {
				return perr
			}
			c.cases = append(c.cases, parsed)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	sort.Slice(c.cases, func(i, j int) bool { return c.cases[i].ID() < c.cases[j].ID() })
	return c, nil
}

// ParseCase reads and validates one case.
func ParseCase(b []byte, source string, lib *knowledge.Library) (*Case, error) {
	var c Case
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, errs.Wrap(err, "MAS-9100", source, err.Error())
	}
	c.source = source
	if err := c.Validate(lib); err != nil {
		return nil, err
	}
	return &c, nil
}

// Cases returns the corpus, sorted by id.
func (c *Corpus) Cases() []*Case { return c.cases }

// Len reports how many cases are loaded.
func (c *Corpus) Len() int { return len(c.cases) }

// Middlewares reports which middlewares the corpus covers.
func (c *Corpus) Middlewares() []string {
	seen := map[string]bool{}
	for _, cs := range c.cases {
		seen[cs.Metadata.Middleware] = true
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func isCaseFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}
