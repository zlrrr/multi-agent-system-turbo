# Feature Specification: MVP Core — Diagnostic Multi-Agent System

> **Feature ID**: `001-mvp-core` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.0.0
> **Constitution**: `.specify/memory/constitution.md` v1.0.0
> **Downstream**: `plan.md`

## 1. Problem statement

When a Redis cluster starts evicting keys, a Kafka consumer group falls behind, or a MongoDB
primary starts rejecting writes, the operator's first thirty minutes are spent *assembling
context*: opening Grafana, guessing the right PromQL, tailing pods, remembering which `INFO`
field matters for this failure, checking whether the deployed version has a known bug. The
analysis itself — the part that requires expertise — starts only after that assembly is done,
and is often performed under pressure by whoever is on call rather than by whoever knows the
system best.

The cost is measured in mean-time-to-diagnosis, in escalations that were avoidable, and in
incident reports that record *what was restarted* rather than *what went wrong*.

There is no tool that (a) gathers this heterogeneous evidence automatically, (b) applies
middleware-specific expert knowledge to it, (c) shows its reasoning and its evidence, and
(d) is trustworthy enough to point at production because it provably cannot touch it.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| SRE / on-call engineer | Get a ranked, evidence-backed explanation of a live symptom in minutes | Alert fires; user runs `mas diagnose` |
| Middleware platform engineer | Encode team expertise once, so triage does not depend on who is awake | Writes/extends a knowledge pack |
| Support engineer | Analyse a customer's exported telemetry without cluster access | Offline mode over a snapshot |
| Applied-AI researcher | Compare multi-agent topologies on identical, realistic diagnostic cases | Runs the same case under `single` vs `supervisor` |

### Primary user journey
1. The operator configures targets and telemetry endpoints once, in `mas.yaml`.
2. An alert fires: *"redis-prod-0 latency p99 up 8×"*.
3. The operator runs `mas diagnose --target redis-prod --symptom "p99 latency spike" --since 1h`.
4. MAS-Turbo resolves the target, loads the Redis knowledge pack, and runs deterministic
   playbook checks first (memory pressure, eviction, fork latency, slowlog, connection churn).
5. Where the deterministic layer is inconclusive, agents form hypotheses and request further
   targeted evidence — specific PromQL series, specific LogQL windows, specific read-only
   `INFO`/`CLIENT LIST` inspections.
6. A critic agent challenges each hypothesis against the collected evidence; unsupported
   hypotheses are dropped or downgraded.
7. The operator reads a Markdown report: ranked hypotheses with confidence, the evidence for
   each, what was checked and found normal, what could not be checked, and advisory next steps
   with risk labels.
8. The run is stored and can be replayed, shared, or re-run under a different topology.

## 3. Scope

### In scope (M1)
- Diagnostic run lifecycle: request → plan → evidence collection → analysis → critique → report.
- Telemetry collectors: Prometheus-compatible metrics; Loki-compatible logs.
- Environment adapters: Kubernetes (read-only API reads) and local host (allow-listed read-only
  inspection commands).
- Source acquisition: network repository with automatic fallback to a local mirror, plus
  code search over the fetched tree.
- Knowledge packs: schema + Redis + Kafka.
- Deterministic playbook engine, usable standalone and as an agent tool.
- Agent runtime with five roles; two interchangeable topologies (`single`, `supervisor`).
- LLM provider abstraction: `mock`, `anthropic`, `openai` (OpenAI-compatible endpoints).
- Safety guard enforcing read-only, deny-by-default, redaction and resource ceilings.
- Error-code registry, structured logging, run records, replay.
- CLI and HTTP API; container image; GitHub Actions release; bilingual user manual.

