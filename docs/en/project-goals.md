# Project Goals — multi-agent-system-turbo (MAS-Turbo)

> **Version**: 1.3.0 · **Status**: approved · **Date**: 2026-08-27
> **Bilingual pair**: [`../zh/project-goals.md`](../zh/project-goals.md)
> **Governed by**: [`.specify/memory/constitution.md`](../../.specify/memory/constitution.md) v1.0.0
> **Downstream**: `specs/001-*/spec.md`, `specs/002-*/spec.md`, …
>
> This is the **root artifact** of the SDD chain. Amending it cascades to every specification
> below it (Constitution Art. II). Goal correction is permitted and must be logged in §8.

---

## 1. Mission

Build an **LLM-driven multi-agent system that diagnoses runtime problems in open-source
middleware** — Redis, MongoDB, Pulsar, Kafka, OceanBase, Milvus and others — by correlating
metrics, logs, live cluster state and upstream source code, and that returns an evidence-backed
analysis with **recommended** remediation for a human operator.

The system **never mutates the target environment**. It reads, reasons, and advises.

## 2. Why this is hard (and why an agent system is the right shape)

Middleware incident triage is a *search problem over heterogeneous evidence under time
pressure*. The signal that explains an outage may live in a PromQL series, a log line 40
minutes earlier, a Kubernetes event, a `redis-cli INFO` field, or a known upstream bug in the
version actually deployed. An expert SRE closes that loop by iteratively forming and discarding
hypotheses. That loop — hypothesise → gather targeted evidence → refute or confirm → escalate
or conclude — is exactly what a well-structured agent system can carry, provided:

- evidence collection is **cheap, safe and bounded** (read-only, allow-listed, rate-limited);
- domain knowledge is **explicit and versioned**, not smuggled into prompts;
- deterministic checks run **before** any model is asked to think (Constitution Art. VII.3);
- every conclusion is **traceable to the evidence that produced it**.

## 3. Goal decomposition

The mission statement is decomposed exhaustively into goal areas. Each goal area carries an ID
used by downstream specifications.

### G1 — Diagnostic core
| ID | Goal | Success signal |
|---|---|---|
| G1.1 | Accept a diagnostic request naming a target middleware instance, a symptom, and a time window | A request is validated and admitted, or rejected with a coded error |
| G1.2 | Produce a ranked set of hypotheses with confidence and supporting evidence | Report contains ≥1 hypothesis, each citing ≥1 evidence item |
| G1.3 | Produce recommended, **advisory-only** remediation steps | Every step is labelled with risk and requires human execution |
| G1.4 | Every run is reproducible and auditable after the fact | Run record replays evidence, prompts, tool calls, verdicts |
| G1.5 | Degrade usefully when a data source is missing | Report states which sources were unavailable and what that costs the conclusion |

### G2 — Middleware coverage
| ID | Goal | Success signal |
|---|---|---|
| G2.1 | Middleware-specific knowledge is a **data pack**, not code | Adding middleware requires no recompilation |
| G2.2 | Ship packs for Redis, Kafka, MongoDB, Pulsar, Milvus, OceanBase | Each pack declares signals, log patterns, failure modes, playbooks |
| G2.3 | Packs are version-aware | A pack rule may be scoped to a version range |
| G2.4 | Third parties can author packs | Pack schema is published and validated |

### G3 — Telemetry ingestion
| ID | Goal | Success signal |
|---|---|---|
| G3.1 | Query Prometheus-compatible metrics (Prometheus, VictoriaMetrics, Thanos, Mimir) | Instant + range queries with auth |
| G3.2 | Query Loki-compatible logs | LogQL queries, label discovery, bounded result sets |
| G3.3 | Endpoints, auth and label mappings are **user-configured** | No hardcoded endpoint anywhere |
| G3.4 | Telemetry sources are pluggable behind one interface | A new source is a registry entry |

