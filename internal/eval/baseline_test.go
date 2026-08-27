package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func sampleSummary() Summary {
	return Summary{
		Cases: 2, Provider: "mock", Topologies: []string{"single", "supervisor"},
		Outcomes: []Outcome{
			{Case: "a", Topology: "single", Model: "mock-1"},
			{Case: "a", Topology: "supervisor", Model: "mock-1", Missing: []string{"memory-pressure"}},
			{Case: "b", Topology: "single", Model: "mock-1", False: []string{"replication-broken"}},
			{Case: "b", Topology: "supervisor", Model: "mock-1", MissingGaps: []string{"MAS-4102"}},
		},
	}
}

// TestBaselineRecordsEveryCell is FR-001. A cell is one (case, topology, model)
// and what is recorded is the class plus the ids that made it that class —
// never a count. A baseline of counts would compare a change that trades a miss
// for a false conclusion as unchanged, which is the pair this project refuses
// to average (design-hld.md §2).
func TestBaselineRecordsEveryCell(t *testing.T) {
	b := NewBaseline(sampleSummary())

	if b.Provider != "mock" {
		t.Errorf("provider %q, want mock", b.Provider)
	}
	if len(b.Cells) != 4 {
		t.Fatalf("%d cells recorded, want 4", len(b.Cells))
	}

	byKey := map[string]Cell{}
	for _, c := range b.Cells {
		byKey[c.Key()] = c
	}
	for key, want := range map[string]Class{
		"a|single|mock-1":     ClassHit,
		"a|supervisor|mock-1": ClassMiss,
		"b|single|mock-1":     ClassWrong,
		"b|supervisor|mock-1": ClassGapMissed,
	} {
		got, ok := byKey[key]
		if !ok {
			t.Errorf("cell %s was not recorded", key)
			continue
		}
		if got.Class != want {
			t.Errorf("cell %s class %q, want %q", key, got.Class, want)
		}
	}

	// The ids travel with the class, or a "changed failure" could never be
	// told from a repeat of the same one.
	if got := byKey["a|supervisor|mock-1"].Missing; len(got) != 1 || got[0] != "memory-pressure" {
		t.Errorf("the missing mode was not recorded: %v", got)
	}

	// A run that both missed and reached a ruled-out mode is the more serious
	// of the two; a class has to pick one, and the ids keep the rest.
	both := Outcome{Case: "c", Topology: "single", Model: "m",
		Missing: []string{"x"}, False: []string{"y"}}
	if got := both.Class(); got != ClassWrong {
		t.Errorf("class %q for a run that both missed and concluded wrongly, want %q",
			got, ClassWrong)
	}
}

// TestBaselineIsByteStableAcrossRuns is FR-012: the file is reviewed as a diff,
// and a diff full of reordering is one nobody reads.
func TestBaselineIsByteStableAcrossRuns(t *testing.T) {
	s := sampleSummary()
	first, err := NewBaseline(s).Encode()
	if err != nil {
		t.Fatal(err)
	}

	// Same content, outcomes in a different order.
	shuffled := s
	shuffled.Outcomes = []Outcome{s.Outcomes[3], s.Outcomes[0], s.Outcomes[2], s.Outcomes[1]}
	second, err := NewBaseline(shuffled).Encode()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("two encodings of the same result differ:\n%s\n---\n%s", first, second)
	}

	// Round-trips unchanged.
	loaded, err := ParseBaseline(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := loaded.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) {
		t.Errorf("a baseline did not round-trip:\n%s\n---\n%s", first, again)
	}

	var decoded map[string]any
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("a baseline is not JSON: %v", err)
	}
	if _, ok := decoded["cells"]; !ok {
		t.Errorf("a baseline records no cells: %s", first)
	}
}

