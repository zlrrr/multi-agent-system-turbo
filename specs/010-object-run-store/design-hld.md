# High-Level Design (HLD): A Shared, Durable Run Store on Object Storage

> **Feature ID**: `010-object-run-store` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-hld.zh.md`](./design-hld.zh.md) · **Upstream**: [`plan.md`](./plan.md) v1.0.0 · **Downstream**: [`design-lld.md`](./design-lld.md)

## 1. Where this sits

```
  service ──► store.RunStore ──┬─► Memory   in-process
                               ├─► FS       one machine's disk
                               └─► S3       a bucket, shared by every replica
                                     │
                                     ├─ sigv4.Sign   from the spec, proven by its vectors
                                     └─ net/http
```

Nothing above `RunStore` changes. The seam has been there since M1 and this is
the first backend to use it for what it was for — which is the point: a
persistence interface that only ever had one real implementation was a claim,
and this is the test of it.

## 2. The object layout, and why it is shaped like this

The contract says a step, once recorded, is never rewritten. The filesystem
store keeps that promise by rewriting the whole record every time a step
arrives — which is fine on a local disk and wrong on object storage twice over:
every step would cost a GET and a PUT of the entire run, and two writers would
silently lose each other's updates.

So the layout inverts it. Nothing is rewritten because nothing is written twice:

```
  <prefix>/runs/<runID>/record.json      written at Create, rewritten once at Finish
  <prefix>/runs/<runID>/steps/0001.json  written once, never again
  <prefix>/runs/<runID>/steps/0002.json
  …
```

The record object exists twice in a run's life, and both times it marks a state
that matters: *this run started*, and *this run ended, here is the report*.
Everything between them is its own immutable object.

That gives the append-only property literally rather than by convention, and it
gives something the filesystem store never had: a run interrupted between those
two writes is **still readable**. On disk, a crash lost everything since the
last whole-record write. Here the steps were already durable when they happened.

The cost is that reading an unfinished run needs a LIST plus a GET per step.
Finished runs — nearly every read — are one GET, which is the case worth
optimising.

## 3. Signing, and why it is written here

Every feature in this project has held `go.mod` unchanged, and an S3 SDK would
be the largest dependency it could take — for a signature that is HMAC-SHA256,
SHA-256 and careful canonicalisation, all of it stdlib.

The reason this is a responsible hand-roll rather than a reckless one is the
direction of the failure. We are the **client**. A signature we compute wrongly
is one the server rejects: the failure mode is `403 SignatureDoesNotMatch`, not
an unauthorised request that succeeds. And the specification ships test vectors,
so "we implemented it" and "we implemented it correctly" can be different
claims and only the second one gets asserted.

This project has made the same call once before, for RFC 6455, for the same
reason and with the same shape of evidence.

## 4. What happens when the bucket is not there

A store that fails must not fail quietly. A run record that silently did not
save is worse than one that failed loudly, because nobody discovers it is
missing until the moment they need it — which is during a review of something
that already went wrong.

So every storage failure is a coded error, logged with the run id, and surfaced.
But it does not destroy the analysis: if the bucket goes away *after* the
diagnosis is complete, the operator still gets their report, with the failure to
persist stated alongside it. Losing the answer because we could not file it away
would be the wrong trade in the middle of an incident.

## 5. What this deliberately does not do

- **No database.** Relational or embedded, every option is a dependency, and a
  hand-rolled one would be a database written by people who are not writing a
  database. Signing is a hundred lines with published vectors; storage engines
  are not.
- **No bucket policy.** Encryption, object locking, versioning, lifecycle and
  retention are configured where the bucket is configured, by the people who own
  it. Reimplementing any of it here would be a second, weaker copy.
- **No migration tool.** Moving a directory into a bucket is a job for the tools
  that move directories, and they already exist.
- **No shared in-flight state.** Two replicas do not co-operate on a running
  diagnosis. They share the record of finished ones, which is what an audit
  trail is.
- **No ambient credentials.** Instance metadata and `~/.aws` are two more paths
  and two more ways to be wrong about which identity is in use. Credentials are
  configuration here, as they are everywhere else in this project.

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial high-level design | LLD, tasks |