### G4 — Operating modes
| ID | Goal | Success signal |
|---|---|---|
| G4.1 | **Offline mode**: analyse from telemetry/log snapshots only | Runs with no access to the live cluster |
| G4.2 | **Online mode**: execute read-only inspection commands in the live environment | Commands pass the safety guard or are refused |
| G4.3 | Mode is selectable per run and per target | Configuration and request both can constrain it |

### G5 — Environment adapters
| ID | Goal | Success signal |
|---|---|---|
| G5.1 | Kubernetes adapter (primary): pods, events, logs, nodes, container exec (read-only) | Works in-cluster and via kubeconfig |
| G5.2 | Local/binary adapter: processes, ports, resource usage, config and log files | Works where middleware runs outside Kubernetes |
| G5.3 | Adapters share one interface so agents are environment-agnostic | Same agent code drives both |

### G6 — Source code acquisition
| ID | Goal | Success signal |
|---|---|---|
| G6.1 | Fetch middleware source from a **network** repository | Clone/fetch by ref |
| G6.2 | Fetch from a **local** repository or mirror | Works fully air-gapped |
| G6.3 | **Automatic fallback to local when the network is unavailable** | Network failure produces a fallback, not a run failure |
| G6.4 | Search source for symbols, error strings and log formats | An observed log line can be located in source |

### G7 — Multi-agent architecture
| ID | Goal | Success signal |
|---|---|---|
| G7.1 | Roles follow mainstream practice: planning, specialised investigation, correlation, critique, reporting | Roles are explicit and separately testable |
| G7.2 | The **topology is switchable at runtime** | `topology: supervisor` → `topology: debate` changes behaviour, not code |
| G7.3 | Topologies are comparable under identical inputs — the system is an experiment platform | Same case, N topologies, comparable metrics |
| G7.4 | Per-run accounting of tokens, cost, latency, tool calls | Every run record carries them |

### G8 — Model pluggability
| ID | Goal | Success signal |
|---|---|---|
| G8.1 | Switch LLM provider by configuration | Anthropic / OpenAI-compatible / local |
| G8.2 | Different agents may use different models | Cheap model for extraction, strong model for reasoning |
| G8.3 | A deterministic mock provider exists for tests | No test needs a network |

### G9 — Deterministic (rule-based) execution
| ID | Goal | Success signal |
|---|---|---|
| G9.1 | Strictly-regular workflows run as **rules, with no model in the loop** | A playbook run makes zero LLM calls |
| G9.2 | Rules and agents compose: a playbook is callable as an agent tool | Agents delegate to deterministic checks |
| G9.3 | A run may be fully rule-based, fully agentic, or hybrid | Selectable per run |

### G10 — Safety
| ID | Goal | Success signal |
|---|---|---|
| G10.1 | **No mutating operation, ever** — enforced in code, not by prompt | Guard rejects; guard is unit-tested adversarially |
| G10.2 | Deny-by-default allow-listing of commands and API paths | Unknown command → refused with a coded error |
| G10.3 | Credentials never appear in logs, reports or prompts | Redaction is tested |
| G10.4 | Resource ceilings on every external call | Timeout, size cap, rate limit |

### G11 — Operability of the tool itself
| ID | Goal | Success signal |
|---|---|---|
| G11.1 | Structured logs correlatable by `run_id` end-to-end | Any log line locates its run |
| G11.2 | Stable `MAS-NNNN` error codes with bilingual message + remediation | `mas errcodes` prints the registry |
| G11.3 | Self-diagnosis command validates configuration and connectivity | `mas doctor` |
| G11.4 | The service exposes its own health and Prometheus metrics | `/healthz`, `/readyz`, `/metrics` |

### G12 — Delivery
| ID | Goal | Success signal |
|---|---|---|
| G12.1 | Container image is the primary artifact | `docker run … mas diagnose` works |
| G12.2 | GitHub Actions produces versioned, reproducible artifacts | Tag → image + binaries + checksums |
| G12.3 | Bilingual user manual ships with the artifact | `docs/en/user-manual.md`, `docs/zh/user-manual.md` |