// TestBaselineIsNeverWrittenImplicitly is FR-002 and CON-003. A baseline that
// writes itself records whatever happened and can never fail.
func TestBaselineIsNeverWrittenImplicitly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")

	// A full run, with nothing asking for a baseline.
	lib := library(t)
	_ = NewRunner(lib).Matrix(context.Background(), corpus(t).Cases()[:1],
		[]string{"single"}, Options{})

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a plain run created %s", path)
	}

	// Only Save writes, and it is the one thing the CLI flag calls.
	if err := NewBaseline(sampleSummary()).Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Save did not write the file: %v", err)
	}

	// A file that is not a baseline is refused with a code, rather than being
	// silently treated as empty — which would make every cell "new".
	bad := filepath.Join(dir, "not-a-baseline.json")
	if err := os.WriteFile(bad, []byte(`{"hello":"world"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(bad); err == nil || errs.CodeOf(err) != "MAS-9106" {
		t.Errorf("a file that is not a baseline was accepted: %v", err)
	}
	if _, err := LoadBaseline(filepath.Join(dir, "absent.json")); errs.CodeOf(err) != "MAS-9106" {
		t.Errorf("a missing baseline was not coded: %v", err)
	}
}

var _ = config.Default
var _ = strings.TrimSpace

// changesByTransition groups a delta for assertion.
func changesByTransition(d Delta) map[Transition][]string {
	out := map[Transition][]string{}
	for _, c := range d.Changes {
		out[c.Transition] = append(out[c.Transition], c.Cell.Key())
	}
	return out
}

// TestRegressionsAndImprovementsAreReportedSeparately is FR-003 and CON-001.
// A change that fixes two cells and breaks one is two improvements and one
// regression, never a net of minus-or-plus one.
func TestRegressionsAndImprovementsAreReportedSeparately(t *testing.T) {
	base := NewBaseline(Summary{
		Provider: "mock", Cases: 3,
		Outcomes: []Outcome{
			{Case: "a", Topology: "single", Model: "m"},                         // hit
			{Case: "b", Topology: "single", Model: "m", Missing: []string{"x"}}, // miss
			{Case: "c", Topology: "single", Model: "m", Missing: []string{"y"}}, // miss
		},
	})
	now := Summary{
		Provider: "mock", Cases: 3,
		Outcomes: []Outcome{
			{Case: "a", Topology: "single", Model: "m", False: []string{"z"}}, // regressed
			{Case: "b", Topology: "single", Model: "m"},                       // improved
			{Case: "c", Topology: "single", Model: "m"},                       // improved
		},
	}

	d := Compare(base, now)
	got := changesByTransition(d)
	if len(got[Regressed]) != 1 || got[Regressed][0] != "a|single|m" {
		t.Errorf("regressions %v, want [a|single|m]", got[Regressed])
	}
	if len(got[Improved]) != 2 {
		t.Errorf("improvements %v, want two", got[Improved])
	}

	// Nothing anywhere sums them. A single number here would let one hide the
	// other, which is what the corpus was arranged against.
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"net"`, `"score"`, `"delta_total"`} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("the delta carries %s, which nets two failures that must stay apart", banned)
		}
	}

	// A regression fails the gate; an improvement does not.
	if err := d.Gate(); err == nil || errs.CodeOf(err) != "MAS-9105" {
		t.Errorf("a regression did not fail the gate: %v", err)
	}
	improvedOnly := Compare(base, Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m"},
		{Case: "b", Topology: "single", Model: "m"},
		{Case: "c", Topology: "single", Model: "m"},
	}})
	if err := improvedOnly.Gate(); err != nil {
		t.Errorf("improvements failed the gate: %v", err)
	}
}

