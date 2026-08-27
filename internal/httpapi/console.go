package httpapi

import (
	"embed"
	"net/http"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

//go:embed assets/index.html assets/app.css assets/app.js
var assetFS embed.FS

// consolePrefix is where the console lives. Everything under it is anonymous
// and static; nothing under it can reach the service
// (specs/012-web-console/design-lld.md §4).
const consolePrefix = "/ui/"

// consoleAsset names one servable file.
type consoleAsset struct{ file, mime string }

// consoleAssets is an allow-list rather than a file server over assetFS.
// Deny by default means dropping a file into assets/ does not publish it —
// the same posture the safety guard and the route table already take.
var consoleAssets = map[string]consoleAsset{
	"":           {"assets/index.html", "text/html; charset=utf-8"},
	"index.html": {"assets/index.html", "text/html; charset=utf-8"},
	"app.css":    {"assets/app.css", "text/css; charset=utf-8"},
	"app.js":     {"assets/app.js", "text/javascript; charset=utf-8"},
}

// consoleCSP denies by default and then admits exactly what the console needs.
//
// `connect-src 'self'` is the load-bearing one: it confines fetch to this
// origin, so a credential could not be posted elsewhere even if a sink were
// somehow found. `form-action 'none'` is simply true — the console has no form
// submission at all — and `frame-ancestors 'none'` keeps it out of someone
// else's page.
const consoleCSP = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; img-src 'self' data:; base-uri 'none'; " +
	"form-action 'none'; frame-ancestors 'none'"

func consoleHeaders(w http.ResponseWriter, mime string) {
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("Content-Security-Policy", consoleCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

// handleConsole serves the console shell, or explains why it is not there.
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, r, http.StatusMethodNotAllowed, errs.New("MAS-7002", r.Method, r.URL.Path))
		return
	}
	if !s.svc.Config().Server.UI.On() {
		// A bare 404 would leave the operator who typed the URL guessing. That
		// the console is switched off is not a fact worth withholding, and the
		// remedy names the key that changes it.
		s.writeError(w, r, http.StatusNotFound, errs.New("MAS-7016"))
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, consolePrefix)
	if rest == "strings.json" {
		s.writeConsoleStrings(w, r)
		return
	}
	asset, ok := consoleAssets[rest]
	if !ok {
		s.writeError(w, r, http.StatusNotFound, errs.New("MAS-7404", r.URL.Path))
		return
	}
	body, err := assetFS.ReadFile(asset.file)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, errs.New("MAS-7003", err.Error()))
		return
	}
	consoleHeaders(w, asset.mime)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeConsoleStrings renders the vocabulary in one language.
//
// It is generated on each request rather than embedded so the table and what
// the console receives cannot diverge: there is one source and no copy.
func (s *Server) writeConsoleStrings(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = s.svc.Config().Run.Language
	}
	lang = consoleLang(lang)
	consoleHeaders(w, "application/json; charset=utf-8")
	writeJSON(w, http.StatusOK, map[string]any{
		"lang": lang, "strings": consoleStringsFor(lang),
	})
}

// handleConsoleRedirect sends /ui to /ui/ so the shell's relative asset
// references resolve.
func (s *Server) handleConsoleRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, consolePrefix, http.StatusMovedPermanently)
}
