# Feature Specification: A Read-Only Web Console

> **Feature ID**: `012-web-console` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.2.2
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

A diagnosis is a document. It has a summary, ranked hypotheses each with its
confidence and its reasoning, findings, the evidence those rest on, the gaps
where evidence was missing, and advisory recommendations. `mas diagnose`
renders it as Markdown and the API returns it as JSON, and both are the right
answer for the person who ran it.

They are the wrong answer for everyone else. An on-call engineer at 03:00 has a
run id in a chat message and no terminal open. A team lead wants to know what
was diagnosed this week without learning `jq`. A reviewer asking "what did it
actually look at" wants to read the step trace, which is a large JSON array.

`NG-6` deferred the UI on purpose: a console over a half-built API is a console
rebuilt three times. That reason has expired. The API now authenticates, scopes,
partitions by tenant, and stores runs durably — a console can be a *client* of
it rather than a second implementation of it.

The risk this feature carries is not complexity, it is dishonesty. A rendered
report is easy to make look more certain than it is: a confident sentence in a
large font, the gaps folded away, the word "advisory" in grey. The diagnosis
this system produces is a set of hypotheses with confidences and known holes,
and the console has to show it as that or it is worse than no console.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| On-call engineer | Read a diagnosis from a phone or a browser tab with only a run id | An incident, at any hour |
| Team lead | See what has been diagnosed and how it was reasoned, without a terminal | Weekly review |
| Reviewer / auditor | Read the step trace: which tools ran, what came back, what failed | After an incident |
| Operator of a hardened deployment | Not serve a console at all | Deployment review |
| Chinese-speaking operator | Read the console in Chinese | Every day |

## 3. Scope

### In scope
- A web console served by `mas serve` at `/ui/`, built from embedded assets
  with no build step and no new module dependency.
- Views: run list, run detail (summary, hypotheses, findings, evidence, gaps,
  recommendations, usage and cost), step trace, target list, system view
  (packs, topologies, version, health).
- **Read-only**: the console renders what has been computed and cannot start a
  diagnosis.
- Authentication by the existing bearer credential, entered by the operator and
  held for the browser session only.
- Bilingual, from a string table that is checked for parity like the error-code
  registry.
- Failures rendered as their `MAS-NNNN` code, message and remedy.
- A configuration key to not serve it, and `mas doctor` reporting which it is.

### Out of scope
- **Starting a diagnosis from the browser.** It spends model tokens and reads
  production telemetry, and the credential that may do it should not be one a
  browser tab holds. The `diagnose` scope stays a machine-to-machine credential.
- **Any action against the estate.** Constitution Art. IV: nothing here writes
  anything anywhere, and the console is a reader of the API like any other.
- **Cookies, sessions or a login endpoint.** No cookie means no CSRF and no
  server-side session state; the console reuses the bearer scheme the API
  already has.
- **A JavaScript framework, a bundler or a package manifest.** NFR-001 forbids
  the dependency, and the surface here does not need one.
- **Server-side rendering of estate data.** A second data path is a second place
  to forget tenancy; every byte of estate data comes through the guarded API.
- **Charts of raw telemetry.** The console shows the diagnosis, not a
  dashboard — Grafana exists and NG-3 stands.
- **Editing configuration.** Configuration is a file under review, not a form.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | `mas serve` MUST serve a web console at `/ui/` | P0 | `TestConsoleIsServed` |
| FR-002 | Console assets MUST answer without a credential, and every data path MUST stay guarded | P0 | `TestConsoleShellIsAnonymousAndDataIsNot` |
| FR-003 | No estate data MUST be served outside the authorised API | P0 | `TestConsoleServesNoEstateData` |
| FR-004 | The console MUST NOT be able to start a diagnosis | P0 | `TestConsoleNeverStartsADiagnosis` |
| FR-005 | The console MUST NOT write data into HTML: no `innerHTML`, `document.write` or `eval` | P0 | `TestConsoleNeverUsesAnHTMLSink` |
| FR-006 | Console responses MUST carry a strict Content-Security-Policy, with no inline script or style | P0 | `TestConsoleSendsAContentSecurityPolicy` |
| FR-007 | Only allow-listed assets MUST be served under `/ui/` | P0 | `TestConsoleServesOnlyItsOwnAssets` |
| FR-008 | Every console string MUST exist in both languages | P0 | `TestConsoleStringsAreBilingual` |
| FR-009 | Every string the console references MUST exist, and every string in the table MUST be referenced | P1 | `TestConsoleStringsAreAllUsed` |
| FR-010 | A failed request MUST render its code, message and remedy | P0 | `TestConsoleRendersTheErrorCode` |
| FR-011 | The credential MUST NOT be placed in a URL, a cookie, or persistent storage | P0 | `TestConsoleKeepsTheCredentialOutOfURLsAndCookies` |
| FR-012 | Gaps, advisory status and unpriced cost MUST be rendered, not hidden | P0 | `TestConsoleSurfacesGapsAndAdvisoryStatus` |
| FR-013 | The console MUST be disableable, and MUST say so when disabled | P1 | `TestConsoleCanBeDisabled` |
| FR-014 | The API index MUST report the server's configured language | P1 | `TestIndexReportsTheLanguage` |
| FR-015 | `mas doctor` MUST report whether the console is served | P1 | `TestDoctorReportsTheConsole` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | No new module dependency and no build step | `go.mod` unchanged; no `package.json` |
| NFR-002 | Every operator-facing string bilingual | `TestConsoleStringsAreBilingual`, `sddctl verify` |
| NFR-003 | The console adds nothing to the request path of the API | Assets are static; no handler consults the console |
| NFR-004 | Assets ship inside the binary and the container image | `//go:embed`; `mas serve` needs no files on disk |
| NFR-005 | The whole console is small enough to read | Under 1500 lines across all assets |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Read-only: no action against any environment | Constitution Art. IV |
| CON-002 | One choke point: the console gets no data path that bypasses the authorizer | Art. VII.2; features 009 and 011 |
| CON-003 | Report prose is untrusted input — it comes from a model and from estate logs | §1 |
| CON-004 | Both languages for every string | Art. III |
| CON-005 | Uncertainty is shown, never smoothed away | Art. IV; `docs/en/project-goals.md` G12 |

## 7. Acceptance

The feature is done when an operator can open `/ui/`, paste a read credential,
see the runs their tenant may see, open one and read its summary, hypotheses
with confidences, findings, evidence, gaps and advisory recommendations in
either language; when the step trace is one click away; when a failure shows its
`MAS-NNNN` code; when a hardened deployment can turn the whole thing off; and
when nothing about the API's authorisation, tenancy or read-only posture has
changed because of it.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-27 | Initial specification | plan, HLD, LLD, tasks, code |