// TestKnownBadCellDoesNotFailTheGate is FR-004, and the reason this feature
// exists. Under the absolute gate the only way to keep CI green with a case
// that legitimately cannot pass is to delete the case — and the case is the
// only record that the gap exists.
func TestKnownBadCellDoesNotFailTheGate(t *testing.T) {
	failing := []Outcome{{Case: "a", Topology: "single", Model: "m", Missing: []string{"x"}}}
	base := NewBaseline(Summary{Provider: "mock", Outcomes: failing})
	d := Compare(base, Summary{Provider: "mock", Outcomes: failing})

	if err := d.Gate(); err != nil {
		t.Errorf("a cell that fails exactly as recorded failed the gate: %v", err)
	}

	// It has to stay visible, every run and not only when it changes: a gap
	// that stops being visible is a gap that stops being fixed.
	got := changesByTransition(d)
	if len(got[KnownBad]) != 1 {
		t.Errorf("known-bad cells %v, want one — a silent known-bad cell is a forgotten one", got[KnownBad])
	}

	// And the absolute gate still fails on it, so the two gates say different
	// things and a reader can tell which one they are looking at.
	if err := (Summary{Outcomes: failing}).Regression(); err == nil {
		t.Error("the absolute gate passed a failing cell")
	}
}

// TestChangedFailureIsReported is FR-005: a cell that was missing one mode and
// now reaches a wrong one has moved, even though both are "not a hit".
func TestChangedFailureIsReported(t *testing.T) {
	base := NewBaseline(Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m", Missing: []string{"x"}},
	}})
	d := Compare(base, Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m", False: []string{"y"}},
	}})

	got := changesByTransition(d)
	if len(got[ChangedFailure]) != 1 {
		t.Errorf("transitions %v, want one changed failure", got)
	}
	if err := d.Gate(); err != nil {
		t.Errorf("a changed failure failed the gate: %v", err)
	}

	// Same class, different ids, is still a change.
	sameClass := Compare(base, Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m", Missing: []string{"different"}},
	}})
	if len(changesByTransition(sameClass)[ChangedFailure]) != 1 {
		t.Errorf("a miss of a different mode was not reported as a changed failure")
	}
}

// TestNewCellIsNotARegression is FR-006. Adding a case must not look like
// breaking one, or nobody will add cases.
func TestNewCellIsNotARegression(t *testing.T) {
	base := NewBaseline(Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m"},
	}})
	d := Compare(base, Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m"},
		{Case: "b", Topology: "single", Model: "m", Missing: []string{"x"}},
	}})

	got := changesByTransition(d)
	if len(got[New]) != 1 || got[New][0] != "b|single|m" {
		t.Errorf("new cells %v, want [b|single|m]", got[New])
	}
	if len(got[Regressed]) != 0 {
		t.Errorf("a new cell was reported as a regression: %v", got[Regressed])
	}
	if err := d.Gate(); err != nil {
		t.Errorf("a new failing cell failed the gate: %v", err)
	}
}

// TestMissingCellIsReported is FR-007: a cell that stopped running is not a
// pass. Silence there is how coverage disappears.
func TestMissingCellIsReported(t *testing.T) {
	base := NewBaseline(Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m"},
		{Case: "b", Topology: "single", Model: "m"},
	}})
	d := Compare(base, Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m"},
	}})

	got := changesByTransition(d)
	if len(got[NotRun]) != 1 || got[NotRun][0] != "b|single|m" {
		t.Errorf("not-run cells %v, want [b|single|m]", got[NotRun])
	}
}

// TestProviderMismatchIsDisclosed is FR-008. Comparing across providers is what
// a model matrix is for; doing it silently is not.
func TestProviderMismatchIsDisclosed(t *testing.T) {
	base := NewBaseline(Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "mock-1"},
	}})
	d := Compare(base, Summary{Provider: "anthropic", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "mock-1"},
	}})

	if d.Mismatch == "" {
		t.Fatal("a provider mismatch was not disclosed")
	}
	for _, want := range []string{"mock", "anthropic"} {
		if !strings.Contains(d.Mismatch, want) {
			t.Errorf("the disclosure does not name %s: %q", want, d.Mismatch)
		}
	}
	if err := d.Gate(); err != nil {
		t.Errorf("a provider mismatch failed the gate; it is a disclosure, not an error: %v", err)
	}

	// It has to survive both renderings.
	text := renderDeltaToString(t, d, "en")
	if !strings.Contains(text, "anthropic") {
		t.Errorf("the rendered comparison does not disclose the mismatch:\n%s", text)
	}
	var buf bytes.Buffer
	if err := RenderDeltaJSON(&buf, d, "en"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "provider_mismatch") {
		t.Errorf("the JSON does not carry the mismatch:\n%s", buf.String())
	}

	same := Compare(base, Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "mock-1"},
	}})
	if same.Mismatch != "" {
		t.Errorf("a matching provider produced a mismatch note: %q", same.Mismatch)
	}
}

