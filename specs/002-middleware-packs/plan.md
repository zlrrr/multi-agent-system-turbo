# Implementation Plan: Middleware Knowledge Breadth

> **Feature ID**: `002-middleware-packs` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Technical context

| Aspect | Decision |
|---|---|
| Language / runtime | None new: this feature adds YAML data and one test file |
| New dependencies | None |
| Where the work lands | `internal/knowledge/packs/*.yaml`, `internal/knowledge/conformance_test.go`, `docs/{en,zh}/knowledge-packs.md` |
| Test strategy | One table-driven conformance test over every shipped pack, plus guard and expression compilation checks |

The interesting constraint is what this feature *may not* do. `spec.md` CON-004
forbids changing the loader. If a pack cannot be expressed in the current schema,
that is a finding about the schema worth recording, not a licence to widen it
mid-feature — the whole claim that "adding middleware requires only a data file"
is on the line here, and this is the first real test of it.

## 2. Constitution check

| Article | Requirement | Compliance | Note |
|---|---|---|---|
| I | Spec approved before implementation | ☑ | `spec.md` v1.0.0 |
| II | Cascade tracked | ☑ | `traceability.yaml` |
| III | Bilingual parity | ☑ | Every pack string and the authoring guide |
| IV | Read-only enforced | ☑ | FR-006 drives every inspect command through the guard |
| V | Error codes | ☑ | Reuses the `MAS-5xxx` block; no new codes needed |
| VI | Test-first | ☑ | The conformance test is written before the packs |
| VII.1 | Abstractions justified | ☑ | No new abstraction; this is the existing seam being used |
| VII.3 | Deterministic-first | ☑ | Each pack's value is its playbooks, which run with no model |

## 3. Decomposition into phases

| Phase | Outcome | Exit criterion |
|---|---|---|
| A | Conformance test, written against the two existing packs first | It passes for Redis and Kafka, and fails informatively for a deliberately shallow pack |
| B | MongoDB pack | Conformance passes; playbooks produce findings against stubbed telemetry |
| C | Pulsar pack | As above |
| D | Milvus pack | As above |
| E | OceanBase pack | As above |
| F | Authoring guide, bilingual | `sddctl verify` parity passes |

Writing the test first is not ceremony here. A conformance test written after four
packs would be shaped by whatever those packs happened to contain, which is
precisely the failure mode this feature is trying to avoid.

## 4. Requirement → phase map

| Requirement | Phase |
|---|---|
| FR-005, FR-006, FR-007, FR-009 | A |
| FR-001 | B |
| FR-002 | C |
| FR-003 | D |
| FR-004 | E |
| FR-008 | F |
| FR-010 | Verified across all phases by the absence of loader changes |
| NFR-001 … NFR-004 | A (gates), verified per pack |

## 5. Risks

| ID | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| RSK-001 | Knowledge is plausible but wrong, and an operator acts on it | Medium | **High** | Every signal is a real exporter metric name recorded in the authoring guide; every failure mode explains the mechanism rather than restating the symptom; every recommendation that could lose data is labelled high risk |
| RSK-002 | Metric names drift between exporter versions | High | Medium | Playbooks treat a missing signal as a gap, not a false reading — the existing engine already does this. The authoring guide documents how to remap |
| RSK-003 | A pack needs schema support that does not exist | Low | Medium | Record it as a finding and ship the pack without that rule, rather than widening the schema mid-feature |
| RSK-004 | Four packs written quickly are shallower than the two written slowly | Medium | Medium | The conformance test sets a floor that applies to all six equally |

## 6. Complexity tracking

No deviations. This feature exists to demonstrate that the M1 architecture holds:
if it needs a workaround, that is the finding.

## 7. Definition of done

- [ ] Every phase's exit criterion met.
- [ ] `make ci` green.
- [ ] `git diff --stat` shows no change under `internal/knowledge/*.go` except the new test.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | Initial plan | `design-hld.md` |
