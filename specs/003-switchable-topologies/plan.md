# Implementation Plan: Switchable Multi-Agent Topologies

> **Feature ID**: `003-switchable-topologies` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

The registry, `agent.State` and the role runner already exist and were built for
this. The work is therefore almost entirely inside `internal/orchestrator`, plus
the three agent roles the new control flows genuinely need.

The order is deliberate and matches feature 002's: **the conformance contract is
written first, and is shown failing for a deliberately broken topology before any
real topology is written.** A contract written afterwards would encode whatever
the three topologies happened to do, which measures nothing. This is the single
most important sequencing decision in this feature, because the whole point of
switchable topologies is comparability, and comparability is exactly the property
that decays silently.

### 1.1 What a topology may and may not do

A topology is a *control flow over roles*. It may decide who acts, in what order,
how many times, and on what condition. It may not:

- reach a tool the run did not put in the registry it was handed;
- write to the report except through `agent.State`;
- change what a role is (a role's prompt and contract belong to `internal/agent`).

This line is what keeps a comparison meaningful: if topologies could differ in
capability, a difference in outcome would not be attributable to the architecture.

## 2. Design decisions

| ID | Decision | Rationale | Reversal condition |
|---|---|---|---|
| D-1 | Conformance contract first, proven against a deliberately broken topology | A contract derived from the implementations measures nothing | — |
| D-2 | No change to `agent.State` | Each topology's working memory (a plan, a set of positions) is its own local data; putting it in shared state would leak one topology's model into all of them | A second topology genuinely needs to *persist* the same structure into the report |
| D-3 | Four new roles: `Strategist`, `Executor`, `Advocate`, `Judge` | Each is a distinct contract, not a re-parameterisation of an existing one. `Planner` writes a prose plan once, for humans and for the investigators' context; adaptive re-planning is a different contract — structured, iterative and terminating — so it is a different role (`Strategist`) rather than an overloaded `Planner`. `Executor` works one stated objective; `Advocate` argues a position it did not choose; `Judge` decides between arguments | A fifth topology reuses them without adding its own |
| D-4 | Blackboard control is deterministic, not model-driven | The classic design asks a control component which knowledge source is eligible. Asking a model that question costs a call per round and makes the run non-reproducible; eligibility is a predicate over the blackboard and is better expressed in code | An eligibility judgement genuinely needs semantics no predicate can express |
| D-5 | Topology descriptions become bilingual in the registry | FR-010 requires the operator's language, and an English-only description in a bilingual product is a defect the parity check cannot see because it does not read Go | — |
| D-6 | Debate argues over hypotheses the correlator produced, not over positions invented by the advocates | Positions must be grounded in the same evidence, or the debate is theatre | — |
| D-7 | Advocates run concurrently, notes re-sorted deterministically | Same reasoning as the supervisor's investigators: independent I/O dominates, and CON-003 is enforced by test | — |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-001 | A new topology is more expensive without being better, and the tool implies otherwise by shipping it | Descriptions state the cost profile and when *not* to choose it; CON-002 forbids a scoring claim this feature cannot support |
| RSK-002 | `plan-execute` loops without converging | Hard round cap, plus the run's existing step budget; truncation is recorded, not hidden |
| RSK-003 | `debate` inflates confidence because an advocate argues persuasively | The judge adjudicates against evidence, and the conformance contract requires that a refuted position stays refuted |
| RSK-004 | `blackboard` never terminates, or terminates immediately | Eligibility must strictly decrease available work; a round that contributes nothing ends the loop, and the contract tests both ends |
| RSK-005 | Five topologies × the mock provider's scripted replies makes tests brittle | The mock is driven by role, not by call index, so a topology change does not renumber another topology's expectations |

## 4. Sequencing

| Phase | Content | Gate |
|---|---|---|
| A | Bilingual registry descriptions; conformance contract; broken-topology proof | Contract passes for `supervisor`/`single`, fails for the broken one |
| B | `Executor` role; `plan-execute` | Contract + FR-002 test |
| C | `Advocate` and `Judge` roles; `debate` | Contract + FR-003 test |
| D | `blackboard` | Contract + FR-004 test |
| E | CLI/API surface, run accounting, bilingual docs | `make ci`, `sddctl verify` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | Initial plan | HLD, LLD, tasks |
