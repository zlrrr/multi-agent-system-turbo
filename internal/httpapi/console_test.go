package httpapi

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The tests in this file are structural: they read the embedded asset and
// decide whether a construct is present or absent. They cannot prove the
// console renders correctly — proving that needs a browser, and a browser is a
// dependency this repository does not take for one feature
// (specs/012-web-console/design-lld.md §11).
//
// What a scan can decide, it decides completely, and the invariants chosen here
// are the ones that fail silently: a broken layout is obvious on first sight,
// and a stored-XSS sink is not.

func consoleScript(t *testing.T) string {
	t.Helper()
	b, err := assetFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("reading the console script: %v", err)
	}
	return string(b)
}

func consoleShell(t *testing.T) string {
	t.Helper()
	b, err := assetFS.ReadFile("assets/index.html")
	if err != nil {
		t.Fatalf("reading the console shell: %v", err)
	}
	return string(b)
}

// TestConsoleNeverUsesAnHTMLSink is FR-005 and CON-003.
//
// Report prose is written by a language model and quoted from estate logs.
// Anyone who can write a log line on a monitored middleware host — which is
// close to everyone — could reach this console. So no value may become markup,
// and the way to guarantee that is for the constructs that could turn a string
// into markup to be absent from the file entirely.
func TestConsoleNeverUsesAnHTMLSink(t *testing.T) {
	script := consoleScript(t)
	shell := consoleShell(t)

	sinks := []string{
		"innerHTML",
		"outerHTML",
		"insertAdjacentHTML",
		"document.write",
		"eval(",
		"new Function",
		"createContextualFragment",
		"srcdoc",
	}
	for _, sink := range sinks {
		if strings.Contains(script, sink) {
			t.Errorf("the console script uses %q; every value must reach the page "+
				"through el()'s textContent, because report prose is model output "+
				"and estate log text", sink)
		}
		if strings.Contains(shell, sink) {
			t.Errorf("the console shell uses %q", sink)
		}
	}

	// The positive half: textContent is how text actually arrives.
	if !strings.Contains(script, "textContent") {
		t.Error("the console script never sets textContent, so it is not building " +
			"the page the way this design says it does")
	}

	// And an inline handler attribute would be a sink the CSP blocks but the
	// scan above would miss.
	if regexp.MustCompile(`\son[a-z]+\s*=\s*"`).MatchString(shell) {
		t.Error("the console shell carries an inline event-handler attribute")
	}
}

// TestConsoleKeepsTheCredentialOutOfURLsAndCookies is FR-011.
//
// A credential in a URL reaches access logs, browser history and referrer
// headers. A credential in a cookie is attached automatically, which is what
// makes CSRF a problem worth defending against — and the reason this console
// has neither.
func TestConsoleKeepsTheCredentialOutOfURLsAndCookies(t *testing.T) {
	script := consoleScript(t)

	for _, forbidden := range []string{"localStorage", "document.cookie", "setCookie"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("the console script uses %q; the credential is held for the "+
				"browser tab only", forbidden)
		}
	}
	if !strings.Contains(script, "sessionStorage") {
		t.Error("the console script does not use sessionStorage, so it is not " +
			"holding the credential the way this design says it does")
	}
	if !strings.Contains(script, `"Authorization"`) {
		t.Error("the console script never sets an Authorization header")
	}
	// The credential must not be assembled into a query string anywhere.
	if regexp.MustCompile(`[?&](token|access_token|auth|key)=`).MatchString(script) {
		t.Error("the console script puts a credential in a query string")
	}
	// Cookies must not travel on the API reads either.
	if !strings.Contains(script, `credentials: "omit"`) {
		t.Error("the console script does not omit credentials on fetch, so a " +
			"cookie set by something else on this origin would be sent")
	}
}