### G13 — Process
| ID | Goal | Success signal |
|---|---|---|
| G13.1 | SDD chain enforced and machine-verified | `make sdd-verify` |
| G13.2 | Every document bilingual and updated in lockstep | Parity check in CI |
| G13.3 | Upstream changes cascade downward, tracked not assumed | Stale-artifact detection |
| G13.4 | Autonomous ("lights-out") execution while unblocked | Assumptions logged, not silently taken |

## 4. Explicit non-goals (this phase)

| ID | Non-goal | Rationale |
|---|---|---|
| NG-1 | Any write, restart, scale, failover or config change | Constitution Art. IV — advisory system only |
| NG-2 | Autonomous remediation or closed-loop control | Follows from NG-1 |
| NG-3 | Being a metrics/log **store** | It queries existing observability stacks |
| NG-4 | Replacing alerting or on-call paging | It is the analysis layer, not the notification layer |
| NG-5 | Fine-tuning or hosting models | Provider-agnostic client only |
| NG-6 | ~~A web UI~~ | **Lifted at v1.3.0.** Deferred while the API was half-built, because a console over a moving API is a console rebuilt three times. Delivered as a read-only client of that API (`specs/012-web-console`) — it renders diagnoses and starts nothing |

## 5. Prioritised backlog (industry practice: walking skeleton → depth → breadth)

Prioritisation rule, applied consistently:
**priority = (risk retired × user value) ÷ cost**, with a hard constraint that the *walking
skeleton* — one thin end-to-end path through every architectural layer — is built before any
layer is deepened. This is what makes the first milestone genuinely usable rather than a set of
disconnected parts.

### Milestone M1 — usable MVP *(first key milestone; the current target)*
| Rank | Item | Goals | Why here |
|---|---|---|---|
| P0-1 | SDD scaffold, constitution, bilingual doc harness | G13 | Everything else is produced through it |
| P0-2 | Error-code registry + structured logging + run context | G11 | Cross-cutting; retrofitting is expensive |
| P0-3 | Configuration model & precedence (file → env → flags) | G3.3, G4.3 | Every component reads it |
| P0-4 | Safety guard (deny-by-default, redaction, ceilings) | G10 | Constitutional invariant; must precede any collector |
| P0-5 | LLM provider interface + mock + Anthropic + OpenAI-compatible | G8 | The seam that makes agents testable |
| P0-6 | Tool/capability layer with schema + guard integration | G4, G10 | Shared substrate for collectors and agents |
| P0-7 | Prometheus/VictoriaMetrics collector | G3.1 | Highest-signal evidence source |
| P0-8 | Loki collector | G3.2 | Second-highest signal source |
| P0-9 | Kubernetes read-only adapter | G5.1 | Dominant deployment environment |
| P0-10 | Knowledge-pack schema + Redis pack + Kafka pack | G2 | Turns raw data into domain judgement |
| P0-11 | Deterministic rule/playbook engine | G9 | Answers most common cases with zero model cost |
| P0-12 | Agent runtime + roles (planner, investigators, correlator, critic, reporter) | G7.1 | The product |
| P0-13 | Topology registry + `single` + `supervisor` | G7.2 | Proves the switchability seam early |
| P0-14 | Evidence store, run record, replay | G1.4 | Auditability is a constitutional requirement |
| P0-15 | Report renderer (Markdown + JSON) | G1.2, G1.3 | The deliverable a human reads |
| P0-16 | CLI (`diagnose`, `serve`, `doctor`, `errcodes`, …) | G11.3, G12.1 | The usable surface |
| P0-17 | HTTP API + health + self-metrics | G11.4 | Integration surface |
| P0-18 | Dockerfile + GitHub Actions release pipeline | G12.1, G12.2 | "Directly usable artifact" |
| P0-19 | Bilingual user manual | G12.3 | Delivery requirement |
| P0-20 | Test suite green end-to-end | Art. VI | Milestone exit gate |