### Out of scope (M1 — deferred, not cancelled)
- In-container command execution in Kubernetes (`exec` subresource) (M2 · P1-1).
- Local target auto-discovery and middleware config-file parsing (M2 · P1-1).
- Deep source analysis: build graphs, cross-version diffing, symbol indexing (M2 · P1-2).
- MongoDB / Pulsar / Milvus / OceanBase packs (M2 · P1-3).
- `plan-execute` / `debate` / `blackboard` topologies (M2 · P1-4).
- Evaluation harness and case corpus (M3).
- Authentication, durable non-filesystem store, Web UI (M4).
- Everything in project-goals §4 non-goals — permanently out of scope for this phase.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | The system MUST accept a diagnostic request specifying target, symptom, time window, mode and topology, and MUST reject malformed requests with a coded error | P0 | Unit test: valid request admitted; each malformed field yields its documented `MAS-1xxx` code |
| FR-002 | The system MUST resolve a named target to its middleware kind, version (when discoverable), environment binding and telemetry label selectors | P0 | Resolving a configured target returns a populated `Target`; unknown target → `MAS-1005` |
| FR-003 | The system MUST query Prometheus-compatible endpoints for instant and range series, honouring configured auth, timeout and result-size ceilings | P0 | Integration test against an httptest Prometheus stub, incl. auth header and truncation |
| FR-004 | The system MUST query Loki-compatible endpoints for log lines over a time window with a bounded result count | P0 | Integration test against an httptest Loki stub |
| FR-005 | The system MUST read Kubernetes pods, pod logs, events and nodes for a target namespace using read-only API access | P0 | Integration test against an httptest Kubernetes API stub |
| FR-006 | The system MUST refuse any tool invocation that is not on the read-only allow-list, before the invocation reaches any external system | P0 | Adversarial unit tests: mutating verbs, mutating CLI subcommands, path traversal, shell metacharacters, all refused with `MAS-8xxx` |
| FR-007 | The system MUST load middleware knowledge packs from embedded defaults and from a user-supplied directory, validating them against a published schema | P0 | Valid pack loads; each schema violation yields its `MAS-5xxx` code |
| FR-008 | The system MUST execute deterministic playbooks — ordered steps of collect → evaluate → conclude — with zero LLM calls | P0 | Playbook run against a mock provider records `llm_calls == 0` |
| FR-009 | The system MUST support at least two interchangeable agent topologies selectable per run without code changes | P0 | Same request under `single` and `supervisor` produces reports from different orchestrators |
| FR-010 | The system MUST support at least two LLM providers plus a deterministic mock, selectable by configuration, with per-agent model override | P0 | Provider registry test; per-agent override honoured |
| FR-011 | The system MUST produce a report containing ranked hypotheses, per-hypothesis confidence and evidence citations, checks that passed, gaps, and advisory recommendations with risk labels | P0 | Golden-file test on the renderer for Markdown and JSON |
| FR-012 | The system MUST persist a replayable run record: request, plan, every tool call with arguments and redacted results, every model exchange, and the final report | P0 | `mas replay <run-id>` reproduces the report without network or model access |
| FR-013 | The system MUST degrade when a data source is unavailable: the run continues, the gap is recorded, and the report states the gap's effect on confidence | P0 | Test with metrics endpoint down: run completes, report lists the gap |
| FR-014 | The system MUST expose a CLI with `diagnose`, `serve`, `doctor`, `replay`, `errcodes`, `packs`, `topologies`, `version` | P0 | CLI smoke tests for each subcommand |
| FR-015 | The system MUST expose an HTTP API to create a diagnosis, fetch its result, list runs, and report health and self-metrics | P0 | API tests over `httptest` for each endpoint |
| FR-016 | The system MUST redact credentials and configured sensitive patterns from logs, reports, run records and model prompts | P0 | Redaction tests inject secrets at each boundary and assert absence |
| FR-017 | The system MUST emit structured logs carrying `run_id`, and MUST attach a `MAS-NNNN` code to every error crossing a boundary | P0 | Log capture test; boundary-error audit test |
| FR-018 | The system MUST validate configuration and probe every configured endpoint on demand, reporting per-check status with codes | P0 | `mas doctor` against stubs reports per-check status |
| FR-019 | The system SHOULD record per-run token, cost, latency and tool-call accounting | P1 | Run record contains a populated `Usage` block |
| FR-020 | The system MUST ship a container image whose entrypoint is the CLI, runnable non-root | P0 | Image builds; `docker run … version` and `… diagnose` succeed |
| FR-021 | The system MUST inspect a local host read-only — process presence, listening ports, resource usage, and allow-listed diagnostic commands — for middleware deployed outside Kubernetes | P0 | Unit tests with a stubbed command runner; adversarial tests confirm non-allow-listed commands are refused |
| FR-022 | The system MUST acquire middleware source from a network repository, MUST fall back automatically to a configured local mirror when the network is unavailable, and MUST report which source was used | P0 | Test with an unreachable remote asserts fallback occurred and is recorded in the run |
| FR-023 | The system MUST search acquired source for literal strings, symbols and log-format patterns, so an observed log line can be located in code | P0 | Search over a fixture tree returns file, line and surrounding context |

