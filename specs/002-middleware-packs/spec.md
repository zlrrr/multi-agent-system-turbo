# Feature Specification: Middleware Knowledge Breadth

> **Feature ID**: `002-middleware-packs` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.1.0
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

MAS-Turbo ships expert knowledge for Redis and Kafka. The stated project goal
names four more middlewares — MongoDB, Pulsar, Milvus and OceanBase — and an
operator running any of them today gets a diagnosis with no domain grounding:
generic evidence, no failure modes, no vetted advice, and no deterministic
playbooks, so every routine incident falls through to a model call that has to
reason from first principles about a system it was told nothing about.

The architecture already anticipated this: knowledge is versioned data, and
`TestPackOnlyMiddlewareAddition` proves a middleware can be added from a file
alone. What is missing is the knowledge itself, and the guidance a third party
needs to write their own.

This feature is therefore *data and documentation*, not architecture. Its risk is
not that the code will not work — it is that the knowledge will be shallow,
plausible-sounding and wrong, which is worse than absent because an operator
would act on it.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| SRE running MongoDB | Diagnose a replica-set or write-concern problem with the same depth Redis gets | Alert fires on a MongoDB target |
| SRE running Pulsar | Diagnose backlog, ledger or bookie problems | Consumer backlog grows |
| SRE running Milvus | Diagnose query-node latency or a stuck compaction | Vector search latency rises |
| SRE running OceanBase | Diagnose tenant resource exhaustion or a slow merge | Tenant reports write stalls |
| Platform engineer | Author a pack for middleware we do not ship | Wants their own expertise encoded |

## 3. Scope

### In scope
- Knowledge packs for MongoDB, Pulsar, Milvus and OceanBase, each with signals,
  log patterns, failure modes with vetted advice, deterministic playbooks and
  read-only inspection commands.
- A pack conformance test that every shipped pack must satisfy, raising the floor
  for all six rather than only the new four.
- A bilingual pack-authoring guide with the published schema.
- Any guard allow-list entries the new inspection commands require.

### Out of scope
- Changes to the pack schema itself beyond what these four genuinely need.
- Kubernetes in-container `exec` (M2, separate feature).
- New topologies (M2, separate feature).
- Version-scoped rule variants within a pack (M3).

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | The system MUST ship a MongoDB pack covering at least replication, write concern, connection saturation, slow operations, lock contention and storage pressure | P0 | Conformance test names each failure mode |
| FR-002 | The system MUST ship a Pulsar pack covering at least backlog growth, bookie and ledger health, broker load and subscription problems | P0 | Conformance test names each failure mode |
| FR-003 | The system MUST ship a Milvus pack covering at least query-node latency, compaction, index build and memory pressure | P0 | Conformance test names each failure mode |
| FR-004 | The system MUST ship an OceanBase pack covering at least tenant resource limits, merge/compaction, and slow SQL | P0 | Conformance test names each failure mode |
| FR-005 | Every shipped pack MUST satisfy a single conformance test: minimum signal, failure-mode and playbook counts; bilingual completeness; every playbook step reachable; every concluded failure mode declared | P0 | One table-driven test covering all six packs |
| FR-006 | Every pack's `inspect` commands MUST be accepted by the safety guard, and no pack may declare a command the guard would refuse | P0 | Test drives every shipped inspect command through the guard |
| FR-007 | Every failure mode MUST carry at least one recommendation, and no shipped recommendation may be phrased as an action already taken | P0 | Conformance test asserts presence and scans for imperative-past phrasing |
| FR-008 | The system MUST publish a bilingual pack-authoring guide documenting the schema, the expression environment and the conformance rules | P0 | `docs/{en,zh}/knowledge-packs.md` exist and parity passes |
| FR-009 | Every playbook expression MUST compile against the sandboxed environment before release | P0 | Test compiles every expression in every shipped pack |
| FR-010 | Adding these packs MUST NOT require any change to `internal/knowledge` beyond validation the schema already performs | P0 | The diff touches no loader logic |

## 5. Non-functional requirements

| ID | Category | Requirement | Measurement |
|---|---|---|---|
| NFR-001 | Correctness | Every metric name used MUST come from a real, widely deployed exporter, not be invented | Each signal's source exporter recorded in the pack-authoring guide |
| NFR-002 | Honesty | A pack MUST NOT claim a failure mode it has no signal or log pattern to detect | Conformance test cross-checks indicators against declared signals |
| NFR-003 | Size | The embedded packs MUST NOT push the binary above 15 MB | Build size gate |
| NFR-004 | Determinism | Playbook selection for a given symptom MUST remain stable | Existing determinism tests continue to pass |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Bilingual completeness in every operator-facing string | Constitution Art. III |
| CON-002 | Inspection commands are re-validated by the guard at call time | Constitution Art. IV.2 |
| CON-003 | Advice is advisory; no recommendation may read as an action performed | Constitution Art. IV.3 |
| CON-004 | No loader change: the schema must already support these packs | 001-mvp-core FR-007 |

## 7. Assumptions

| ID | Assumption | If wrong |
|---|---|---|
| ASM-001 | Operators use the mainstream exporters: `percona/mongodb_exporter`, Pulsar's built-in Prometheus endpoint, Milvus's built-in metrics, OceanBase's `obagent` | The pack's signals miss; the authoring guide tells an operator how to remap them |
| ASM-002 | Metric names are stable enough within each project's supported versions | Version-scoped rules (M3) become necessary sooner |

## 8. Open questions

| ID | Question | Blocking? | Default taken |
|---|---|---|---|
| OQ-001 | How deep should each pack go before shipping? | No | Match the Redis pack's shape: ≥10 signals, ≥6 failure modes, ≥3 playbooks. Depth beyond that is iterative |
| OQ-002 | Should packs assert exporter version ranges? | No | Not yet; `versionRange` targets the middleware, and exporter drift is called out in the authoring guide |

## 9. Acceptance criteria

- [ ] FR-001 … FR-010 verified by automated tests.
- [ ] All six packs pass one shared conformance test.
- [ ] `make ci` green, including `sddctl verify`.
- [ ] Bilingual authoring guide published.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-24 | Initial specification, derived from goals v1.1.0 backlog item P1-3 | `plan.md`, `design-hld.md`, `design-lld.md`, `tasks.md` |
