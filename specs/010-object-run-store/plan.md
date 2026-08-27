# Implementation Plan: A Shared, Durable Run Store on Object Storage

> **Feature ID**: `010-object-run-store` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`plan.zh.md`](./plan.zh.md) · **Upstream**: [`spec.md`](./spec.md) v1.0.0 · **Downstream**: [`design-hld.md`](./design-hld.md)

## 1. Approach

Two decisions, and neither is about which library to use, because there is no
library to use: every feature in this project has held `go.mod` unchanged, and
an S3 SDK is the largest dependency tree it could take.

**Signing.** SigV4 is a specification with published test vectors, and the whole
of it is HMAC-SHA256, SHA-256 and careful canonicalisation — all stdlib. Getting
it wrong produces `403 SignatureDoesNotMatch`, not a security hole: we are the
client, and a signature we compute badly is one the server rejects. That makes
it a rare case where hand-rolling is the responsible option, and this project
has done the same thing once before, for RFC 6455, for the same reason. The test
vectors are what turn "we implemented a spec" into "we implemented it
correctly".

**The append-only property, on a backend that has no append.** The `RunStore`
contract says a step, once recorded, is never rewritten. The filesystem store
honours that by loading, appending and writing the whole record — which on
object storage would be a GET plus a PUT of the entire run for every step, and
a lost update whenever two writers overlap.

The object-native answer inverts it: **one immutable object per step**. Nothing
is ever rewritten because nothing is ever written twice. The record object is
written once at the start and once at the end, and the steps between it are
their own objects, in order, by key.

That buys the contract literally rather than by convention, and it buys
something the filesystem store does not have: a run interrupted between its
first and last write is still readable, because the steps are already there.
A crash used to lose everything after the last successful whole-record write;
now it loses only what had not been flushed at all.

The cost is a read that needs a LIST for an unfinished run. Finished runs — the
overwhelming majority of reads — are one GET, which is NFR-003.

## 2. Design decisions

| ID | Decision | Rationale |
|---|---|---|
| D-1 | SigV4 from the specification, stdlib only | An SDK is the largest dependency this project could take, for a signature it can compute in a hundred lines |
| D-2 | Proven against the published test vectors | "We implemented a spec" and "we implemented it correctly" are different claims, and only one is testable |
| D-3 | One immutable object per step | Honours append-only literally on a backend with no append, and removes the read-modify-write and its lost updates |
| D-4 | The record object is written at Create and at Finish, not per step | Per-step whole-record writes cost a GET and a PUT each; two writes bound the cost and mark the two states that matter |
| D-5 | Get prefers the record object and falls back to reconstruction | A finished run is one GET; an interrupted one is still readable, which the filesystem store cannot do |
| D-6 | List uses the key prefix and the run id's time ordering | Run ids are `run-<RFC3339-ish>-<rand>`, so lexicographic descending *is* newest-first — no index to maintain and none to fall out of step |
| D-7 | Credentials are `Secret`, from configuration only | Instance metadata and `~/.aws` are two more code paths and two more ways to be surprised about which identity is in use |
| D-8 | A storage failure is a coded error the run reports, and never a silent drop | CON-002. A record that quietly did not save is worse than one that failed loudly, because nobody looks for it until they need it |

## 3. Risks

| ID | Risk | Mitigation |
|---|---|---|
| RSK-1 | The signature is subtly wrong and works against one implementation but not another | The published vectors cover the canonicalisation cases that differ; a stub server verifies the signature rather than ignoring it |
| RSK-2 | Two replicas write the same run id | Run ids carry a random suffix, and every object a run writes is under its own prefix; a collision would need the same millisecond and the same random draw |
| RSK-3 | An interrupted run reconstructs into something misleading | The reconstructed record keeps `status: running` and is not presented as finished; a test asserts exactly that |
| RSK-4 | List becomes O(everything) as the bucket grows | The prefix listing is bounded by `limit` and stops early; it never reads a record it will not return |

## 4. Sequencing

1. SigV4, alone, against the published vectors.
2. A minimal S3 client: PUT, GET and LIST, over `net/http`.
3. The store: the object layout, Create/Append/Finish/Fail.
4. Get, including reconstruction; List, newest-first and bounded.
5. Configuration, validation, `store.Open`, and the doctor probe.
6. Bilingual documentation and the deployment example.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial plan | HLD, LLD, tasks |
