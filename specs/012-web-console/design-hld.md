# High-Level Design (HLD): A Read-Only Web Console

> **Feature ID**: `012-web-console` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
  browser
    │  GET /ui/            (anonymous: html, css, js, strings.json)
    ▼
  ┌──────────────────────────────────────────────────────────┐
  │  console assets — embedded, static, allow-listed         │
  └──────────────────────────────────────────────────────────┘
    │
    │  fetch(..., {headers: {Authorization: "Bearer …"}})
    ▼
  ┌──────────────────────────────────────────────────────────┐
  │  Authorizer (009) ──► Principal ──► MayReach (011)       │
  └──────────────────────────────────────────────────────────┘
    │
    ▼
  GET /api/v1/diagnoses · /diagnoses/{id}[?steps=true]
      /targets · /topologies · /packs · /
```

The console is below the authorizer, not beside it. There is no arrow from the
asset handler to the service, and that absence is the design: everything the
console displays crosses the same guard `curl` crosses, so scope and tenancy are
enforced once rather than twice.

The server's whole contribution is the top box — four files it already holds in
its binary — plus one new field on the API index.

## 2. The shell is anonymous; the data is not

A browser navigating to a page cannot attach an `Authorization` header. Nothing
on a plain navigation can. That leaves two shapes:

| Shape | Cost |
|---|---|
| Login endpoint minting a cookie | A session store, a CSRF defence on every state-changing route, a second authentication scheme to keep in step with the first, and a credential the browser attaches to every request automatically |
| Anonymous shell, credential typed in, sent as a header | Nothing new on the server; the existing bearer scheme, used from JavaScript |

The second is chosen. What is served anonymously is HTML with no data in it, a
stylesheet, a script, and a table of UI labels in two languages. It discloses
that MAS-Turbo is running, which `/healthz` already discloses, and nothing else:
no target name, no run, no version of the estate, no tenant.

The operator pastes a credential; it is held in `sessionStorage` for that tab
and sent as `Authorization: Bearer` on each `fetch`. Closing the tab ends it.
It never enters a URL — URLs reach access logs, browser history and referrer
headers — and never a cookie, because a credential the browser sends
automatically is one that needs CSRF defences that a bearer header does not.

On a plaintext origin that is not loopback, the console says so before it
accepts anything. `httpapi.Admit` already refuses to bind a credentialled API
off-host without TLS or a declared terminating proxy; the banner is the same
rule restated where the person typing can see it.

## 3. Read-only, twice over

The system is read-only against the estate by constitution. The console is
read-only against the *system* as well: it renders what has been computed and
offers no way to start a diagnosis.

That is a deliberate narrowing, not an oversight. Starting a diagnosis spends
model tokens and reads production telemetry, and it needs the `diagnose` scope.
A browser tab is a poor place for that credential: it is shoulder-surfable, it
is pasted into whatever window is open, and a console that can spend money is a
console someone will leave logged in. `diagnose` stays a machine-to-machine
scope, held by the CLI, by CI and by whatever automation the operator writes.

The console needs `read`, and `read` is the scope a status page should have.

## 4. Report prose is untrusted input

The report the console renders contains:

- a summary and per-hypothesis reasoning written by a language model,
- log lines and error strings quoted from the estate,
- target names, symptoms and notes typed by whoever asked.

None of it is under our control. A console that turned any of it into HTML would
be a stored-XSS vector reachable by anyone who can write a log line on a
monitored server — which, on a middleware host, is close to everyone.

So the console never produces HTML from data. Elements are created and their
`textContent` set; no string from a response is ever parsed as markup. The sinks
that could do otherwise — `innerHTML`, `outerHTML`, `insertAdjacentHTML`,
`document.write`, `eval`, `new Function` — do not appear in the asset, and a test
refuses them so they cannot appear later.

A strict `Content-Security-Policy` sits behind that as defence in depth:
`default-src 'none'`, script and style from `'self'` only, `connect-src 'self'`,
no inline anything. It costs one header and the discipline of keeping CSS and
JavaScript in their own files, which is where they belong.

## 5. Honesty is a rendering requirement

The output of this system is not an answer. It is a ranked set of hypotheses,
each with a confidence, resting on evidence that is sometimes incomplete, with
recommendations that are advisory and a cost that is sometimes unknown. A
console can destroy all of that nuance with a large font and a collapsed
section, and it would still look like a working console.

So the console renders, as first-class content and never behind a disclosure:

| What | Why it cannot be folded away |
|---|---|
| Every hypothesis's confidence and status | A supported hypothesis at 55% is a different claim from one at 95% |
| Refuted hypotheses, kept visible | What was ruled out is half of what was learned |
| Gaps, with their code and impact | A diagnosis reached without the logs is a diagnosis with a hole in it, and the hole is the first thing an on-call reader needs |
| `advisory` on every recommendation | Constitution Art. IV: this system suggests, it never acts, and the reader must not be able to forget that |
| Unpriced models and partial cost | A cost that says nothing is honest; a cost that says zero is a lie |
| `truncated` and notes | A run that hit a limit produced less than it might have |

## 6. Strings live in Go

The console's own labels are a bilingual table in Go, served as JSON. They could
have lived in the JavaScript, and then their parity would be checked by reading
JavaScript from a Go test, or not at all.

In Go they are checked the way the error-code registry is checked: every id has
both languages or the build fails, in a test that reads the actual table rather
than a copy of it. The script contains no operator-facing prose at all — it
references ids — so the question "is this console bilingual" has one place to
look.

Language is resolved in three steps, each more authoritative than the last:

1. the browser's own language, which is a guess available before any credential;
2. the server's configured `run.language`, once the index has been read, because
   an operator who configured Chinese wants Chinese;
3. the reader's explicit choice, which wins and lasts for the tab.

## 7. Turning it off

`server.ui.enabled` defaults to on. A console that requires discovering a
configuration key is a console nobody uses, and "UI serving reports" is an exit
condition of this milestone.

A deployment that does not want one sets it false, and then `/ui/` answers
`MAS-7016` rather than a bare 404 — the operator who typed the URL is owed the
reason and the key that changes it, and the fact that a console is switched off
is not information worth withholding.

`mas doctor` reports which it is, next to the API's authentication and tenancy
state, because "is the console on" is asked in the same breath as those.

## 8. What this deliberately does not do

- **No dashboard.** NG-3 stands: this system queries observability stacks, it is
  not one. The console shows a diagnosis; Grafana shows a time series.
- **No configuration editing.** Configuration is a reviewed file, and a form
  that writes YAML is a form that bypasses review.
- **No framework and no build step.** A bundler would add a toolchain to a
  repository whose delivery story is "one static binary and one container",
  and this surface does not need one.
- **No websocket.** A run in flight is polled while its detail view is open,
  which is a `setTimeout` rather than a protocol.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-27 | Initial high-level design | LLD, tasks |
