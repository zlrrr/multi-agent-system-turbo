# Project Constitution — multi-agent-system-turbo (MAS-Turbo)

> **Status**: Ratified · **Version**: 1.0.0 · **Ratified**: 2026-08-23 · **Last amended**: 2026-08-23
> **Bilingual pair**: [`constitution.zh.md`](./constitution.zh.md) — both files MUST be amended in the same commit.

The constitution is the highest-precedence document in this repository. Every specification,
plan, design document, task list, line of code, and released artifact is subordinate to it.
When any other document conflicts with this one, **this one wins** and the other document is
defective and must be fixed.

---

## Article I — Specification-Driven Development (SDD) is mandatory

**I.1 No code without a spec.** Every behavioural change traces to a numbered feature under
`specs/NNN-slug/`. The chain is fixed and each link is a reviewable artifact:

```
goal (docs/*/project-goals.md)
  └─> spec.md         WHAT & WHY  (no technology choices)
        └─> plan.md   HOW (stack, constraints, risk)
              └─> design-hld.md   system decomposition, interfaces, data flow
                    └─> design-lld.md   package/type/function level contracts
                          └─> tasks.md  executable, ordered, testable units
                                └─> code + tests
                                      └─> artifacts (image, binary, docs)
```

**I.2 Upstream first.** A change is made at the highest layer it belongs to, then *cascaded
downward*. Editing code to fix a behaviour that contradicts the spec is a constitution
violation; fix the spec, cascade, then fix the code.

**I.3 Traceability is machine-checked.** Every requirement carries a stable ID
(`FR-###`, `NFR-###`, `CON-###`). Every task cites the requirement IDs it satisfies. Every
exported package cites the design section that governs it. `make sdd-verify` fails the build
when a requirement has no task, or a task cites an unknown requirement.

## Article II — Cascading amendment

**II.1 Amendments are explicit.** Any change to an upstream artifact MUST be recorded in the
`## Change Log` table of that artifact with a version bump and an *Impact* column naming the
downstream artifacts that must be re-derived.

**II.2 Cascade is tracked, not assumed.** `specs/NNN-slug/traceability.yaml` records the
version of each artifact. When an upstream version increases, downstream entries become
`stale` and `make sdd-verify` fails until each is re-reviewed and re-stamped.

**II.3 Goals may be corrected.** The project goal document is itself amendable when reality
disagrees with it, under the same cascade discipline. Goal drift must be *recorded*, never
silent.

## Article III — Bilingual documentation parity

**III.1** Every document under `docs/`, `specs/`, and `.specify/` exists in English (`X.md`)
and Simplified Chinese (`X.zh.md`).

**III.2** Both files are updated **in the same commit**. A commit that touches one without the
other is rejected by `make sdd-verify`.

**III.3** The two files are semantically equivalent. Neither is a summary of the other.
Identifiers, requirement IDs, error codes, type names, and CLI flags are **not** translated.

## Article IV — Safety: read-only, always

**IV.1 The system performs no mutating operation on any target environment.** No write, no
delete, no restart, no scale, no config change, no `exec` that mutates state. This is not a
default — it is an invariant enforced in code by a guard that every tool call passes through.

**IV.2 Deny by default.** A command, HTTP verb, or API path that is not explicitly allow-listed
is refused. New capability = new allow-list entry = spec change.

**IV.3 Advice only.** The system's output is analysis plus *recommended* actions for a human.
It never presents itself as having acted.

**IV.4 Secrets never leave.** Credentials are redacted from logs, traces, reports, and from
every prompt sent to any LLM provider.

## Article V — Observability of the tool itself

**V.1 Structured logging.** All logs are structured (`slog`), carry `run_id`, and are
correlatable end-to-end.

**V.2 Every error carries a code.** Errors surfaced to a user or a log carry a stable
`MAS-NNNN` code from the central registry, with bilingual message and remediation. An
un-coded error reaching a boundary is a defect.

**V.3 Runs are replayable.** Every diagnostic run persists its evidence, its prompts, its tool
calls and its verdicts, so a result can be audited without re-running it.

## Article VI — Test-first checkpoints

**VI.1** Every task in `tasks.md` declares its acceptance test *before* implementation.

**VI.2** A task is `done` only when its tests pass in CI. Partial credit does not exist.

**VI.3** Determinism: LLM behaviour is tested through a scripted mock provider. No test
requires a network or a live model.

## Article VII — Architectural taste

**VII.1 Abstractions earn their place.** An interface exists because there are (or provably
will be) at least two implementations, or because it is the seam a test needs.

**VII.2 Pluggability where the domain varies, concreteness elsewhere.** LLM providers,
agent topologies, telemetry sources, environment adapters and knowledge packs vary — they are
registry-backed plugins. Nothing else is.

**VII.3 Deterministic-first.** If a question can be answered by a rule, a rule answers it.
LLM reasoning is for the parts that genuinely need judgement.

**VII.4 The core is dependency-light.** `internal/core` depends on the standard library and
this repository only.

## Article VIII — Delivery

**VIII.1** The deliverable is a container image plus a versioned binary, produced by GitHub
Actions from a tagged commit, accompanied by a bilingual user manual.

**VIII.2** `main` is always releasable: `make ci` (lint, vet, unit, integration, build) is green.

## Article IX — Autonomous ("lights-out") operation

**IX.1** Development proceeds without human intervention while the priority list has an
unblocked item.

**IX.2** Human intervention is requested only for: irreversible or outward-facing actions,
credentials, a genuine goal-level ambiguity where different readings produce materially
different systems, or an external blocker.

**IX.3** Any assumption made to keep moving is recorded in the artifact it affects under
`## Assumptions`, so a human can audit and reverse it later.

---

## Amendment procedure

1. Amend `constitution.md` **and** `constitution.zh.md` in one commit.
2. Bump the version: MAJOR = an article removed/redefined, MINOR = article added,
   PATCH = wording.
3. Record the amendment below.
4. Run `make sdd-verify`; resolve every artifact it marks `stale`.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-23 | Initial ratification | All artifacts derive from this |
