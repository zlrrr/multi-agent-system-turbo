# Implementation Plan: MVP Core

> **Feature ID**: `001-mvp-core` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Technical context

| Aspect | Decision |
|---|---|
| Language / runtime | **Go 1.24** — single static binary, first-class concurrency, tiny container |
| CLI framework | `spf13/cobra` |
| Config | `gopkg.in/yaml.v3` + env overlay + flag overlay (hand-rolled precedence, no framework) |
| Logging | stdlib `log/slog` (JSON and text handlers), custom redacting handler |
| HTTP | stdlib `net/http` (server and clients); no web framework |
| Kubernetes access | **Purpose-built read-only REST client** over the apiserver — see §6 |
| LLM providers | Hand-written clients over `net/http`: Anthropic Messages API, OpenAI-compatible Chat Completions, plus `mock` |
| Self-metrics | Hand-rolled Prometheus text exposition (no client library) — see §6 |
| Rule expressions | `github.com/expr-lang/expr` — dependency-free expression evaluator for playbook conditions |
| Knowledge packs | YAML data files, `go:embed`-ed defaults + user directory overlay |
| Storage | Filesystem run store behind a `RunStore` interface |
| Testing | `go test`, `httptest` stubs for every external system, golden files, adversarial safety suite |
| Target platforms | linux/amd64, linux/arm64; container image from `scratch`-adjacent distroless-style base |

### Language decision (resolves project-goals ASM-G3)

The obvious counter-argument is that the multi-agent ecosystem is centred on Python
(LangGraph, AutoGen, CrewAI). The decision is **Go**, because this system's dominant work is
not model orchestration — it is *concurrent, bounded, safe I/O against operational systems*,
plus *deterministic rule evaluation*, with model calls as a comparatively thin layer on top.

| Force | Go | Python |
|---|---|---|
| Delivery as a single static binary + tiny image (G12.1) | Native | Needs interpreter + deps; image 5–10× larger |
| Concurrent bounded evidence collection with per-call timeouts | `context` + goroutines, idiomatic | `asyncio`, workable but heavier to bound correctly |
| Provably read-only enforcement, adversarially tested | Static typing + one guarded call path | Dynamism makes the guard easier to bypass |
| Operator-tool ecosystem (K8s, Prometheus) | Native home | Second-class |
| Multi-agent framework availability | Must be built | Rich (LangGraph, AutoGen) |
| Topology experimentation (G7.3) | Built on our own registry — *this is a feature*, since frameworks impose their own execution model and make like-for-like topology comparison harder | Frameworks help, but comparing across them is confounded |

The framework gap is real but narrow: what LangGraph provides that we need — a typed state
object, a step graph, and message passing — is roughly 700 lines of Go. What it does *not*
provide is the safety guard, the collectors and the knowledge packs, which are the bulk of
this system. Building the orchestration ourselves also gives G7.3 a clean experimental design:
identical state, identical tools, identical evidence, only the topology varies.

**Reversal condition** (recorded so the decision stays falsifiable): if M3 evaluation shows we
are reimplementing substantial framework functionality (structured planning DSLs, learned
routing, memory hierarchies), a Python sidecar exposing the same `Orchestrator` gRPC/HTTP
contract is added rather than rewriting the core. The topology registry is the seam that makes
that possible without touching collectors, packs or safety.

## 2. Constitution check

| Article | Requirement | Compliance | Note |
|---|---|---|---|
| I | Spec approved before design | ☑ | `spec.md` v1.0.0 |
| II | Cascade tracked | ☑ | `traceability.yaml` + `sddctl verify` |
| III | Bilingual parity | ☑ | Every artifact paired; CI-enforced |
| IV | Read-only enforced in code | ☑ | HLD §7.3 — single guarded call path |
| V | Error codes + structured logs | ☑ | `pkg/errs` registry; `internal/obs` |
| VI | Test-first checkpoints | ☑ | `tasks.md` declares tests per task |
| VII.1 | Abstractions justified | ☑ | HLD §4 justifies each of the six interfaces |
| VII.4 | Core dependency-light | ☑ | `internal/core` imports stdlib + repo only |
| VIII | Releasable `main` | ☑ | `make ci` gate |
| IX | Lights-out with recorded assumptions | ☑ | `spec.md` §7, §8 |

No deviations requiring §6 entries.

## 3. Decomposition into phases