func renderDeltaToString(t *testing.T, d Delta, lang string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderDelta(&buf, d, lang); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestModelAxisRunsEveryCell is FR-009. G7.3 asks for a model/topology matrix
// and only the topology axis existed: --matrix varied the topology and held the
// model fixed.
func TestModelAxisRunsEveryCell(t *testing.T) {
	cases := corpus(t).Cases()[:2]
	models := []string{"mock-1", "mock-2"}
	topologies := []string{"single", "supervisor"}

	s := NewRunner(library(t)).Matrix(context.Background(), cases, topologies,
		Options{Models: models})

	want := len(cases) * len(topologies) * len(models)
	if len(s.Outcomes) != want {
		t.Fatalf("%d cells, want %d (%d cases × %d topologies × %d models)",
			len(s.Outcomes), want, len(cases), len(topologies), len(models))
	}

	seen := map[string]bool{}
	for _, o := range s.Outcomes {
		if o.Model == "" {
			t.Errorf("cell %s/%s carries no model", o.Case, o.Topology)
		}
		key := o.Case + "|" + o.Topology + "|" + o.Model
		if seen[key] {
			t.Errorf("cell %s ran twice", key)
		}
		seen[key] = true
	}
	for _, c := range cases {
		for _, topology := range topologies {
			for _, m := range models {
				if !seen[c.ID()+"|"+topology+"|"+m] {
					t.Errorf("cell %s|%s|%s never ran", c.ID(), topology, m)
				}
			}
		}
	}

	// Ordering stays deterministic, so two runs of the same matrix render
	// identically (feature 006 FR-008).
	again := NewRunner(library(t)).Matrix(context.Background(), cases, topologies,
		Options{Models: models})
	for i := range s.Outcomes {
		if s.Outcomes[i].Case != again.Outcomes[i].Case ||
			s.Outcomes[i].Topology != again.Outcomes[i].Topology ||
			s.Outcomes[i].Model != again.Outcomes[i].Model {
			t.Fatalf("cell %d differs between two runs of the same matrix", i)
		}
	}
}

// TestPerCellAccountingIsAttributed is FR-010 and RSK-4: a cell's cost must be
// attributed to the model that ran it. Reading the model from shared config
// instead would attribute every cell to whichever was configured last, which
// looks authoritative and is wrong.
func TestPerCellAccountingIsAttributed(t *testing.T) {
	cases := corpus(t).Cases()[:1]
	s := NewRunner(library(t)).Matrix(context.Background(), cases,
		[]string{"single", "supervisor"}, Options{Models: []string{"mock-1", "mock-2"}})

	for _, o := range s.Outcomes {
		if o.Usage.LLMCalls == 0 {
			t.Errorf("cell %s|%s|%s recorded no model calls at all",
				o.Case, o.Topology, o.Model)
		}
	}

	// The two topologies differ in how many calls they make, so per-cell
	// accounting must differ between them under the same model. If every cell
	// reported the run's total, they would be identical.
	byTopology := map[string]int{}
	for _, o := range s.Outcomes {
		if o.Model != "mock-1" {
			continue
		}
		byTopology[o.Topology] = o.Usage.LLMCalls
	}
	if byTopology["single"] == byTopology["supervisor"] {
		t.Errorf("single and supervisor recorded the same call count (%d); "+
			"accounting is not per cell", byTopology["single"])
	}

	// A baseline made from this run records the model on every cell, or a
	// comparison could never say which model regressed.
	for _, c := range NewBaseline(s).Cells {
		if c.Model == "" {
			t.Errorf("baseline cell %s|%s carries no model", c.Case, c.Topology)
		}
	}
}

// TestComparisonCarriesTheSamplingCaveat is FR-011 and CON-002. A comparison
// says what changed; it cannot say whether the change is significant, and the
// difference has to be on the page rather than in the reader's head.
func TestComparisonCarriesTheSamplingCaveat(t *testing.T) {
	base := NewBaseline(Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m"},
	}})
	d := Compare(base, Summary{Provider: "mock", Outcomes: []Outcome{
		{Case: "a", Topology: "single", Model: "m", Missing: []string{"x"}},
	}})

	for _, lang := range []string{"en", "zh"} {
		text := renderDeltaToString(t, d, lang)
		want := "one sample"
		if lang == "zh" {
			want = "一个样本"
		}
		if !strings.Contains(text, want) {
			t.Errorf("%s output does not say each cell is one sample:\n%s", lang, text)
		}
	}

	// And in the JSON, so an integration cannot format it away.
	var buf bytes.Buffer
	if err := RenderDeltaJSON(&buf, d, "zh"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "一个样本") {
		t.Errorf("the JSON carries no Chinese caveat:\n%s", buf.String())
	}

	// The comparison must not claim significance it cannot support. Scanned on
	// the caveats unwrapped, because the renderer breaks lines at 92 columns
	// and a phrase split across two of them is still one phrase.
	joined := strings.ToLower(strings.Join(translateCaveats(d, "en"), " "))
	for _, banned := range []string{"confidence interval", "p-value", "statistically"} {
		if strings.Contains(joined, banned) {
			t.Errorf("the comparison claims %q from one sample:\n%s", banned, joined)
		}
	}
	if !strings.Contains(joined, "not whether the change is significant") {
		t.Errorf("the comparison does not disclaim significance:\n%s", joined)
	}
}

