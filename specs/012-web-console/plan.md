# Implementation Plan: A Read-Only Web Console

> **Feature ID**: `012-web-console` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

The whole design follows from one decision: **the console is a client of the
HTTP API, not a second implementation of it.**

Everything a console wants is already an endpoint. Runs are `GET
/api/v1/diagnoses`, a run is `GET /api/v1/diagnoses/{id}`, its trace is the same
with `?steps=true`, targets and packs and topologies each have theirs. If the
browser calls those, then the authorizer, the scope check and the tenant filter
all apply to the console exactly as they apply to `curl`, because they *are* the
same code path. Nothing new is trusted, and there is no second place to forget
tenancy.

The alternative — a server-side renderer reading the service directly — is how
most consoles are built and is the wrong shape here. It would need its own
authorisation, its own tenant filtering and its own projection of every type,
and the first endpoint added afterwards would be filtered in one place and not
the other. Feature 011 exists precisely because that class of mistake is easy.

So the server's entire contribution is: serve four static files, and answer a
question the API could not answer before (which language is configured).

Three consequences follow, and each is a design decision rather than an
accident:

**The shell is anonymous.** A browser navigating to `/ui/` cannot send an
`Authorization` header — nothing on a plain navigation can. So either the shell
is served without a credential, or there is a login endpoint that mints a
cookie, and a cookie means sessions, CSRF and a second authentication scheme.
The shell is HTML, CSS, JavaScript and a table of UI labels: it says nothing
about the estate that `/healthz` does not already say. It is served
anonymously, and the data it fetches is not.

**The credential is typed in and held for the tab.** `sessionStorage`, not
`localStorage`, so closing the tab ends it; never a URL, because URLs reach
access logs, browser history and referrer headers; never a cookie, because a
credential the browser attaches automatically is a credential that needs CSRF
defences.

**Report prose is untrusted.** The summary comes from a language model and the
log excerpts come from the estate. Neither is under our control, and the console
would be a stored-XSS vector the moment it put either into HTML. So the console
never produces HTML from data at all: every value reaches the page as
`textContent`, and a test refuses the sinks that could do otherwise.

## 2. Design decisions

| ID | Decision | Rationale |
|---|---|---|
| D-1 | The console is a client of the API; no server-side data path | One authorisation path, one tenant filter; a second one drifts |
| D-2 | The shell is anonymous, the data is not | A navigation cannot carry a bearer header, and the shell reveals nothing |
| D-3 | No cookies, no sessions, no login endpoint | No cookie means no CSRF and no session store, and the bearer scheme already exists |
| D-4 | Read-only: the console cannot start a diagnosis | It spends tokens and reads production; that credential should not sit in a browser tab |
| D-5 | No `innerHTML`, no `eval`; every value is `textContent` | Report prose is model output and estate log text — untrusted by construction |
| D-6 | A strict CSP, and therefore no inline script or style | Defence in depth behind D-5, and it costs two files |
| D-7 | Assets are an explicit allow-list, not a file server over the embed | Deny by default: adding a file to the directory must not publish it |
| D-8 | The string table lives in Go, served as JSON | Parity is then checked by a Go test, like the error-code registry, instead of by reading JavaScript |
| D-9 | Hash routing, one shell | The server stays dumb, and a run id in a fragment never reaches an access log |
| D-10 | Language: browser guess → server's configured language → the operator's choice | Each step is more authoritative than the last, and the last word is the reader's |
| D-11 | `server.ui.enabled`, defaulting to on | A console you must discover a key to enable is a console nobody uses; a hardened deployment still gets to say no |
| D-12 | Disabled answers `MAS-7016`, not a bare 404 | The operator who typed the URL is owed the reason and the key that changes it |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-1 | Model or log text executes as script in the console | No HTML sink exists in the asset; `TestConsoleNeverUsesAnHTMLSink` scans for every one of them, and the CSP forbids inline and remote script regardless |
| RSK-2 | The console becomes a way around tenancy or scope | It has no data path of its own; `TestConsoleServesNoEstateData` asserts the asset routes return only the embedded bytes |
| RSK-3 | The credential leaks through a URL, a log or persistent storage | `sessionStorage` only, header only; `TestConsoleKeepsTheCredentialOutOfURLsAndCookies` scans for the alternatives |
| RSK-4 | A rendered report reads as more certain than the diagnosis is | Gaps, `advisory`, unpriced cost and truncation are rendered as first-class content, and a test asserts each is referenced |
| RSK-5 | Structural tests over an asset prove less than a browser would | Stated plainly in the LLD as a known limit rather than implied away; the invariants chosen are the ones a scan can actually decide |
| RSK-6 | The token is pasted into a page delivered over plaintext | `httpapi.Admit` already refuses a credentialled off-host bind without TLS or a declared proxy; the console additionally warns when it is not on a secure origin |
| RSK-7 | Strings drift: referenced but absent, or present but dead | Checked in both directions by `TestConsoleStringsAreAllUsed` |

## 4. Sequencing

1. The string table in Go, and its parity test.
2. The assets: shell, stylesheet, script.
3. Serving: allow-list, CSP, the disabled path, `MAS-7016`.
4. The structural tests: HTML sinks, credential handling, honesty of the render.
5. `language` on the index; `mas doctor`; bilingual documentation.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-27 | Initial plan | HLD, LLD, tasks |