| Phase | Outcome | Exit criterion |
|---|---|---|
| **A. Foundation** | Errors, logging, config, run context, safety guard | Guard's adversarial suite green; `mas version` runs |
| **B. Capability layer** | Tool abstraction; Prometheus, Loki, Kubernetes collectors | Each collector green against its `httptest` stub |
| **C. Knowledge & rules** | Pack schema, Redis + Kafka packs, playbook engine | Playbook run with `llm_calls == 0` produces findings |
| **D. Reasoning layer** | LLM providers, agent runtime, roles, `single` + `supervisor` topologies | Same request under both topologies produces a report |
| **E. Output & persistence** | Report model, Markdown/JSON renderers, run store, replay | Golden-file tests; `mas replay` reproduces a report |
| **F. Surfaces** | CLI, HTTP API, health, self-metrics, `doctor` | Smoke + API tests green |
| **G. Delivery** | Dockerfile, GitHub Actions, manual, config & error references | Image runs; release workflow produces artifacts |

Order is a strict dependency chain except that C and D may proceed in parallel once B lands.

## 4. Requirement → phase map

| Requirement | Phase |
|---|---|
| FR-001, FR-002 | A |
| FR-003, FR-004, FR-005, FR-006, FR-021, FR-022, FR-023 | B |
| FR-007, FR-008 | C |
| FR-009, FR-010 | D |
| FR-011, FR-012, FR-013 | E |
| FR-014, FR-015, FR-018 | F |
| FR-016, FR-017 | A (enforced), verified in every phase |
| FR-019 | D (accounting), E (persistence) |
| FR-020 | G |
| NFR-001…NFR-010 | Gated in the phase that introduces the surface they measure |

## 5. Risks

| ID | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| RSK-001 | Hand-rolled Kubernetes client mishandles auth (client certs, token files) | Medium | High | Support SA token, kubeconfig bearer token, token file, client certificate and basic auth, each unit-tested; refuse `exec` credential plugins explicitly with the alternative named (`design-lld.md` §2.9); `mas doctor` probes and reports which credential source was used |
| RSK-002 | LLM produces unparseable tool calls, stalling a run | Medium | Medium | Strict schema validation, bounded repair retries, then deterministic fallback to playbook-only conclusions |
| RSK-003 | Agent loops burn tokens without converging | Medium | Medium | Hard ceilings: max steps, max tool calls, max wall clock, max tokens; exceeded ⇒ report what was found with an explicit truncation notice |
| RSK-004 | Safety guard has a bypass | Low | Critical | Single choke point; deny-by-default; adversarial test suite; guard cannot be disabled by config |
| RSK-005 | Knowledge packs drift from reality | Medium | Medium | Packs are versioned data with conformance tests; every finding cites the pack rule ID that produced it |
| RSK-006 | Vendor telemetry API divergence (VictoriaMetrics vs Prometheus) | Low | Medium | Program against the documented v1 shapes; per-vendor quirks isolated behind the collector interface |
| RSK-007 | Report quality is unmeasurable without a corpus | High | Medium | Accepted for M1; M3 delivers the evaluation harness. M1 asserts *structural* quality (evidence citation, no unsupported claims) |

## 6. Complexity tracking

| Deviation | Why necessary | Simpler alternative rejected because |
|---|---|---|
| Purpose-built Kubernetes REST client instead of `client.go` | `client-go` adds ~40 MB and a large transitive tree for the ~6 read-only endpoints we use; it also drags in a schema surface far wider than our allow-list, weakening CON-002 | Depending on `client-go` was rejected on image size (NFR-005), on audit surface, and because a narrow client makes the read-only guarantee inspectable |
| Hand-rolled Prometheus exposition for self-metrics | Fewer than 15 series; the client library's registry/collector machinery is unused weight | `prometheus/client_golang` rejected: dependency weight exceeds the value for this surface |
| Own agent-orchestration layer rather than a framework | See §1; required by G7.3's like-for-like topology comparison | An existing framework was rejected because its execution model becomes a confounder in topology experiments |

## 7. Definition of done

- [ ] Every phase's exit criterion met.
- [ ] `make ci` green locally and in GitHub Actions.
- [ ] `sddctl verify` reports no stale artifacts and full requirement coverage.
- [ ] Container image published by the release workflow with checksums.
- [ ] Bilingual user manual, configuration reference and error-code reference published.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-23 | Initial plan; resolves ASM-G3 in favour of Go with a recorded reversal condition | `design-hld.md` |