// TestShippedBaselineMatchesTheCorpus is FR-014: the repository's own baseline
// has to describe the corpus it ships, or it is a file nobody can trust.
func TestShippedBaselineMatchesTheCorpus(t *testing.T) {
	base, err := ParseBaseline(shippedBaseline)
	if err != nil {
		t.Fatalf("the shipped baseline does not parse: %v", err)
	}

	s := NewRunner(library(t)).Matrix(context.Background(), corpus(t).Cases(),
		AllTopologies(), Options{})

	d := Compare(base, s)
	if err := d.Gate(); err != nil {
		t.Fatalf("%v\n%s", err, renderDeltaToString(t, d, "en"))
	}

	// Coverage in both directions: a baseline missing cells would pass the gate
	// while checking less than it claims, and one carrying cells that no longer
	// run would look like coverage that is gone.
	if n := d.Count(New); n > 0 {
		t.Errorf("%d cell(s) ran that the baseline does not cover; "+
			"record them with `mas eval --matrix --write-baseline internal/eval/baseline.json`\n%s",
			n, renderDeltaToString(t, d, "en"))
	}
	if n := d.Count(NotRun); n > 0 {
		t.Errorf("the baseline covers %d cell(s) that no longer run:\n%s",
			n, renderDeltaToString(t, d, "en"))
	}

	if base.Provider != "mock" {
		t.Errorf("the shipped baseline was recorded under %q; a committed file "+
			"that changes on every run is not a baseline", base.Provider)
	}

	// Known-bad cells are logged rather than failed: they are meant to be
	// visible on every run, which is the whole reason they do not fail the gate.
	for _, c := range d.Changes {
		if c.Transition == KnownBad {
			t.Logf("known-bad: %s — %s", c.Cell.Key(), c.Detail)
		}
	}
}
