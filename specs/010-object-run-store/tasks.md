# Task Breakdown: A Shared, Durable Run Store on Object Storage

> **Feature ID**: `010-object-run-store` · **Version**: 1.0.0
> **Bilingual pair**: [`tasks.zh.md`](./tasks.zh.md) · **Upstream**: [`design-lld.md`](./design-lld.md) v1.0.0

## Legend
`status` ∈ `todo | doing | done | blocked`. Each task declares its test before
implementation (Constitution Art. VI.1) and is `done` only when that test passes.
Every test named here must exist: `sddctl verify` checks it.

## Phase A — signing

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T901 | SigV4 from the specification, stdlib only | FR-002, NFR-001 | `TestSigV4MatchesPublishedVectors` | — | done |
| T902 | Error codes `MAS-6010`…`MAS-6014`, bilingual, docs regenerated | CON-004 | `mas errcodes` output current | T901 | done |
| **G-A** | **Gate A** | | The published vectors pass | | done |

## Phase B — the client

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T910 | PUT, GET and LIST over `net/http`, against a signature-checking stub | FR-001, NFR-004 | `TestS3StoreSatisfiesTheContract` | G-A | done |
| T911 | Path-style and virtual-host addressing | FR-010 | `TestBothAddressingStylesAreSupported` | T910 | done |
| T912 | A non-2xx becomes a coded error carrying the S3 error code | FR-007, CON-002 | `TestStorageFailuresAreCoded` | T910 | done |
| **G-B** | **Gate B** | | `go test ./internal/store/...` | | done |

## Phase C — the store

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T920 | The object layout; Create, Append, Finish, Fail | FR-001, FR-003, CON-001 | `TestStepsAreWrittenAsImmutableObjects` | G-B | done |
| T921 | Get, including reconstruction of an interrupted run | FR-004, NFR-003 | `TestInterruptedRunIsReconstructed` | T920 | done |
| T922 | List: newest-first, bounded, reading no record it will not return | FR-005 | `TestListIsNewestFirstAndBounded` | T920 | done |
| T923 | The integrity digest survives the round trip | FR-006 | `TestDigestSurvivesTheRoundTrip` | T920 | done |
| **G-C** | **Gate C** | | `go test ./internal/store/...` | | done |

## Phase D — configuration, service and documentation

| ID | Task | Satisfies | Test / checkpoint | Deps | Status |
|---|---|---|---|---|---|
| T930 | `StoreConfig.S3`, validated at load; credentials are secrets | FR-012, FR-009, CON-003 | `TestObjectStoreConfigIsValidatedAtLoad`, `TestObjectStoreCredentialsAreNeverEchoed` | G-C | done |
| T931 | `store.Open` gains `s3`; the other stores are untouched | FR-013 | `TestExistingStoresAreUnchanged` | T930 | done |
| T932 | A run survives a store failure with the report intact | FR-008 | `TestRunSurvivesAStoreFailure` | T931 | done |
| T933 | `mas doctor` probes the bucket | FR-011 | `TestDoctorProbesTheObjectStore` | T931 | done |
| T934 | Bilingual documentation: configuration reference, manual, deployment example | NFR-002 | `sddctl verify` parity | T933 | done |
| **G-D** | **Gate D — feature exit** | | `make ci` green | | done |

## Checkpoint gates

| Gate | Tasks | Verification command |
|---|---|---|
| G-A | T901–T902 | `go test ./internal/store/...` |
| G-B | T910–T912 | `go test ./internal/store/...` |
| G-C | T920–T923 | `go test ./internal/store/...` |
| G-D | T930–T934 | `make ci` |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial task breakdown | code, config, docs |
