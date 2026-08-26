# Low-Level Design (LLD): A Shared, Durable Run Store on Object Storage

> **Feature ID**: `010-object-run-store` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

```
internal/store/
  sigv4.go     new: AWS Signature Version 4, from the specification
  s3.go        new: the minimal S3 client — PUT, GET, LIST
  object.go    new: the RunStore over that client
  fs.go        Open gains the "s3" case; nothing else changes
internal/config/
  config.go    + StoreConfig.S3
  validate.go  + validation at load
internal/service/
  doctor.go    + the object-store probe
pkg/errs/
  registry.go  MAS-6010…MAS-6014
```

## 2. Configuration

```yaml
store:
  type: s3
  s3:
    endpoint: https://s3.eu-west-1.amazonaws.com   # or http://minio:9000
    region: eu-west-1
    bucket: mas-runs
    prefix: prod                                    # optional
    access_key_id: "${env:MAS_S3_KEY_ID}"
    secret_access_key: "${env:MAS_S3_SECRET}"
    path_style: true                                # MinIO and most self-hosted
    timeout: 30s
```

```go
type S3Config struct {
    Endpoint        string   `yaml:"endpoint"`
    Region          string   `yaml:"region"`
    Bucket          string   `yaml:"bucket"`
    Prefix          string   `yaml:"prefix"`
    AccessKeyID     Secret   `yaml:"access_key_id"`
    SecretAccessKey Secret   `yaml:"secret_access_key"`
    PathStyle       bool     `yaml:"path_style"`
    Timeout         Duration `yaml:"timeout"`
}
```

Both credentials are `Secret`, so they are unprintable in logs, `mas config`
and JSON, and they resolve `${env:}` and `${file:}` references (FR-009).

Validation at load (FR-012), reported as `MAS-6010`: `endpoint` must parse as an
absolute URL; `region` and `bucket` must be set; the two credentials must both
be set or both be empty — half a credential pair means an operator believes they
configured access and did not.

## 3. SigV4

```go
// Sign adds the Authorization and x-amz-* headers a request needs.
func Sign(req *http.Request, payloadSHA256, accessKeyID, secret, region, service string, now time.Time) error
```

The four steps of the specification, in order: the canonical request, the string
to sign, the signing key, and the signature. Everything is `crypto/sha256`,
`crypto/hmac` and `encoding/hex`.

The parts worth naming because they are where implementations differ:

- the URI path is escaped **per segment**, and `/` is not escaped;
- query parameters are sorted by name, and by value within a name, and both are
  escaped with the RFC 3986 rules — `+` is `%2B`, a space is `%20`;
- header names are lower-cased, values are trimmed and internal runs of spaces
  collapsed, and the signed-header list is sorted;
- the payload hash is the hex SHA-256 of the body, and it is also sent as
  `x-amz-content-sha256`.

`TestSigV4MatchesPublishedVectors` runs the specification's own test suite
cases: they exist precisely to catch the four bullets above, which is why the
test is worth more than the implementation.

## 4. The client

Three verbs, not four: an existence check was in the first draft, and `Get`
already is one, so it went rather than sitting unused.

Three verbs, not four: an existence check was in the first draft and `Get`
already is one, so it went rather than sitting unused.

```go
type s3Client struct {
    cfg  config.S3Config
    http *http.Client
}

func (c *s3Client) put(ctx context.Context, key string, body []byte) error
func (c *s3Client) get(ctx context.Context, key string) ([]byte, error)
func (c *s3Client) list(ctx context.Context, prefix, delimiter, after string, max int) (listResult, error)
```

`list` parses the ListObjectsV2 XML with `encoding/xml` — four fields, and the
`CommonPrefixes` that make a delimiter listing behave like directories.

Addressing (FR-010): path-style puts the bucket in the path
(`http://minio:9000/mas-runs/key`), virtual-host puts it in the host
(`https://mas-runs.s3.region.amazonaws.com/key`). Path-style is the default for
self-hosted deployments and is what most people actually run.

A non-2xx response becomes `MAS-6011` carrying the status and the S3 error code
from the body, because "AccessDenied" and "NoSuchBucket" call for different
actions and both are in the response.

## 5. The object layout

```
<prefix>/runs/<runID>/record.json
<prefix>/runs/<runID>/steps/0001.json
```

Step keys are zero-padded to four digits so lexicographic order is step order.
A run with more than 9999 steps is not a run this project can produce — the step
budget is three orders of magnitude below it — but the writer returns
`MAS-6014` rather than wrapping around, because a silent reordering of an audit
trail is worse than a refusal.

| Operation | What it does |
|---|---|
| `Create` | PUT `record.json` with status `running` |
| `Append` | PUT `steps/NNNN.json`, never touching `record.json` |
| `Finish` | PUT `record.json` with the report, usage, status and digest |
| `Fail` | PUT `record.json` with status `failed` and the code |
| `Get` | GET `record.json`; if status is `running`, LIST the steps and merge |
| `List` | LIST `runs/` with `delimiter=/`, newest-first, then GET each record |

`Append` keeps a per-run counter in memory. A second process appending to the
same run would collide — and cannot, because a run belongs to the process
executing it; replicas share finished records, not running ones (HLD §5).

## 6. Get, and the interrupted run

A finished run is one GET, which is NFR-003.

When `record.json` says `running`, the process that wrote it is gone or still
going, and the steps are the truth. They are listed and merged, and the returned
record **keeps `status: running`**: it is what was recorded, not a claim that
the run completed. `TestInterruptedRunIsReconstructed` asserts both the merge
and the status, because a reconstruction presented as a finished run would be
the harness inventing an ending (RSK-3).

## 7. List

Run ids are `run-<timestamp>-<random>`, so lexicographic descending is
newest-first — no index to maintain and none to fall out of step with reality
(D-6).

The listing pages until it has `limit` run prefixes and then stops, and only
then does it GET their records. A record that is not going to be returned is
never read (RSK-4).

## 8. Failure

Every non-2xx and every transport error becomes a coded error, logged with the
run id. The service already treats a store error as non-fatal after the analysis
is complete: the report is returned with the failure recorded beside it, because
losing the answer because we could not file it is the wrong trade mid-incident
(FR-008).

## 9. Errors

| Code | Meaning |
|---|---|
| `MAS-6010` | The object-store configuration is invalid |
| `MAS-6011` | The object store returned an error |
| `MAS-6012` | The object store could not be reached |
| `MAS-6013` | A stored record is malformed |
| `MAS-6014` | A run exceeded the step count the key layout can order |

## 10. Tests

| Test | What it pins |
|---|---|
| `TestSigV4MatchesPublishedVectors` | FR-002, CON-005 |
| `TestS3StoreSatisfiesTheContract` | FR-001 |
| `TestStepsAreWrittenAsImmutableObjects` | FR-003, CON-001 |
| `TestInterruptedRunIsReconstructed` | FR-004, RSK-3 |
| `TestListIsNewestFirstAndBounded` | FR-005, RSK-4 |
| `TestDigestSurvivesTheRoundTrip` | FR-006 |
| `TestStorageFailuresAreCoded` | FR-007, CON-002 |
| `TestRunSurvivesAStoreFailure` | FR-008 |
| `TestObjectStoreCredentialsAreNeverEchoed` | FR-009, CON-003 |
| `TestBothAddressingStylesAreSupported` | FR-010 |
| `TestDoctorProbesTheObjectStore` | FR-011 |
| `TestObjectStoreConfigIsValidatedAtLoad` | FR-012 |
| `TestExistingStoresAreUnchanged` | FR-013 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-26 | Initial low-level design | tasks, code |