### Milestone M2 — depth
| Rank | Item | Goals | Status |
|---|---|---|---|
| P1-1 | Local/binary environment adapter | G5.2 | Delivered in M1 |
| P1-2 | Source-code acquisition with network→local fallback + code search | G6 | Delivered in M1 |
| P1-3 | Knowledge packs: MongoDB, Pulsar, Milvus, OceanBase | G2.2 | Delivered (`specs/002-middleware-packs`) |
| P1-4 | Topologies: `plan-execute`, `debate`, `blackboard` | G7.2 | Delivered (`specs/003-switchable-topologies`) |
| P1-5 | Per-agent model routing | G8.2 | Delivered (`specs/005-model-routing-and-cost`) |
| P1-6 | Cost/latency/token accounting surfaced per run | G7.4 | Delivered, per role (`specs/005-model-routing-and-cost`) |
| P1-7 | Kubernetes in-container `exec` (read-only commands) | G5.1 | Delivered (`specs/004-kube-exec`) |

### Milestone M3 — experimentation & quality
| Rank | Item | Goals | Note |
|---|---|---|---|
| P2-1 | Case corpus + evaluation harness (topology A/B) | G7.3 | Delivered (`specs/006-eval-harness`): 21 cases, ≥3 per pack, one healthy deployment, both sources withheld somewhere |
| P2-2 | Version-scoped pack rules | G2.3 | Delivered (`specs/007-version-scoped-rules`): any rule may carry a range, a rename is two variants of one id, and resolution happens once per run |
| P2-3 | Pack authoring guide + schema publication | G2.4 | *Pulled forward: delivered with P1-3, because four new packs written against an unwritten contract would have fixed the contract by accident* |
| P2-4 | Regression scoring across model/topology matrix | G7.3 | Delivered (`specs/008-regression-baselines`): a baseline per (case, topology, model) cell, regressions and improvements never netted, and a known-bad cell that stays listed without failing the build |

### Milestone M4 — hardening
| Rank | Item | Goals | Note |
|---|---|---|---|
| P3-1 | AuthN/AuthZ on the HTTP API | — | Delivered (`specs/009-api-authentication`): bearer tokens with `read`/`diagnose` scopes, a refusal to bind off-host without them, and the principal recorded on the run |
| P3-2 | Durable run store (beyond filesystem) | G1.4 | Delivered (`specs/010-object-run-store`): any S3-compatible bucket, shared by every replica, with each step an immutable object and an interrupted run still readable |
| P3-3 | Multi-tenant target registry | — | Delivered (`specs/011-tenant-registry`): a tenant on a target and on a credential, enforced at one choke point, with a cross-tenant target refused as if it did not exist |
| P3-4 | Web UI | NG-6 lifted | Delivered (`specs/012-web-console`): a read-only console at `/ui/`, built from embedded assets with no new dependency and no build step, authorised by the existing bearer credential and rendering gaps, confidences and advisory status as first-class content |

## 6. Milestone exit criteria

