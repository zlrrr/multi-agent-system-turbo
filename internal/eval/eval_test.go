package eval

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func library(t *testing.T) *knowledge.Library {
	t.Helper()
	lib, err := knowledge.LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func corpus(t *testing.T) *Corpus {
	t.Helper()
	c, err := LoadCorpus(library(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Len() == 0 {
		t.Fatal("the shipped corpus is empty")
	}
	return c
}

// TestCorpusLoadsFromDirectory is FR-001: a case is data, and an operator's own
// corpus is the point. A shipped corpus they cannot extend is a demo.
func TestCorpusLoadsFromDirectory(t *testing.T) {
	dir := t.TempDir()
	body := validCase("operator-written", "redis", "memory-pressure")
	if err := os.WriteFile(filepath.Join(dir, "mine.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := LoadCorpus(library(t), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cs := range c.Cases() {
		if cs.ID() == "operator-written" {
			found = true
			if !strings.HasPrefix(cs.Source(), dir) {
				t.Errorf("source = %q, want the file it came from", cs.Source())
			}
		}
	}
	if !found {
		t.Errorf("a case in an extra directory was not loaded: %v", c.Cases())
	}
	if c.Len() < 2 {
		t.Error("the extra directory replaced the shipped corpus instead of adding to it")
	}
}

// TestCaseSchemaRequiresAnExpectedOutcome is FR-002. A case asserting nothing
// measures nothing, and would sit in the corpus looking like coverage.
func TestCaseSchemaRequiresAnExpectedOutcome(t *testing.T) {
	lib := library(t)
	full := validCase("x", "redis", "memory-pressure")
	for name, body := range map[string]string{
		// Removing one line of `expect` leaves the other, which is still an
		// expectation — the case must assert *nothing* to be refused.
		"no expectation at all": full[:strings.Index(full, "expect:")] + "expect:\n",
		"no telemetry": strings.Replace(full,
			full[strings.Index(full, "telemetry:"):strings.Index(full, "expect:")],
			"telemetry:\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCase([]byte(body), "test", lib)
			if code := errs.CodeOf(err); code != "MAS-9100" {
				t.Fatalf("got %v (%s), want MAS-9100", err, code)
			}
		})
	}

	// Both languages are required, like everything else operator-facing.
	half := strings.Replace(validCase("x", "redis", "memory-pressure"),
		`    zh: "中文标题"`, `    zh: ""`, 1)
	if code := errs.CodeOf(mustFail(t, half, lib)); code != "MAS-9100" {
		t.Errorf("a case with a missing translation was accepted")
	}
}

// TestCaseNamingAnUndeclaredModeIsRefused: a case can only assert conclusions
// the pack is able to reach. One that cannot be satisfied would fail forever
// and teach nobody anything.
func TestCaseNamingAnUndeclaredModeIsRefused(t *testing.T) {
	lib := library(t)

	body := validCase("x", "redis", "no-such-failure-mode")
	if code := errs.CodeOf(mustFail(t, body, lib)); code != "MAS-9101" {
		t.Errorf("a case naming an undeclared mode was accepted")
	}

	// Ruled-out modes are held to the same rule: a case that rules out
	// something no pack can conclude rules out nothing.
	ruled := strings.Replace(validCase("x", "redis", "memory-pressure"),
		"  not_failure_modes: [replication-broken]", "  not_failure_modes: [invented-mode]", 1)
	if code := errs.CodeOf(mustFail(t, ruled, lib)); code != "MAS-9101" {
		t.Errorf("a case ruling out an undeclared mode was accepted")
	}

	// A middleware with no pack at all.
	noPack := strings.Replace(validCase("x", "redis", "memory-pressure"),
		"middleware: redis", "middleware: notamiddleware", 1)
	if code := errs.CodeOf(mustFail(t, noPack, lib)); code != "MAS-9102" {
		t.Errorf("a case for a middleware with no pack was accepted")
	}
}

// TestWithholdingWithoutExpectingAGapIsRefused: taking a source away and
// expecting nothing tests nothing — the run simply has less evidence.
func TestWithholdingWithoutExpectingAGapIsRefused(t *testing.T) {
	body := strings.Replace(validCase("x", "redis", "memory-pressure"),
		"  logs:\n", "  withhold: [logs]\n  logs:\n", 1)
	if code := errs.CodeOf(mustFail(t, body, library(t))); code != "MAS-9100" {
		t.Errorf("withholding a source without expecting a gap was accepted")
	}
}

// TestHarnessUsesTheRealPipeline is FR-003 and the reason this harness lives
// outside the system. The layer most likely to regress is between a signal's
// PromQL and the parsed series; a harness that injected tools would skip it.
func TestHarnessUsesTheRealPipeline(t *testing.T) {
	c := caseByID(t, corpus(t), "redis-maxmemory-eviction")
	out := NewRunner(library(t)).Run(context.Background(), c, Options{})

	if out.Err != nil {
		t.Fatalf("the case failed to run: %v", out.Err)
	}
	if out.TelemetryHits == 0 {
		t.Error("no telemetry request reached the stub servers; the collectors were bypassed")
	}
	if len(out.Concluded) == 0 {
		t.Error("the run reached no conclusion at all")
	}
}

// TestUnmatchedQueryReturnsEmptyNotZero: a query the case does not answer must
// return an empty result. Zero is a measurement; empty is "this deployment does
// not export that", and since feature 002's engine fix the difference is what
// turns a silent false pass into a recorded gap.
func TestUnmatchedQueryReturnsEmptyNotZero(t *testing.T) {
	c := &Case{Telemetry: Telemetry{Metrics: map[string][]float64{"redis_up": {1}}}}
	st := newStubs(c)
	defer st.close()

	body := get(t, st.prom.URL+"/api/v1/query_range?query=kafka_partition_count")
	if !strings.Contains(body, `"result":[]`) {
		t.Errorf("an unmatched query returned %s, want an empty result", body)
	}
	if strings.Contains(body, `"0"`) {
		t.Errorf("an unmatched query returned a zero value: %s", body)
	}

	matched := get(t, st.prom.URL+"/api/v1/query_range?query=redis_up{job=%22x%22}")
	if strings.Contains(matched, `"result":[]`) {
		t.Errorf("a matched query returned empty: %s", matched)
	}
}

// TestFalseConclusionIsScoredSeparately is FR-005. "Said nothing" and "said the
// wrong thing confidently" are different failures with different costs, and a
// combined figure would hide which one a change caused.
func TestFalseConclusionIsScoredSeparately(t *testing.T) {
	c := &Case{
		Metadata: Metadata{ID: "c"},
		Expect: Expectation{
			FailureModes:    []string{"memory-pressure"},
			NotFailureModes: []string{"replication-broken"},
		},
	}

	miss := Score(c, &core.Report{Conclusions: []string{}})
	if len(miss.Missing) != 1 || len(miss.False) != 0 {
		t.Errorf("a miss scored as %+v", miss)
	}

	wrong := Score(c, &core.Report{Conclusions: []string{"memory-pressure", "replication-broken"}})
	if len(wrong.Missing) != 0 {
		t.Errorf("the expected mode was concluded but counted as missing: %+v", wrong)
	}
	if len(wrong.False) != 1 || wrong.False[0] != "replication-broken" {
		t.Errorf("a ruled-out conclusion was not counted separately: %+v", wrong)
	}
	if wrong.Hit() {
		t.Error("a run that concluded a ruled-out mode was scored as a hit")
	}

	right := Score(c, &core.Report{Conclusions: []string{"memory-pressure"}})
	if !right.Hit() {
		t.Errorf("a correct run was not a hit: %+v", right)
	}
}

// TestWithheldSourceMustProduceADeclaredGap is FR-006: correctness is not
// enough when the evidence was taken away — the run has to say it is missing.
func TestWithheldSourceMustProduceADeclaredGap(t *testing.T) {
	c := &Case{
		Metadata: Metadata{ID: "c"},
		Expect: Expectation{
			FailureModes: []string{"memory-pressure"},
			Gaps:         []string{"MAS-4101"},
		},
	}

	silent := Score(c, &core.Report{Conclusions: []string{"memory-pressure"}})
	if silent.Hit() {
		t.Error("a run that reached the right answer without declaring the missing source was a hit")
	}
	if len(silent.MissingGaps) != 1 {
		t.Errorf("the undeclared gap was not counted: %+v", silent)
	}

	honest := Score(c, &core.Report{
		Conclusions: []string{"memory-pressure"},
		Gaps:        []core.Gap{{Code: "MAS-4101", Detail: "metrics unavailable"}},
	})
	if !honest.Hit() {
		t.Errorf("a run that declared the gap was not a hit: %+v", honest)
	}
}

// TestScoringUsesNoTextSimilarity is FR-004, checked structurally rather than
// by behaviour: a similarity scorer would reward a model that restates the
// prompt, so the scorer must not be able to read prose at all.
func TestScoringUsesNoTextSimilarity(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "score.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Fields of core.Report that carry prose. Scoring may read ids and codes;
	// reading any of these would make the number mean something else.
	prose := map[string]bool{
		"Summary": true, "Statement": true, "Detail": true, "Rationale": true,
		"Title": true, "Explanation": true, "Notes": true, "Text": true,
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if prose[sel.Sel.Name] {
			t.Errorf("scoring reads %s at %s: scoring may only read ids and codes",
				sel.Sel.Name, fset.Position(sel.Pos()))
		}
		return true
	})

	// The banned-token scan reads code with comments stripped: score.go's own
	// comments explain *why* there is no similarity matching, and flagging the
	// explanation would make the check impossible to document.
	code := sourceWithoutComments(t, "score.go")
	for _, banned := range []string{"strings.Contains", "Levenshtein", "similarity", "fuzzy"} {
		if strings.Contains(code, banned) {
			t.Errorf("score.go references %q, which suggests text matching", banned)
		}
	}
}

// TestResultsAreDeterministic is FR-008: a regression must be a real change,
// not scheduling.
func TestResultsAreDeterministic(t *testing.T) {
	r := NewRunner(library(t))
	cases := corpus(t).Cases()

	first := r.Matrix(context.Background(), cases, []string{"supervisor", "single"}, Options{})
	firstText := renderToString(t, first, "en")
	for i := 0; i < 2; i++ {
		again := r.Matrix(context.Background(), cases, []string{"supervisor", "single"}, Options{})
		if got := renderToString(t, again, "en"); got != firstText {
			t.Fatalf("run %d rendered differently:\n--- first ---\n%s\n--- again ---\n%s",
				i+1, firstText, got)
		}
	}
}

// TestMatrixRunsEveryCaseAgainstEveryTopology is FR-007.
func TestMatrixRunsEveryCaseAgainstEveryTopology(t *testing.T) {
	cases := corpus(t).Cases()
	topologies := []string{"single", "supervisor", "plan-execute"}
	s := NewRunner(library(t)).Matrix(context.Background(), cases, topologies, Options{})

	if want := len(cases) * len(topologies); len(s.Outcomes) != want {
		t.Fatalf("outcomes = %d, want %d", len(s.Outcomes), want)
	}
	seen := map[string]map[string]bool{}
	for _, o := range s.Outcomes {
		if seen[o.Case] == nil {
			seen[o.Case] = map[string]bool{}
		}
		seen[o.Case][o.Topology] = true
	}
	for _, c := range cases {
		for _, top := range topologies {
			if !seen[c.ID()][top] {
				t.Errorf("%s was never run under %s", c.ID(), top)
			}
		}
	}
	if len(s.ByTopology()) != len(topologies) {
		t.Errorf("totals cover %d topologies, want %d", len(s.ByTopology()), len(topologies))
	}
}

// TestReportKeepsOutcomesSeparate is FR-010 and CON-002: no collapsed score,
// anywhere.
func TestReportKeepsOutcomesSeparate(t *testing.T) {
	s := Summary{
		Cases: 2, Topologies: []string{"supervisor"}, Provider: "mock",
		Outcomes: []Outcome{
			{Case: "a", Topology: "supervisor", Concluded: []string{"x"}},
			{Case: "b", Topology: "supervisor", Missing: []string{"y"}, False: []string{"z"}},
		},
	}
	totals := s.ByTopology()
	if len(totals) != 1 {
		t.Fatalf("totals = %+v", totals)
	}
	got := totals[0]
	if got.Hits != 1 || got.Misses != 1 || got.False != 1 {
		t.Errorf("totals = %+v; hits, misses and false conclusions must be counted apart", got)
	}

	// No combined figure in the table or the totals. The caveats are excluded
	// deliberately: one of them exists precisely to say the number is *not*
	// accuracy, and banning the word there would forbid the disclaimer.
	text := renderToString(t, s, "en")
	table := text
	if i := strings.Index(text, "This corpus is synthetic"); i >= 0 {
		table = text[:i]
	}
	for _, banned := range []string{"score", "Score", "accuracy", "Accuracy", "%"} {
		if strings.Contains(table, banned) {
			t.Errorf("the result table contains %q, which reads as a collapsed score:\n%s",
				banned, table)
		}
	}
}

// TestRenderedResultAlwaysCarriesTheCaveats is NFR-005. A caveat in the manual
// is absent from the screenshot, and the screenshot is what gets forwarded.
func TestRenderedResultAlwaysCarriesTheCaveats(t *testing.T) {
	s := Summary{Cases: 1, Topologies: []string{"supervisor"}, Provider: "mock",
		Outcomes: []Outcome{{Case: "a", Topology: "supervisor"}}}

	for lang, want := range map[string][]string{
		"en": {"synthetic", "agreement with its own labels", "replays a script"},
		"zh": {"合成", "与其自身标签的一致程度", "重放"},
	} {
		text := renderToString(t, s, lang)
		for _, phrase := range want {
			if !strings.Contains(text, phrase) {
				t.Errorf("%s output omits %q:\n%s", lang, phrase, text)
			}
		}
	}

	// JSON carries them as fields, so a consumer cannot drop them by choosing a
	// different formatter.
	var buf bytes.Buffer
	if err := RenderJSON(&buf, s, "en"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"caveats"`) {
		t.Errorf("JSON output carries no caveats field:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "synthetic") {
		t.Errorf("JSON caveats do not state the corpus is synthetic:\n%s", buf.String())
	}
}

// TestMockRunRefusesToClaimModelQuality is FR-009. The scripted transcript
// already contains the answer; presenting that as a model score would be the
// most flattering possible lie.
func TestMockRunRefusesToClaimModelQuality(t *testing.T) {
	mock := Summary{Cases: 1, Provider: "mock", Topologies: []string{"supervisor"},
		Outcomes: []Outcome{{Case: "a", Topology: "supervisor"}}}
	text := renderToString(t, mock, "en")
	if !strings.Contains(text, "say nothing about a model") {
		t.Errorf("a scripted run did not disclaim model quality:\n%s", text)
	}

	// A real provider gets no such disclaimer, or the caveat would be noise
	// that readers learn to skip.
	real := mock
	real.Provider = "anthropic"
	if got := renderToString(t, real, "en"); strings.Contains(got, "replays a script") {
		t.Errorf("a non-scripted provider was given the scripted-provider caveat:\n%s", got)
	}
}

// TestEveryPackHasACase is FR-013: a pack with no case is knowledge nothing
// checks.
func TestEveryPackHasACase(t *testing.T) {
	covered := map[string]bool{}
	for _, m := range corpus(t).Middlewares() {
		covered[m] = true
	}
	for _, p := range library(t).All() {
		if !covered[p.Metadata.Middleware] {
			t.Errorf("pack %s has no case; its knowledge is checked by nothing",
				p.Metadata.Middleware)
		}
	}
}

// TestCorpusRunsInsideTheCIBudget is NFR-001. A corpus that slows CI is a
// corpus someone eventually deletes.
func TestCorpusRunsInsideTheCIBudget(t *testing.T) {
	cases := corpus(t).Cases()
	start := time.Now()
	s := NewRunner(library(t)).Matrix(context.Background(), cases,
		[]string{"supervisor", "single"}, Options{})
	elapsed := time.Since(start)

	if elapsed > 60*time.Second {
		t.Errorf("the corpus took %s, above the 60s budget", elapsed)
	}
	if len(s.Outcomes) == 0 {
		t.Fatal("nothing ran")
	}
	t.Logf("%d case-runs in %s", len(s.Outcomes), elapsed.Round(time.Millisecond))
}

// TestShippedCorpusPasses is the regression gate itself: the shipped cases must
// hold under the default topology, or a pack change went unnoticed.
func TestShippedCorpusPasses(t *testing.T) {
	s := NewRunner(library(t)).Matrix(context.Background(), corpus(t).Cases(),
		[]string{"supervisor"}, Options{})
	if err := s.Regression(); err != nil {
		t.Fatalf("%v\n%s", err, renderToString(t, s, "en"))
	}
}

// helpers

func mustFail(t *testing.T, body string, lib *knowledge.Library) error {
	t.Helper()
	_, err := ParseCase([]byte(body), "test", lib)
	if err == nil {
		t.Fatal("expected the case to be refused")
	}
	return err
}

func caseByID(t *testing.T, c *Corpus, id string) *Case {
	t.Helper()
	for _, cs := range c.Cases() {
		if cs.ID() == id {
			return cs
		}
	}
	t.Fatalf("no case %q in the corpus", id)
	return nil
}

// sourceWithoutComments returns a file's code with comments removed, so a scan
// for a forbidden construct does not trip over the comment explaining why it is
// forbidden.
func sourceWithoutComments(t *testing.T, path string) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0) // 0: comments discarded
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func renderToString(t *testing.T, s Summary, lang string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, s, lang); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := httpGet(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func validCase(id, middleware, mode string) string {
	return `apiVersion: mas.turbo/v1
kind: DiagnosticCase
metadata:
  id: ` + id + `
  middleware: ` + middleware + `
  version: "7.2.4"
  title:
    en: "A title"
    zh: "中文标题"
  description:
    en: "A description"
    zh: "中文描述"
symptom:
  en: "something is wrong"
  zh: "出问题了"
telemetry:
  metrics:
    redis_memory_used_bytes: [940, 990]
    redis_memory_max_bytes: [1000, 1000]
  logs:
    - "OOM command not allowed"
expect:
  failure_modes: [` + mode + `]
  not_failure_modes: [replication-broken]
`
}

func httpGet(url string) (string, error) {
	resp, err := http.Get(url) //nolint:gosec,noctx // a test server this test just started
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
