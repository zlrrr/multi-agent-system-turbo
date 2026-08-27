# Low-Level Design (LLD): A Read-Only Web Console

> **Feature ID**: `012-web-console` · **Version**: 1.0.1 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

```
internal/httpapi/
  console.go          new: the asset allow-list, the handler, the CSP
  strings.go          new: the bilingual UI string table
  assets/
    index.html        new: the shell — no data, no inline script or style
    app.css           new
    app.js            new: the whole client
  server.go           + the console routes, + `language` on the index,
                        Routes() now reports what was actually registered
  auth.go             + the console paths in anonymousRoutes
internal/config/
  config.go           + ServerConfig.UI
  validate.go         (nothing: a bool has no invalid value)
internal/service/
  doctor.go           + the console check
pkg/errs/
  registry.go         + MAS-7016
```

## 2. Configuration

```yaml
server:
  addr: ":8080"
  ui:
    enabled: true      # default; set false to not serve the console at all
```

```go
// UIConfig configures the web console.
type UIConfig struct {
    // Enabled defaults to true, which is why it is a *bool: a plain bool
    // cannot tell "the operator wrote false" from "the operator wrote
    // nothing", and those must mean opposite things here.
    Enabled *bool `yaml:"enabled" json:"enabled,omitempty"`
}

func (u UIConfig) On() bool { return u.Enabled == nil || *u.Enabled }
```

The pointer is the only subtlety in the configuration, and it is forced: a
default-on boolean loaded from YAML into a `bool` reads an absent key and an
explicit `false` identically. `config.Default()` therefore does not set it —
absence *is* the default — and `On()` is the single reader.

## 3. Assets and the allow-list

```go
//go:embed assets/index.html assets/app.css assets/app.js
var assetFS embed.FS

// consoleAssets is an allow-list rather than a file server over assetFS:
// deny by default means adding a file to the directory does not publish it.
var consoleAssets = map[string]struct{ file, mime string }{
    "":            {"assets/index.html", "text/html; charset=utf-8"},
    "index.html":  {"assets/index.html", "text/html; charset=utf-8"},
    "app.css":     {"assets/app.css", "text/css; charset=utf-8"},
    "app.js":      {"assets/app.js", "text/javascript; charset=utf-8"},
}
```

`strings.json` is not in the map: it is generated from Go on each request rather
than embedded, so the table and what the console receives cannot diverge.

`handleConsole` takes the path after `/ui/`, looks it up, and answers `MAS-7404`
for anything else. `/ui` without the slash redirects to `/ui/` so relative asset
references resolve.

Every console response carries:

```
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self';
                         connect-src 'self'; img-src 'self' data:; base-uri 'none';
                         form-action 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cache-Control: no-store        (on strings.json; assets are max-age=0 with the version in the URL query)
```

`default-src 'none'` means anything not listed is refused, which is the same
deny-by-default posture the safety guard and the route table already take.
`connect-src 'self'` confines `fetch` to this origin, so a credential cannot be
posted elsewhere even if a sink were found. `frame-ancestors 'none'` blocks
clickjacking, and `form-action 'none'` is honest — the console has no form
submission at all.

## 4. Anonymity, precisely

```go
var anonymousRoutes = map[string]bool{
    "/healthz": true,
    "/readyz":  true,
    "/ui":      true,
    "/ui/":     true,   // prefix: every asset under it
}
```

`ScopeFor` treats a `/ui/` prefix as anonymous, not merely the exact path. The
handler behind it can only return embedded bytes and a string table, so the
prefix is safe in a way an `/api/` prefix would not be — and
`TestConsoleServesNoEstateData` asserts exactly that by configuring a target
with a distinctive name and requiring it to appear in no console response.

`Routes()` stops being a hand-maintained literal beside `routes()` and instead
records what `routes()` registered:

```go
func (s *Server) route(pattern string, h http.HandlerFunc) {
    s.registered = append(s.registered, pattern)
    s.mux.HandleFunc(pattern, h)
}
```

This is an amendment to feature 009's guarantee rather than a new one. The
structural test `TestEveryRouteIsGuarded` walked a list that was written by
hand next to the registrations; a route added without adding it to the list was
invisible to the test that exists to find exactly that. Now the list cannot
disagree with the mux, and the same test also stops keeping its own copy of
`anonymousRoutes` — it reads the package's.

## 5. The string table

