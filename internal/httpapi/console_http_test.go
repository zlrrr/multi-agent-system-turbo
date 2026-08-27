package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/httpapi"
)

// disableConsole is the one configuration a hardened deployment writes.
func disableConsole(cfg *config.Config) {
	off := false
	cfg.Server.UI.Enabled = &off
}

// TestConsoleIsServed is FR-001.
func TestConsoleIsServed(t *testing.T) {
	s := newServerWith(t, nil)

	w := request(t, s, http.MethodGet, "/ui/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/ui/ returned %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, "app.js") {
		t.Errorf("/ui/ did not return the console shell:\n%s", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("/ui/ served %q", ct)
	}

	// The bare path redirects, so the shell's relative asset references resolve.
	if w := request(t, s, http.MethodGet, "/ui", "", nil); w.Code != http.StatusMovedPermanently {
		t.Errorf("/ui returned %d, want a redirect to /ui/", w.Code)
	} else if loc := w.Header().Get("Location"); loc != "/ui/" {
		t.Errorf("/ui redirected to %q", loc)
	}

	for _, asset := range []struct{ path, mime string }{
		{"/ui/app.js", "text/javascript"},
		{"/ui/app.css", "text/css"},
		{"/ui/index.html", "text/html"},
		{"/ui/strings.json", "application/json"},
	} {
		w := request(t, s, http.MethodGet, asset.path, "", nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s returned %d", asset.path, w.Code)
			continue
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, asset.mime) {
			t.Errorf("%s served %q, want %s", asset.path, ct, asset.mime)
		}
		if w.Body.Len() == 0 {
			t.Errorf("%s served an empty body", asset.path)
		}
	}
}

// TestConsoleStringsEndpointSpeaksBothLanguages: the vocabulary is generated
// from the Go table on each request, so the two cannot diverge.
func TestConsoleStringsEndpointSpeaksBothLanguages(t *testing.T) {
	s := newServerWith(t, func(cfg *config.Config) { cfg.Run.Language = "zh" })

	read := func(query string) (string, map[string]string) {
		t.Helper()
		w := request(t, s, http.MethodGet, "/ui/strings.json"+query, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("strings.json%s returned %d", query, w.Code)
		}
		var body struct {
			Lang    string            `json:"lang"`
			Strings map[string]string `json:"strings"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("strings.json%s is not JSON: %v", query, err)
		}
		return body.Lang, body.Strings
	}

	// With no parameter it follows the server's configured language.
	lang, zh := read("")
	if lang != "zh" {
		t.Errorf("strings.json defaulted to %q on a zh server", lang)
	}
	lang, en := read("?lang=en")
	if lang != "en" {
		t.Errorf("?lang=en returned %q", lang)
	}
	if zh["nav.runs"] == en["nav.runs"] || zh["nav.runs"] == "" || en["nav.runs"] == "" {
		t.Errorf("the two languages are not distinct: %q vs %q", zh["nav.runs"], en["nav.runs"])
	}
	if len(zh) != len(en) {
		t.Errorf("the languages carry different numbers of strings: %d zh, %d en", len(zh), len(en))
	}
}

// TestConsoleShellIsAnonymousAndDataIsNot is FR-002.
//
// A browser navigating to a page cannot send an Authorization header, so the
// shell has to answer without one. Everything the shell then fetches must not.
func TestConsoleShellIsAnonymousAndDataIsNot(t *testing.T) {
	s := newServerWith(t, withTokens)

	for _, path := range []string{"/ui/", "/ui/app.js", "/ui/app.css", "/ui/strings.json"} {
		if w := request(t, s, http.MethodGet, path, "", nil); w.Code != http.StatusOK {
			t.Errorf("%s demanded a credential: %d", path, w.Code)
		}
	}
	for _, path := range []string{
		"/api/v1/diagnoses", "/api/v1/targets", "/api/v1/packs",
		"/api/v1/topologies", "/metrics", "/",
	} {
		if w := request(t, s, http.MethodGet, path, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s answered the console's fetch without a credential: %d", path, w.Code)
		}
	}
}

// TestConsoleServesNoEstateData is FR-003 and CON-002.
//
// The console has no data path of its own. This is the assertion that says so
// in the only way that cannot rot: a target with a name nothing else would ever
// produce must not appear in any byte the console routes return.
func TestConsoleServesNoEstateData(t *testing.T) {
	const secret = "zzz-tenant-only-target-name"
	s := newServerWith(t, func(cfg *config.Config) {
		withTokens(cfg)
		cfg.Targets = append(cfg.Targets, config.TargetConfig{
			ID: secret, Kind: "redis", Version: "7.2.4",
		})
	})

	for _, path := range []string{
		"/ui/", "/ui/index.html", "/ui/app.js", "/ui/app.css", "/ui/strings.json",
		"/ui/strings.json?lang=zh",
	} {
		w := request(t, s, http.MethodGet, path, "", nil)
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("%s leaked a configured target name", path)
		}
	}

	// And the same fact from the other side: with a credential, the API does
	// return it, so the test above is not passing because nothing is configured.
	w := request(t, s, http.MethodGet, "/api/v1/targets", readerToken, nil)
	if !strings.Contains(w.Body.String(), secret) {
		t.Fatalf("the target was never visible anywhere, so the leak test proved nothing")
	}
}

// TestConsoleServesOnlyItsOwnAssets is FR-007: deny by default, so dropping a
// file into the directory does not publish it.
func TestConsoleServesOnlyItsOwnAssets(t *testing.T) {
	s := newServerWith(t, nil)

	for _, path := range []string{
		"/ui/nothing.js", "/ui/../server.go", "/ui/assets/app.js",
		"/ui/app.js.map", "/ui/.env", "/ui/config.yaml",
	} {
		w := request(t, s, http.MethodGet, path, "", nil)
		if w.Code == http.StatusOK {
			t.Errorf("%s was served (%d):\n%s", path, w.Code, w.Body.String())
		}
	}
}

// TestConsoleSendsAContentSecurityPolicy is FR-006.
func TestConsoleSendsAContentSecurityPolicy(t *testing.T) {
	s := newServerWith(t, nil)

	for _, path := range []string{"/ui/", "/ui/app.js", "/ui/app.css", "/ui/strings.json"} {
		w := request(t, s, http.MethodGet, path, "", nil)
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: CSP %q does not deny by default", path, csp)
		}
		if strings.Contains(csp, "unsafe-inline") {
			t.Errorf("%s: CSP permits unsafe-inline", path)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff header", path)
		}
		if w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: no referrer policy", path)
		}
	}

	// A strict CSP and an inline script are a contradiction the browser
	// resolves by not running the page. The shell must have neither.
	shell := request(t, s, http.MethodGet, "/ui/", "", nil).Body.String()
	if strings.Contains(shell, "<style") {
		t.Error("the shell carries an inline stylesheet, which the CSP blocks")
	}
	for _, frag := range strings.Split(shell, "<script")[1:] {
		open := strings.Index(frag, ">")
		close := strings.Index(frag, "</script>")
		if open < 0 || close < 0 {
			continue
		}
		if strings.TrimSpace(frag[open+1:close]) != "" {
			t.Error("the shell carries an inline script, which the CSP blocks")
		}
	}
}

// TestConsoleCanBeDisabled is FR-013: a hardened deployment gets to say no, and
// the operator who typed the URL gets told why rather than a bare 404.
func TestConsoleCanBeDisabled(t *testing.T) {
	off := newServerWith(t, disableConsole)

	for _, path := range []string{"/ui/", "/ui/app.js", "/ui/strings.json"} {
		w := request(t, off, http.MethodGet, path, "", nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s returned %d with the console disabled", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "MAS-7016") {
			t.Errorf("%s did not name MAS-7016:\n%s", path, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "server.ui.enabled") {
			t.Errorf("%s did not name the key that turns it back on:\n%s", path, w.Body.String())
		}
	}

	// The API is untouched either way.
	if w := request(t, off, http.MethodGet, "/api/v1/targets", "", nil); w.Code != http.StatusOK {
		t.Errorf("disabling the console changed the API: /api/v1/targets returned %d", w.Code)
	}

	// And the default is on: a console nobody can find is a console nobody uses.
	on := newServerWith(t, nil)
	if w := request(t, on, http.MethodGet, "/ui/", "", nil); w.Code != http.StatusOK {
		t.Errorf("the console is not served by default: %d", w.Code)
	}
	if !config.Default().Server.UI.On() {
		t.Error("config.Default() does not serve the console")
	}
}

// TestIndexReportsTheLanguage is FR-014: the console matches the operator's
// configured language without asking them a second time.
func TestIndexReportsTheLanguage(t *testing.T) {
	for _, want := range []string{"en", "zh"} {
		s := newServerWith(t, func(cfg *config.Config) { cfg.Run.Language = want })
		w := request(t, s, http.MethodGet, "/", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("/ returned %d", w.Code)
		}
		var body struct {
			Language string `json:"language"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Language != want {
			t.Errorf("/ reported language %q, want %q", body.Language, want)
		}
	}
}

// TestConsoleRoutesAreRegistered guards the amendment in design-lld.md §4:
// Routes() reports what routes() registered rather than a list maintained
// beside it, so a route added without being listed cannot hide from
// TestEveryRouteIsGuarded.
func TestConsoleRoutesAreRegistered(t *testing.T) {
	s := newServerWith(t, nil)
	got := map[string]bool{}
	for _, r := range s.Routes() {
		got[r] = true
	}
	for _, want := range []string{"/ui", "/ui/", "/api/v1/diagnoses", "/healthz", "/"} {
		if !got[want] {
			t.Errorf("Routes() does not report %q", want)
		}
	}
	if !httpapi.IsAnonymous("/ui/app.js") {
		t.Error("a console asset is not anonymous, so a browser could never load it")
	}
	if httpapi.IsAnonymous("/api/v1/targets") {
		t.Error("an API path became anonymous")
	}
}
