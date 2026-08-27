# Task Breakdown: A Read-Only Web Console

> **Feature ID**: `012-web-console` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.1

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.
Every test named here must exist: `sddctl verify` checks it.

## Phase A — serving

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| TA01 | `ServerConfig.UI`, `UIConfig.On()`, default on | FR-013 | `TestConsoleCanBeDisabled` | — | done |
| TA02 | Embedded assets, the allow-list, `handleConsole`, `/ui` → `/ui/` | FR-001, FR-007, NFR-004 | `TestConsoleIsServed`, `TestConsoleServesOnlyItsOwnAssets` | TA01 | done |
| TA03 | `Routes()` reports what `routes()` registered; `TestEveryRouteIsGuarded` reads the package's own anonymous set | CON-002 | `TestEveryRouteIsGuarded` | TA02 | done |
| TA04 | Console paths anonymous; every data path still guarded | FR-002, FR-003, NFR-003 | `TestConsoleShellIsAnonymousAndDataIsNot`, `TestConsoleServesNoEstateData` | TA03 | done |
| TA05 | CSP and the other response headers | FR-006 | `TestConsoleSendsAContentSecurityPolicy` | TA02 | done |
| TA06 | `MAS-7016`, bilingual; error-code docs regenerated | FR-013, CON-004 | `mas errcodes` output current | TA01 | done |
| **G-A** | **Gate A** | | The console is served, guarded correctly, and can be switched off | | done |

## Phase B — the client

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| TB01 | The bilingual string table and `/ui/strings.json` | FR-008, NFR-002 | `TestConsoleStringsAreBilingual` | G-A | done |
| TB02 | `app.js`: state, `t`, `api`, `el`, hash routing, the five views | FR-001, FR-009, NFR-005 | `TestConsoleStringsAreAllUsed`; assets under 1500 lines | TB01 | done |
| TB03 | No HTML sink anywhere in the asset | FR-005, CON-003 | `TestConsoleNeverUsesAnHTMLSink` | TB02 | done |
| TB04 | The credential: `sessionStorage`, header only, cleared on 401 | FR-011 | `TestConsoleKeepsTheCredentialOutOfURLsAndCookies` | TB02 | done |
| TB05 | Read-only: no POST, no `diagnose` scope | FR-004, CON-001 | `TestConsoleNeverStartsADiagnosis` | TB02 | done |
| TB06 | Failures render code, message and remedy | FR-010 | `TestConsoleRendersTheErrorCode` | TB02 | done |
| TB07 | Gaps, advisory, unpriced cost and truncation rendered, not folded away | FR-012, CON-005 | `TestConsoleSurfacesGapsAndAdvisoryStatus` | TB02 | done |
| **G-B** | **Gate B** | | `go test ./internal/httpapi/...` | | done |

## Phase C — integration and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| TC01 | `language` on the API index | FR-014 | `TestIndexReportsTheLanguage` | G-B | done |
| TC02 | `mas doctor` reports the console state | FR-015 | `TestDoctorReportsTheConsole` | TC01 | done |
| TC03 | `NG-6` lifted in the goals; P3-4 recorded delivered; M4 exit met | — | `sddctl verify` cascade | TC02 | done |
| TC04 | Bilingual documentation: manual, configuration reference, README | NFR-002, NFR-001 | `sddctl verify` parity; `go.mod` unchanged | TC03 | done |
| **G-C** | **Gate C — feature exit** | | `make ci` green; `make demo` unchanged | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | TA01–TA06 | `go test ./internal/config/... ./internal/httpapi/...` |
| G-B | TB01–TB07 | `go test ./internal/httpapi/...` |
| G-C | TC01–TC04 | `make ci && make demo` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-27 | Initial task breakdown | code, config, docs |