```go
// consoleStrings is the console's entire operator-facing vocabulary.
// id -> lang -> text. Both languages, always: TestConsoleStringsAreBilingual.
var consoleStrings = map[string][2]string{
    // {en, zh}
    "app.title":       {"MAS-Turbo console", "MAS-Turbo 控制台"},
    "runs.empty":      {"No runs yet.", "尚无诊断记录。"},
    ...
}
```

A two-element array rather than a map keyed by language, because the parity
question then has no way to be answered "the key is missing": every entry has
exactly two slots and both are checked non-empty. This is the shape the
error-code registry uses, for the same reason.

`GET /ui/strings.json` returns `{"lang": "en", "strings": {"app.title": "…"}}`
for one language, chosen by `?lang=`, defaulting to the server's configured
language. The console fetches it again when the reader switches language, which
is one request and no client-side table.

The script references strings only through `t("id")`. Two tests read the script
and the table and compare the two sets in both directions: an id referenced and
absent is a blank label, an id present and unreferenced is dead weight, and both
are defects.

## 6. The client

`app.js` is one file, roughly:

```
state        {token, lang, strings, route}
t(id)        string lookup, falls back to the id so a miss is visible
api(path)    fetch with the bearer header; throws {code, message, remedy}
el(tag, …)   createElement + textContent; the only way a value reaches the page
render()     dispatch on location.hash
  #/runs           list  ← GET /api/v1/diagnoses
  #/runs/<id>      detail ← GET /api/v1/diagnoses/<id>
  #/runs/<id>/steps trace ← GET /api/v1/diagnoses/<id>?steps=true
  #/targets        ← GET /api/v1/targets
  #/system         ← GET /api/v1/packs, /topologies, /
```

`el` is the whole XSS defence in one function:

```js
function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined && text !== null) n.textContent = String(text);
  return n;
}
```

There is no other way to put a value on the page, because there is no other
function that touches text, and the sinks that would allow it are absent by test.

**Polling.** While a detail view shows a run whose status is `running`, a
`setTimeout` re-fetches every 4 seconds. The timer is cleared whenever the
route changes, so a stale view cannot keep polling behind a new one.

**The credential.** Read from and written to `sessionStorage` under one key.
`api()` sends it in the header. On `401` the console clears it and returns to
the credential prompt, showing `MAS-7012` with its remedy. Nothing writes it to
`localStorage`, `document.cookie`, or any URL, and a test scans for all three.

**The secure-origin banner.** If `location.protocol` is `http:` and the hostname
is not `localhost`, `127.0.0.1` or `[::1]`, the credential prompt carries a
warning above it. It does not refuse — an operator behind a TLS-terminating
proxy that forwards over plaintext inside a cluster is a real and correct
deployment — it states what it can see.

## 7. Rendering the diagnosis honestly

The detail view is one column, in this order, and none of it is collapsed:

1. **Header** — id, status, target, topology, tenant if present, timestamps.
2. **Gaps**, when there are any, immediately under the header and before the
   summary. Each shows its `MAS-NNNN` code, what was wanted, why it failed and
   the stated impact. They come first because a reader who stops after the
   summary must still have seen them.
3. **Summary**.
4. **Hypotheses**, in rank order, each with its confidence as a percentage and
   its status word. Refuted ones stay in the list, marked, not hidden.
5. **Findings**, with severity and confidence.
6. **Recommendations**, each labelled advisory, under a heading that says
   MAS-Turbo changes nothing itself.
7. **Evidence**, each with its kind, source and time window.
8. **Usage and cost** — tokens, and cost only when known; an unpriced model is
   named as unpriced rather than counted as zero.
9. **Notes** and a truncation warning when the run hit a limit.

The step trace is a separate route rather than a section, because it is long and
the reader who wants it knows they want it.

## 8. `language` on the index

`GET /` gains `"language": cfg.Run.Language`. This is an amendment to feature
001's LLD §2.16: the index previously described the API without describing the
server's own presentation choice, and the console needs it to match the operator
without asking them twice.

It is behind `read` like the rest of the index, so the console applies the
browser's guess until a credential is entered — which is the right order anyway,
since the pre-credential screen has to render before there is anything to ask.

## 9. `mas doctor`

One check, `web console`, beside `api exposure` and `tenancy`:

- served, and at which path → `CheckOK`
- disabled by configuration → `CheckOK` with the detail saying so, not a warning:
  a deliberate configuration is not a defect.

## 10. Errors