// TestConsoleNeverStartsADiagnosis is FR-004 and CON-001.
//
// Starting a diagnosis spends model tokens and reads production telemetry. The
// console is a reader; the `diagnose` scope stays a machine-to-machine
// credential rather than one a browser tab holds.
func TestConsoleNeverStartsADiagnosis(t *testing.T) {
	script := consoleScript(t)

	for _, forbidden := range []string{
		`method: "POST"`, `method: "PUT"`, `method: "PATCH"`, `method: "DELETE"`,
		`method:"POST"`, "XMLHttpRequest", "sendBeacon", "navigator.send",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("the console script contains %q; it renders what has been "+
				"computed and starts nothing", forbidden)
		}
	}
	// The `diagnose` scope, as opposed to the `/diagnoses` collection the
	// console legitimately reads: \b keeps the plural out of this.
	if regexp.MustCompile(`\bdiagnose\b`).MatchString(script) {
		t.Error("the console script names the diagnose scope; it holds read only")
	}
	if strings.Contains(script, "<form") || strings.Contains(consoleShell(t), "<form") {
		t.Error("the console has a form; it submits nothing")
	}
}

// TestConsoleStringsAreBilingual is FR-008 and NFR-002.
func TestConsoleStringsAreBilingual(t *testing.T) {
	for _, id := range consoleStringIDs() {
		pair := consoleStrings[id]
		if strings.TrimSpace(pair[0]) == "" {
			t.Errorf("%s: no English text", id)
		}
		if strings.TrimSpace(pair[1]) == "" {
			t.Errorf("%s: no Chinese text", id)
		}
		// A Chinese entry that is byte-identical to the English one is almost
		// always a forgotten translation rather than a deliberate choice. The
		// exceptions are the ones with nothing to translate.
		if pair[0] == pair[1] && !untranslatable[id] {
			t.Errorf("%s: both languages are %q, which is usually a forgotten "+
				"translation; add it to untranslatable if it is deliberate", id, pair[0])
		}
	}

	// And the projection must actually pick the right column.
	en := consoleStringsFor("en")
	zh := consoleStringsFor("zh")
	if en["nav.runs"] == zh["nav.runs"] {
		t.Error("consoleStringsFor returns the same text for both languages")
	}
	if len(en) != len(consoleStrings) || len(zh) != len(consoleStrings) {
		t.Errorf("projection dropped entries: %d/%d en, %d/%d zh",
			len(en), len(consoleStrings), len(zh), len(consoleStrings))
	}
}

// untranslatable lists the ids whose two languages are legitimately identical.
var untranslatable = map[string]bool{
	"col.id": true, // "ID" is "ID"
}

// declaredString matches a t("…") reference in the console script.
var declaredString = regexp.MustCompile(`\bt\("([a-z0-9._]+)"\)`)

