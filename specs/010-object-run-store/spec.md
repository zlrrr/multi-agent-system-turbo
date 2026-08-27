# Feature Specification: A Shared, Durable Run Store on Object Storage

> **Feature ID**: `010-object-run-store` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`spec.zh.md`](./spec.zh.md) · **Upstream**: [`docs/en/project-goals.md`](../../docs/en/project-goals.md) v1.2.0
> **Constitution**: `.specify/memory/constitution.md` v1.0.0 · **Downstream**: `plan.md`

## 1. Problem statement

Run records are the audit trail. G1.4 says every run must be reproducible and
auditable after the fact, and `mas replay` delivers that — from a directory on
one machine's disk.

That is enough for a laptop and wrong for the way the service is actually
deployed. In Kubernetes the filesystem is usually the pod's, so a restart loses
the history; and a second replica of `mas serve` cannot see the first one's
runs, so `GET /api/v1/diagnoses` answers differently depending on which pod
takes the request. An audit trail that half the replicas cannot see is not an
audit trail — it is a cache with a promise attached.

The `RunStore` seam has been in place since M1, so nothing here changes what a
run *is*. What is missing is a backend that is durable and **shared**, in the
form the target environment already has: object storage.

## 2. Users & scenarios

| Persona | Goal | Trigger |
|---|---|---|
| Platform engineer | Run more than one replica and have them agree about history | Scaling past one pod |
| SRE | Replay a run from last month after the pod that produced it is long gone | An incident review |
| Auditor | Read the trail from storage with its own retention and access controls | A compliance question |
| Developer | Keep using a directory, with no bucket and no credentials | Every day |

## 3. Scope

### In scope
- An **S3-compatible** run store: AWS S3, MinIO, Ceph RGW, and anything else
  speaking the same subset.
- **SigV4** request signing, implemented against the published specification and
  verified against its published test vectors.
- Steps written as **immutable objects**, so the append-only property survives
  a backend that has no append.
- Reconstruction of an **interrupted** run from what was written before the
  process died.
- Listing newest-first, bounded, without downloading every record.
- A `mas doctor` probe that says whether the bucket is reachable and writable.
- Bilingual errors and documentation.

### Out of scope
- A relational or embedded database. Every option needs a dependency, and a
  hand-rolled one would be a database written by people who are not writing a
  database.
- Server-side encryption, object locking, lifecycle rules, versioning. All are
  bucket policy, which the operator configures where they configure the bucket.
- Migration of an existing filesystem store into a bucket. Copying a directory
  is a job for the tools that copy directories.
- Cross-replica coordination of *in-flight* runs. Two replicas do not share a
  running diagnosis; they share the record of finished ones.
- Credentials from instance metadata or `~/.aws`. Credentials are configuration,
  as they are everywhere else in this project.

## 4. Functional requirements

| ID | Requirement | Priority | Acceptance signal |
|---|---|---|---|
| FR-001 | `store.type: s3` MUST satisfy the whole `RunStore` interface | P0 | `TestS3StoreSatisfiesTheContract` |
| FR-002 | Requests MUST be signed with SigV4, matching the published test vectors | P0 | `TestSigV4MatchesPublishedVectors` |
| FR-003 | A step, once written, MUST never be rewritten | P0 | `TestStepsAreWrittenAsImmutableObjects` |
| FR-004 | A run interrupted before it finished MUST be reconstructable from what was written | P0 | `TestInterruptedRunIsReconstructed` |
| FR-005 | `List` MUST return newest-first, bounded, without reading every record | P0 | `TestListIsNewestFirstAndBounded` |
| FR-006 | A stored record MUST carry the same integrity digest the filesystem store uses | P0 | `TestDigestSurvivesTheRoundTrip` |
| FR-007 | A storage failure MUST surface as a coded error and MUST NOT be silently swallowed | P0 | `TestStorageFailuresAreCoded` |
| FR-008 | The report MUST still be returned when the store fails after the analysis | P0 | `TestRunSurvivesAStoreFailure` |
| FR-009 | Credentials MUST be secrets, never printed or logged | P0 | `TestObjectStoreCredentialsAreNeverEchoed` |
| FR-010 | Path-style and virtual-host addressing MUST both work | P1 | `TestBothAddressingStylesAreSupported` |
| FR-011 | `mas doctor` MUST report whether the bucket is reachable and writable | P1 | `TestDoctorProbesTheObjectStore` |
| FR-012 | Configuration MUST be validated at load, not at first write | P0 | `TestObjectStoreConfigIsValidatedAtLoad` |
| FR-013 | The filesystem and memory stores MUST keep working unchanged | P0 | `TestExistingStoresAreUnchanged` |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-001 | No new module dependency | `go.mod` unchanged |
| NFR-002 | Every operator-facing string bilingual | `sddctl verify` |
| NFR-003 | A finished run MUST be readable in one GET | Structural test |
| NFR-004 | No test may reach a real network | `httptest` only |

## 6. Constraints

| ID | Constraint | Source |
|---|---|---|
| CON-001 | Append-only within a run, whatever the backend | LLD §2.14, G1.4 |
| CON-002 | A lost record must be reported, never hidden | Constitution Art. IX |
| CON-003 | Credentials are secrets and are never rendered | Art. VIII |
| CON-004 | Both languages for every message | Art. III |
| CON-005 | Signing is implemented from the specification, and proven against its vectors | §3, FR-002 |

## 7. Acceptance

The feature is done when a bucket can hold run records, two processes pointed at
the same bucket see the same history, a run interrupted mid-flight can still be
read back, a storage failure is a coded error rather than a silent loss, no
credential reaches a log, and the filesystem store is untouched.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial specification | plan, HLD, LLD, tasks, code |