| Code | Meaning | Where |
|---|---|---|
| `MAS-7016` | the web console is disabled in this configuration | `/ui/…` when `server.ui.enabled: false`, HTTP 404 |
| `MAS-7404` | not found | an asset that is not on the allow-list |
| `MAS-7012` | no usable credential | rendered by the console on a 401, with its remedy |

## 11. Tests

| Test | Asserts |
|---|---|
| `TestConsoleIsServed` | `/ui/` returns the shell; `/ui` redirects to it |
| `TestConsoleShellIsAnonymousAndDataIsNot` | assets answer with no credential; `/api/v1/*` still refuses |
| `TestConsoleServesNoEstateData` | a distinctively-named target appears in no console response |
| `TestConsoleNeverStartsADiagnosis` | the asset issues no POST and references no diagnose scope |
| `TestConsoleNeverUsesAnHTMLSink` | the asset contains no `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write`, `eval` or `new Function` |
| `TestConsoleSendsAContentSecurityPolicy` | the header is present and denies by default; the shell has no inline `<script>` or `<style>` |
| `TestConsoleServesOnlyItsOwnAssets` | an unlisted path under `/ui/` is `MAS-7404` |
| `TestConsoleStringsAreBilingual` | every id has both languages, non-empty |
| `TestConsoleStringsAreAllUsed` | referenced ⊆ table and table ⊆ referenced |
| `TestConsoleRendersTheErrorCode` | the asset reads `code`, `message` and `remedy` from a failure |
| `TestConsoleKeepsTheCredentialOutOfURLsAndCookies` | no `localStorage`, no `document.cookie`, no credential in a query string |
| `TestConsoleSurfacesGapsAndAdvisoryStatus` | the asset references gaps, advisory, unpriced and truncated |
| `TestConsoleCanBeDisabled` | `enabled: false` answers `MAS-7016`; the default serves |
| `TestIndexReportsTheLanguage` | `/` carries `language` |
| `TestDoctorReportsTheConsole` | the doctor names the console state |

### What a browser found that they did not

Before this feature shipped the console was driven once in a headless browser
against a live `mas serve` — not as a check the build runs, but as the
verification the structural tests explicitly cannot be. It found one defect the
Go tests could not see:

**A credential refused on the first read left a blank prompt.** `boot()` reads
the index to learn the server's language. With a stale token in
`sessionStorage`, that read returns `401`, `api()` forgets the credential, and
the `catch` swallowed the failure on the grounds that "the route below will
surface it". It could not: `route()` then found no token and rendered the
credential gate with nothing to say. The reader saw an empty prompt where
`MAS-7012` and its remedy belonged, which is precisely what FR-010 exists to
prevent.

The fix is `state.problem`: a failure raised before any view exists waits there,
and `viewGate` renders it above the prompt and clears it. This is why FR-010's
guarantee is now honoured on the one path where it was most needed — the path
where the reader has no other way to find out what went wrong.

Two smaller refinements came from reading the rendered page rather than the
code:

- `withoutCode(detail, code)` drops a leading `MAS-NNNN: ` from a gap's or a
  step's detail when the code is already rendered above it. `Gap.Detail` is
  usually `err.Error()`, so the line said its own code twice.
- `counted(title, list)` puts the size in a section heading. Nothing is
  collapsed — the design forbids that — but a run whose telemetry was
  unconfigured produced twenty-seven gaps, and a reader deserves to know that
  before they start scrolling.

### What these tests do not prove

Nine of the fifteen are **structural**: they read the embedded asset and assert
that a construct is present or absent. They cannot prove the console renders
correctly, because proving that needs a browser, and a browser is a dependency
this repository does not take for one feature.

What a scan can decide, it decides completely: whether a dangerous sink appears
anywhere in the file is a textual question with a definite answer, and so is
whether the string ids match. Those are the invariants chosen, and they are
chosen because they are the ones that fail *silently* — a broken layout is
obvious on first sight, and a stored-XSS sink is not.

The behavioural half is covered where behaviour lives: the API the console reads
is tested directly and thoroughly, and `make demo` renders the same report
through the Markdown path.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.1 | 2026-08-27 | §11: recorded what a one-off headless-browser run found — a credential refused during boot rendered a blank prompt instead of `MAS-7012`, fixed with `state.problem` — plus `withoutCode` and `counted` | code |
| 1.0.0 | 2026-08-27 | Initial low-level design | tasks, code |
