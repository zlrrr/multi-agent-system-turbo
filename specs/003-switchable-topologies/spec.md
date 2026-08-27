# Feature Specification: Switchable Multi-Agent Topologies

> **Feature ID**: `003-switchable-topologies` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.1
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

The project goal asks for a switchable multi-agent architecture so that the
choice of architecture can be settled by experiment rather than by taste. The
registry seam for that exists and is exercised by two topologies — `supervisor`
(the default) and `single` (the control condition) — but two points do not make
a comparison, and one of them is deliberately degenerate.

The gap is not the seam; it is that the seam has nothing interesting plugged
into it. The three architectures the multi-agent literature actually argues
about — an adaptive plan/execute loop, an adversarial debate, and an
opportunistic blackboard — differ from `supervisor` in *control flow*, which is
precisely the variable an operator would want to test against their own
incidents.

There is a second, subtler gap. Nothing today forces a new topology to be
comparable with the existing ones. A topology that quietly skipped the critique
step, or that could not run offline, or whose output depended on goroutine
scheduling, would still register and still run — and every comparison involving
it would be meaningless without anyone noticing. Adding three topologies without
first writing down what makes them comparable would multiply that risk by three.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| SRE | Get a diagnosis whose control flow suits the incident: adaptive when the first check may settle it, adversarial when two causes look alike | Runs `mas diagnose --topology …` |
| Platform engineer | Choose a default topology for their organisation on evidence from their own incidents | Evaluating the tool |
| Researcher | Hold everything constant except the architecture and compare | Studying multi-agent design |
| Maintainer | Add a topology without silently breaking comparability | Contributing |

## 3. Scope

### In scope
- A topology conformance contract, written before the new topologies, that every
  registered topology must satisfy.
- `plan-execute`: an adaptive loop that plans, executes one objective at a time,
  and re-plans on what it learned.
- `debate`: competing positions argued from shared evidence and adjudicated.
- `blackboard`: opportunistic, data-driven control in which contributors act when
  the shared state makes them eligible.
- Whatever agent roles those topologies genuinely need, and no more.
- Per-run topology accounting an operator can compare: model calls, tool calls,
  tokens and wall-clock, already recorded, surfaced per topology.
- Bilingual documentation of when to choose each.

### Out of scope
- A case corpus and scored evaluation harness (M3, P2-1). This feature makes the
  comparison *possible and fair*; scoring it across a corpus is separate work.
- Per-agent model routing (M2, P1-5) — orthogonal, and mixing it in would
  confound the very comparison this feature exists to enable.
- Changing `supervisor` or `single`, except where the conformance contract
  exposes a genuine defect in them.
- Any new tool, collector or environment access. Topologies differ in control
  flow, not in capability.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | Every registered topology MUST satisfy one conformance contract, and a topology that ships without being listed in it MUST fail the build | P0 | `TestEveryRegisteredTopologyIsGoverned` |
| FR-002 | The system MUST provide a `plan-execute` topology that re-plans on what each executed objective returned | P0 | `TestPlanExecuteReplansOnFindings` |
| FR-003 | The system MUST provide a `debate` topology in which at least two positions are argued from the same evidence and adjudicated by a distinct role | P0 | `TestDebateProducesAdjudicatedPositions` |
| FR-004 | The system MUST provide a `blackboard` topology whose control is driven by the state of the shared workspace, not by a fixed script | P0 | `TestBlackboardSchedulesByEligibility` |
| FR-005 | Every topology MUST produce at least one hypothesis and a summary when given evidence that supports one | P0 | Conformance contract, per topology |
| FR-006 | Every topology MUST attribute every model exchange to the role that made it | P0 | Conformance contract, per topology |
| FR-007 | Every topology MUST respect the run's step budget and record truncation rather than exceeding it | P0 | `TestTopologiesRespectStepBudget` |
| FR-008 | Every topology MUST complete when a tool domain is unavailable, recording a gap rather than failing | P0 | `TestTopologiesDegradeWithoutTools` |
| FR-009 | Running the same topology twice over identical state MUST produce the same hypotheses and notes in the same order | P0 | `TestTopologiesAreDeterministic` |
| FR-010 | `mas topologies` MUST describe every topology, in the operator's language, including when to prefer it | P1 | CLI test asserts all names and both languages |
| FR-011 | A run record MUST identify the topology that produced it and its per-topology cost | P1 | `TestRunRecordCarriesTopologyAccounting` |
| FR-012 | Selecting an unknown topology MUST fail with `MAS-3001` before any model call | P1 | Existing admission test extended |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | No topology may access a tool the run did not give it | Structural audit test |
| NFR-002 | Adding a topology MUST need no change to `internal/agent` state or to the service | `git diff` at review; contract test |
| NFR-003 | The conformance contract MUST run against the deterministic mock provider, with no network | `go test ./internal/orchestrator/...` offline |
| NFR-004 | Every operator-facing string added by this feature MUST be bilingual | `sddctl verify` parity |
| NFR-005 | A topology MUST NOT be able to widen the safety guard or reach a tool outside the registry it was handed | Structural audit test |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Read-only always: a topology may only ever cause read-only tool calls | Constitution Art. IV |
| CON-002 | Topologies are compared, never scored, by this feature — a scoring claim without a corpus would be an unsupported claim | Art. IX, honest reporting |
| CON-003 | Concurrency inside a topology must not change its output | FR-009 |
| CON-004 | No new dependency | Art. VII.4 |

## 7. Acceptance

The feature is done when five topologies are registered, all five satisfy one
conformance contract, `mas topologies` describes each in both languages, a run
record identifies the topology and its cost, and `make ci` is green.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | Initial specification | plan, HLD, LLD, tasks, code |