// TestConsoleStringsAreAllUsed is FR-009: checked in both directions, because
// an id referenced and absent is a blank label, an id present and unreferenced
// is dead weight, and both are defects.
func TestConsoleStringsAreAllUsed(t *testing.T) {
	script := consoleScript(t)

	referenced := map[string]bool{}
	for _, m := range declaredString.FindAllStringSubmatch(script, -1) {
		referenced[m[1]] = true
	}
	if len(referenced) == 0 {
		t.Fatal("no t(…) references found; the scanner and the script disagree " +
			"about how strings are referenced")
	}

	var missing, dead []string
	for id := range referenced {
		if _, ok := consoleStrings[id]; !ok {
			missing = append(missing, id)
		}
	}
	for _, id := range consoleStringIDs() {
		if !referenced[id] {
			dead = append(dead, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the console references strings that do not exist: %s "+
			"(each renders as its own id)", strings.Join(missing, ", "))
	}
	if len(dead) > 0 {
		t.Errorf("the string table carries entries nothing references: %s",
			strings.Join(dead, ", "))
	}
}

// TestConsoleRendersTheErrorCode is FR-010: a failure must arrive as its code,
// its message and its remedy, not as "something went wrong". The whole
// error-code registry exists so an operator can act on a failure, and a console
// that swallows the code throws that away.
func TestConsoleRendersTheErrorCode(t *testing.T) {
	script := consoleScript(t)
	for _, field := range []string{"body.code", "body.message", "body.remedy",
		"err.code", "err.message", "err.remedy"} {
		if !strings.Contains(script, field) {
			t.Errorf("the console script never reads %s", field)
		}
	}
	if !strings.Contains(script, "MAS-7012") {
		t.Error("the console does not name MAS-7012 when a credential is rejected")
	}
}

// TestConsoleSurfacesGapsAndAdvisoryStatus is FR-012 and CON-005.
//
// The output of this system is a ranked set of hypotheses with known holes and
// advisory recommendations. A console can destroy that nuance with a large font
// and a collapsed section and still look like a working console.
func TestConsoleSurfacesGapsAndAdvisoryStatus(t *testing.T) {
	script := consoleScript(t)
	for _, must := range []string{
		"rep.gaps",      // the holes in the evidence
		"gaps.impact",   // and what each one cost
		"rec.advisory",  // recommendations are advice, never actions
		"foot.advisory", // said again where it cannot be scrolled past
		"cost.unpriced", // an unpriced model is named, not counted as zero
		"rep.truncated", // a run that hit a budget produced less
		"hyp.refuted",   // what was ruled out stays visible
		"confidenceBar", // a confidence is shown, not implied
	} {
		if !strings.Contains(script, must) {
			t.Errorf("the console script never references %q", must)
		}
	}

	// Nothing may be hidden behind a disclosure widget.
	if strings.Contains(script, "<details") || strings.Contains(script, `"details"`) {
		t.Error("the console folds content into a disclosure widget; gaps, " +
			"confidences and advisory status are first-class content")
	}

	// And the honest-cost path must exist in code, not only in a label.
	if !strings.Contains(script, "cost.known") {
		t.Error("costLine does not branch on cost.known, so it cannot tell a " +
			"free run from an unpriced one")
	}
}

// TestConsoleCSPDeniesByDefault is FR-006 at the constant, beside the served
// header: the policy has to start from nothing and admit what is needed.
func TestConsoleCSPDeniesByDefault(t *testing.T) {
	if !strings.Contains(consoleCSP, "default-src 'none'") {
		t.Error("the console CSP does not deny by default")
	}
	for _, directive := range []string{
		"script-src 'self'", "style-src 'self'", "connect-src 'self'",
		"frame-ancestors 'none'", "base-uri 'none'",
	} {
		if !strings.Contains(consoleCSP, directive) {
			t.Errorf("the console CSP is missing %q", directive)
		}
	}
	if strings.Contains(consoleCSP, "unsafe-inline") || strings.Contains(consoleCSP, "unsafe-eval") {
		t.Error("the console CSP permits unsafe-inline or unsafe-eval, which " +
			"gives back exactly what it exists to withhold")
	}
}

// TestConsoleAssetsAreAnAllowList is FR-007 at the table: every entry must
// resolve, so a renamed file fails here rather than at a reader's browser.
func TestConsoleAssetsAreAnAllowList(t *testing.T) {
	for path, asset := range consoleAssets {
		if _, err := assetFS.ReadFile(asset.file); err != nil {
			t.Errorf("/ui/%s maps to %s, which is not embedded: %v", path, asset.file, err)
		}
	}
	entries, err := assetFS.ReadDir("assets")
	if err != nil {
		t.Fatal(err)
	}
	published := map[string]bool{}
	for _, a := range consoleAssets {
		published[a.file] = true
	}
	for _, e := range entries {
		if !published["assets/"+e.Name()] {
			t.Errorf("assets/%s is embedded but not on the allow-list; either "+
				"publish it deliberately or remove it", e.Name())
		}
	}
}

// TestConsoleLangNarrowsAnything: the language a query parameter or a
// configuration might carry has to land on one of the two this project has.
func TestConsoleLangNarrows(t *testing.T) {
	for input, want := range map[string]string{
		"zh": "zh", "zh-CN": "zh", "ZH-Hans": "zh", "zh_TW": "zh",
		"en": "en", "en-GB": "en", "": "en", "fr": "en", "z": "en",
	} {
		if got := consoleLang(input); got != want {
			t.Errorf("consoleLang(%q) = %q, want %q", input, got, want)
		}
	}
}