| Milestone | Exit criterion | Status |
|---|---|---|
| **M1** | `make ci` green; container image runs `mas diagnose` end-to-end against a mock provider and a fixture telemetry stack; report produced; manual published; release workflow produces artifacts | **Met.** `make ci` green (format, vet, lint, race tests, SDD checks, build); `make demo` produces English and Chinese reports from stub telemetry with no credentials; a container running as uid 65532 completes a diagnosis and returns the documented exit codes; bilingual manual, configuration and error-code references published |
| M2 | All six knowledge packs pass their pack-conformance tests; Kubernetes in-container `exec`; ≥4 topologies selectable | **Met, and the milestone is complete.** Six packs pass one conformance contract; in-container `exec` is opt-out and shares the host allow-list; five topologies are selectable against that same contract. Source fallback was already proven under simulated network failure in M1 |
| M3 | Case corpus of ≥20 scenarios; topology comparison report reproducible by one command | **Met, and the milestone is complete.** `mas eval --matrix` runs 22 cases against all five topologies in about a second and gates on a recorded baseline. Mutation-tested in both directions and on both gates: widening a pack rule turns cells `WRONG`, narrowing one turns them `MISS`, and removing a version range turns the KRaft case red. Every ranked M3 item — P2-1 through P2-4 — is delivered |
| M4 | API authenticated; run store pluggable; UI serving reports | **Met, and the milestone is complete.** The API authenticates and authorises per route, refuses to bind off-host without credentials and TLS, and partitions targets and runs by tenant. The run store is pluggable with a shared, durable S3-compatible backend beside the filesystem one. The console at `/ui/` serves reports as a client of that API, so scope and tenancy are enforced once rather than twice. Every ranked M4 item — P3-1 through P3-4 — is delivered |

## 7. Measures of success

| Metric | Target (M1) |
|---|---|
| End-to-end diagnosis wall-clock, mock provider | < 5 s |
| Deterministic playbook run, no LLM | < 2 s |
| Unit + integration test pass rate | 100 % |
| Mutating operations reaching a target | 0 (asserted by adversarial tests) |
| Docs bilingual parity | 100 % (CI-enforced) |
| Every user-visible error carries a code | 100 % |

## 8. Goal amendment log