## 5. Non-functional requirements

| ID | Category | Requirement | Measurement |
|---|---|---|---|
| NFR-001 | Latency | End-to-end diagnosis with mock provider and stub telemetry completes in < 5 s | Integration test asserts wall clock |
| NFR-002 | Latency | A deterministic playbook run completes in < 2 s | Test asserts wall clock |
| NFR-003 | Safety | Zero mutating operations reach any target under any input, including adversarial | Adversarial test suite |
| NFR-004 | Resource bounds | Every external call has a timeout, a response-size cap and a concurrency limit | Unit tests per collector |
| NFR-005 | Portability | Static binary for linux/amd64 and linux/arm64; image < 100 MB | CI build matrix + image size gate |
| NFR-006 | Testability | No test requires network access or a live model | CI runs with egress disabled |
| NFR-007 | Extensibility | Adding a middleware requires only a knowledge-pack file | Pack-only test adds a synthetic middleware |
| NFR-008 | Observability | Logs are structured, correlatable by `run_id`, and level-configurable | Log tests |
| NFR-009 | Documentation | 100 % bilingual parity across `docs/`, `specs/`, `.specify/` | `sddctl verify` in CI |
| NFR-010 | Determinism | Identical input + mock provider ⇒ identical report | Repeat-run equality test |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Read-only against every target environment; enforced in code | Constitution Art. IV.1 |
| CON-002 | Deny-by-default allow-listing for commands and API paths | Constitution Art. IV.2 |
| CON-003 | Output is advisory; the system never claims to have acted | Constitution Art. IV.3 |
| CON-004 | Every document bilingual, updated in the same commit | Constitution Art. III |
| CON-005 | Every boundary error carries a registry error code | Constitution Art. V.2 |
| CON-006 | Tests are the gate for task completion | Constitution Art. VI.2 |
| CON-007 | An interface requires ≥2 implementations or a test seam | Constitution Art. VII.1 |
| CON-008 | Deterministic checks precede model reasoning | Constitution Art. VII.3 |

## 7. Assumptions

| ID | Assumption | If wrong |
|---|---|---|
| ASM-001 | Targets are declared in configuration; discovery is not required for M1 | Add a discovery collector (M2) |
| ASM-002 | Prometheus HTTP API v1 and Loki HTTP API v1 shapes are sufficient | Add per-vendor adapters behind the same interface |
| ASM-003 | Read-only Kubernetes credentials (SA token or kubeconfig) are available | Only offline mode is usable; `mas doctor` reports it |
| ASM-004 | A filesystem run store suffices for M1 | Pluggable store interface already isolates this (M4) |
| ASM-005 | LLM tool-calling is available on the configured provider, or can be emulated via structured output | Mock and OpenAI-compatible paths both exercise the emulation path |
| ASM-006 | A `git` client is available on the host or in the image for source acquisition | Source capability reports unavailable via `mas doctor`; the run degrades per FR-013 |

## 8. Open questions

| ID | Question | Blocking? | Default taken |
|---|---|---|---|
| OQ-001 | Which Redis/Kafka failure modes must ship in the M1 packs? | No | Ship the ten highest-frequency modes per pack, drawn from upstream operational docs; extend in M2 |
| OQ-002 | Should online mode be opt-in or opt-out? | No | **Opt-in** — offline is the default; online requires explicit `--mode online` and configured credentials |
| OQ-003 | Report format for machine consumers? | No | JSON alongside Markdown, schema versioned as `report/v1` |
| OQ-004 | Default topology? | No | `supervisor` — best cost/quality balance in mainstream practice |

## 9. Acceptance criteria (feature-level "done")

- [ ] FR-001 … FR-018 and FR-020 … FR-023 verified by automated tests; FR-019 present in the run record.
- [ ] All NFRs measured by a test or a CI gate.
- [ ] `make ci` green: format, vet, lint, unit, integration, build, SDD verification.
- [ ] Container image built, non-root, runs `mas diagnose` end-to-end.
- [ ] GitHub Actions produces image + binaries + checksums for a tag.
- [ ] Bilingual user manual, configuration reference and error-code reference published.
- [ ] Adversarial safety suite passes with zero mutating operations reaching a target.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-23 | Initial specification derived from project-goals v1.0.0 M1 backlog | `plan.md`, `design-hld.md`, `design-lld.md`, `tasks.md` |