| Version | Date | Amendment | Rationale | Cascaded to |
|---|---|---|---|---|
| 1.3.0 | 2026-08-27 | **NG-6 lifted** and M4's P3-4 recorded as delivered, completing M4. The web UI was a non-goal for as long as the API it would sit on was still moving; with authentication, scopes, tenancy and a durable store in place, a console became a client of that API rather than a second implementation of it — which is why it adds no authorisation surface, no server-side data path and no module dependency | A deferred non-goal whose stated reason has expired; scope change logged rather than made silently | `specs/012-web-console/` |
| 1.2.2 | 2026-08-27 | M4's P3-3 recorded as delivered, closing the gap feature 009 named when it shipped: a credential that may diagnose no longer reaches every target. Tenancy turns itself on from the configuration rather than a flag, so a partitioned deployment cannot run unpartitioned, and a single-team one never encounters it | Backlog reflects delivered scope; no goal changed | `specs/011-tenant-registry/` |
| 1.2.1 | 2026-08-26 | M4's P3-2 recorded as delivered: run records can live in an S3-compatible bucket rather than one machine's disk, so replicas agree about history and a pod restart does not erase it. SigV4 is implemented from the specification and proven against its published vectors, keeping `go.mod` unchanged | Backlog reflects delivered scope; no goal changed | `specs/010-object-run-store/` |
| 1.2.0 | 2026-08-26 | M4's P3-1 recorded as delivered: the API authenticates, authorises per route by scope, and records who asked on the run it caused. The requirement attaches to the bind address rather than a flag, so a loopback developer workflow is unchanged and an exposed one cannot be unauthenticated by omission | Backlog reflects delivered scope; no goal changed | `specs/009-api-authentication/` |
| 1.1.9 | 2026-08-26 | M3's exit note reconciled: it said the milestone's backlog was still open, which stopped being true when P2-2 and P2-4 landed. Recorded because a status line that is stale is worse than one that is absent — it is read as current | Status only; no goal or backlog changed | `docs/en/project-goals.md` |
| 1.1.8 | 2026-08-26 | M3's P2-4 recorded as delivered, completing every ranked item under M3: a run is compared against a recorded baseline cell by cell, regressions and improvements are reported side by side and never netted, and a cell that legitimately cannot pass is recorded rather than deleted — which is what stops the absolute gate from making case deletion the cheapest way to a green build | Backlog reflects delivered scope; no goal changed | `specs/008-regression-baselines/` |
| 1.1.7 | 2026-08-26 | M3's P2-2 recorded as delivered, completing G2.3: a pack rule may be scoped to a version range, a renamed metric is two variants of one id, and dropping is transitive so a step never outlives the signal it reads. Only rules with a documented boundary are scoped in the shipped packs — a range invented from memory silently removes a working check | Backlog reflects delivered scope; no goal changed | `specs/007-version-scoped-rules/` |
| 1.1.6 | 2026-08-26 | M3's P2-1 recorded as delivered and the M3 exit criterion as met: 21 cases, at least three per pack, one healthy deployment that must conclude nothing, and each telemetry source withheld somewhere. The criterion covers the corpus only, so P2-2 and P2-4 remain open under M3 | Backlog and M3 exit status reflect delivered scope; no goal changed | `specs/006-eval-harness/` |
| 1.1.5 | 2026-08-26 | M3's P2-1 recorded as partly delivered: the evaluation harness, the `mas eval` gate and one case per pack exist, and the corpus is at 7 of the ≥20 cases M3 exits on. The harness's first run found a real defect — a source that was down was only reported as a gap if some agent happened to query it — recorded as amendment 1.0.6 to `specs/001-mvp-core/design-lld.md` | Backlog and M3 exit status reflect delivered scope; no goal changed | `specs/006-eval-harness/` |
| 1.1.4 | 2026-08-25 | M2's P1-5 and P1-6 recorded as delivered, completing M2: a role can be routed to a different provider, and a run reports what it cost per role — or states plainly that nobody priced it, rather than reporting zero | Backlog reflects delivered scope; no goal changed | `specs/005-model-routing-and-cost/` |
| 1.1.3 | 2026-08-25 | M2's P1-7 recorded as delivered, which completes M2's exit criteria: a knowledge pack's read-only commands now run inside a pod under the same allow-list that governs them on a host | Backlog reflects delivered scope; no goal changed | `specs/004-kube-exec/` |
| 1.1.2 | 2026-08-24 | M2's P1-4 recorded as delivered: five topologies now ship against one conformance contract, so the "switchable architecture" goal is testable rather than asserted | Backlog reflects delivered scope; no goal changed | `specs/003-switchable-topologies/` |
| 1.1.1 | 2026-08-24 | M2's P1-3 recorded as delivered (MongoDB, Pulsar, Milvus and OceanBase packs); M3's P2-3 pulled forward and delivered alongside it, because a conformance contract written after the packs would have been shaped by them; Kubernetes in-container `exec` given its own rank (P1-7) instead of living only in a change-log sentence | Backlog reflects delivered scope; no goal changed | `specs/002-middleware-packs/` |
| 1.1.0 | 2026-08-24 | M1 recorded as delivered; two items promoted into M1 during implementation (the local host adapter from P1-1, and source acquisition with local fallback from P1-2) because both proved self-contained and both are headline capabilities of the stated goal; Kubernetes in-container `exec` moved from M1 into M2 in their place | Delivered scope reconciled with planned scope, so the backlog reflects reality rather than intent | `specs/001-mvp-core/spec.md` §3 (already amended before implementation) |
| 1.0.0 | 2026-08-23 | Initial decomposition of the stated project goal into G1–G13, non-goals NG-1–NG-6, and the M1–M4 prioritised backlog | Baseline | `specs/001-mvp-core/*` |

## 9. Assumptions

| ID | Assumption | If wrong |
|---|---|---|
| ASM-G1 | Users already operate a Prometheus-compatible metrics stack and (optionally) Loki | An embedded collector becomes an M2 item |
| ASM-G2 | Read-only Kubernetes credentials can be provided to the tool | Only offline mode is usable |
| ASM-G3 | Go is an appropriate implementation language for a multi-agent system of this shape | Re-evaluated in `specs/001-mvp-core/plan.md` §5; Python is the fallback |
| ASM-G4 | "Middleware instance" is addressable by a stable target identifier the user configures | Target discovery becomes an M2 item |
