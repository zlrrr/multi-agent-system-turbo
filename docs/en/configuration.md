# Configuration Reference

> **Bilingual pair**: [`../zh/configuration.md`](../zh/configuration.md)
> A fully commented example: [`deploy/config/mas.example.yaml`](../../deploy/config/mas.example.yaml)

---

## Precedence

Four layers, each overriding the last:

```
built-in defaults  →  configuration file  →  MAS_* environment  →  command-line flags
```

`mas config` prints the effective result with every secret masked. When something
is not what you expect, start there.

### Locating the file

In order: `--config <path>`, `$MAS_CONFIG`, `./mas.yaml`, `./mas.yml`,
`/etc/mas/mas.yaml`. Absence is not an error — a zero-configuration run uses
defaults — but a path you *name* and that does not exist is (`MAS-1004`).

Unknown fields are rejected (`MAS-1002`) rather than ignored, because a silently
ignored typo in a safety-relevant setting is worse than a failed start.

## Secrets

Never write a credential into the file. Two indirections are available:

| Form | Resolved from |
|---|---|
| `${env:NAME}` | The environment, at the moment of use |
| `${file:/path}` | A file, at the moment of use, trailing newline trimmed |

A `Secret` cannot be printed: it renders as `***` through `fmt`, JSON and YAML
alike, so it cannot leak through a log line, an API response or a bug report.
An unresolvable reference is `MAS-1006`.

## `log`

| Key | Default | Meaning |
|---|---|---|
| `level` | `info` | `debug`, `info`, `warn`, `error`. `debug` logs prompts and tool arguments — after redaction |
| `format` | `json` | `json` or `text` |
| `redact` | — | Extra regexes scrubbed from logs, reports, run records and model prompts |

Redaction happens at the log handler rather than at each call site, so a new log
statement cannot leak by omission. Common credential shapes — bearer tokens,
`api_key=` assignments, `user:pass@host` URLs, JWTs, PEM headers — are covered by
default; `redact` is for values specific to you.

## `llm`

| Key | Default | Meaning |
|---|---|---|
| `provider` | `mock` | `mock`, `anthropic`, or `openai` |
| `model` | `mock-1` | Model name for the provider |
| `api_key` | — | Use `${env:...}` |
| `base_url` | — | For OpenAI-compatible servers: DeepSeek, Qwen, vLLM, Ollama |
| `timeout` | `60s` | Per-request timeout |
| `max_tokens` | `4096` | Cap per completion |
| `temperature` | `0` | Sampling temperature |
| `mock_script` | — | Path to a scripted transcript (mock provider only) |
| `per_agent` | — | Per-role `provider`, `model` and `temperature` overrides |
| `providers` | — | Named alternative providers a role may be routed to |
| `pricing` | — | Per-model prices, used to compute a run's cost |

### Routing roles to different models

`per_agent` exists because roles have different needs. Investigators mostly
extract and summarise; correlation and critique are where judgement is required.
A role may override the model, the temperature, or the **provider** entirely:

```yaml
llm:
  provider: anthropic
  model: claude-opus-5              # correlator, critic, reporter
  api_key: ${env:ANTHROPIC_API_KEY}

  providers:                        # named alternatives
    local:
      provider: openai
      base_url: http://127.0.0.1:11434/v1
      model: qwen2.5:14b

  per_agent:
    investigator: { provider: local }     # cheap extraction
    executor:     { provider: local }
    correlator:   { temperature: 0.1 }    # default provider, cooler
```

A named provider inherits every field of the default it does not set, and a role
inherits everything it does not override. That matters more than it looks: a role
that changes only the temperature must not lose the endpoint and the key, and
restating settings per role is how a production run fails on the one field
somebody forgot.

Every provider a run routes to is opened when the run is admitted, so a bad
credential refuses the run rather than becoming a gap discovered three minutes
in. Run `mas models` to see the routing that will actually be used.

### Pricing

**This project ships no price list.** Prices change, differ by contract and
region, and a stale number that looks authoritative is a false claim. So prices
are yours to supply:

```yaml
llm:
  pricing:
    claude-opus-5:  { input_per_mtok: 5.00, output_per_mtok: 25.00 }
    qwen2.5:14b:    { input_per_mtok: 0,    output_per_mtok: 0 }   # self-hosted
```

A model with **no entry** makes the run's cost *unknown*. It is never reported as
zero — a report claiming a run cost nothing is worse than one that says nothing,
because you would believe it. A configured price of exactly `0` is different and
stays known: a self-hosted model really is free at the margin, and writing `0`
says so deliberately.

A run that priced some models and not others reports the priced part **and**
names what it could not price, so the figure is never mistaken for the total.
`mas doctor` and `mas models` both report which models are priced.

The `mock` provider replays a scripted transcript. It is what makes the test
suite deterministic and the demo credential-free; `mas doctor` warns when it is
configured, because it is not a real analysis.

## `telemetry`

### `telemetry.metrics[]`

| Key | Default | Meaning |
|---|---|---|
| `name` | `metrics-N` | Referenced by `targets[].metrics_source` |
| `type` | `prometheus` | `prometheus`, `victoriametrics`, `thanos`, `mimir` — one wire API |
| `url` | required | Base URL, without a trailing `/api/v1` |
| `auth.type` | `none` | `none`, `bearer`, `basic`, `header` |
| `auth.token` | — | For `bearer` and `header` |
| `auth.username` / `auth.password` | — | For `basic` |
| `auth.header` | — | Header name, for `header` |
| `timeout` | `15s` | Per-query timeout |
| `max_samples` | `11000` | Truncation ceiling; a range query's step is widened to respect it |
| `headers` | — | Extra request headers |

### `telemetry.logs[]`

| Key | Default | Meaning |
|---|---|---|
| `name` | `logs-N` | Referenced by `targets[].logs_source` |
| `type` | `loki` | Only `loki` today |
| `url` | required | Base URL |
| `auth` | — | As for metrics |
| `tenant_id` | — | Sets `X-Scope-OrgID` for multi-tenant Loki |
| `timeout` | `20s` | Per-query timeout |
| `max_lines` | `1000` | Result ceiling, enforced locally as well as sent |

## `envs`

| Key | Meaning |
|---|---|
| `type` | `kubernetes` or `local` |
| `kubeconfig` | Path; empty means the in-cluster service account |
| `context` | Named kubeconfig context; empty means `current-context` |
| `namespace` | Default namespace |
| `api_server` | Explicit API server URL, instead of a kubeconfig |
| `token` | Bearer token, with `api_server` |
| `ca_file` | CA bundle path |
| `tls_insecure_skip_verify` | Disable certificate verification. Do not use in production |
| `timeout` | Per-request timeout |
| `exec` | `false` disables in-container inspection for this environment. Narrowing only: absent or `true` means the guard's read-only allow-list decides each command, exactly as it does on a host |

Supported credential sources: in-cluster service account, kubeconfig bearer
token, `tokenFile`, client certificate, and basic auth.

**`exec` credential plugins are not supported.** Honouring them means running an
arbitrary binary named by a configuration file, which is exactly what the
deny-by-default command allow-list exists to prevent. MAS-Turbo refuses such a
kubeconfig with `MAS-4202` and tells you to supply a read-only service-account
token instead.

## `targets[]`

| Key | Meaning |
|---|---|
| `id` | Unique identifier; what `--target` refers to |
| `kind` | Middleware kind, matched against knowledge packs: `redis`, `kafka`, … |
| `version` | Pins pack selection; omit to detect from the running image tag |
| `env` | Environment name from `envs` |
| `namespace` | Overrides the environment's namespace |
| `selector` | Kubernetes label selector |
| `labels` | Becomes the PromQL selector, e.g. `{job="redis"}` |
| `metrics_source` / `logs_source` | Named sources; omitted means the first configured |
| `hosts` / `port` | For `local` environments |

`labels` and `selector` do different jobs: `labels` selects **series** in your
metrics backend, `selector` selects **pods** in Kubernetes. They usually differ.

## `knowledge`

| Key | Default | Meaning |
|---|---|---|
| `pack_dirs` | — | Directories searched for knowledge packs |

A pack whose id matches a shipped one replaces it, so you can correct the
built-in knowledge without forking. An invalid pack is reported by `mas doctor`
and skipped; it never prevents the others from loading.

## `source`

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `true` | Whether to acquire middleware source at all |
| `cache_dir` | `$TMPDIR/mas-src` | Where fetched trees are kept |
| `network_timeout` | `10s` | How long a network attempt may take before falling back |
| `cache_ttl` | `24h` | How long a cached tree stays fresh |
| `repos` | — | Middleware kind → network repository URL |
| `mirrors` | — | Middleware kind → local mirror path |

The fallback chain is: fresh cache → network repository → local mirror. When the
network is unreachable and a mirror is configured, the run continues from the
mirror and records `MAS-4401`, so the report states that the code consulted may
not match the deployed version. In an air-gapped environment, configure only
`mirrors` and no network attempt is made at all.

## `run`

| Key | Default | Meaning |
|---|---|---|
| `default_topology` | `supervisor` | Used when `--topology` is not given |
| `default_mode` | `offline` | `offline` or `online` |
| `default_window` | `1h` | Used when neither `--since` nor `--from/--to` is given |
| `deterministic_short_circuit` | `0.85` | Confidence at which the agent phase is skipped entirely |
| `language` | `en` | `en` or `zh` |
| `max_concurrency` | `4` | Concurrent investigators |
| `budget.max_steps` | `24` | Reasoning steps per run |
| `budget.max_tool_calls` | `40` | Tool invocations per run |
| `budget.max_tokens` | `120000` | Tokens per run |
| `budget.max_wall` | `5m` | Wall-clock ceiling |

`deterministic_short_circuit` is the setting that decides what a routine incident
costs you. At `0.85`, a rule that establishes memory pressure with 0.9 confidence
ends the run there: no model call, sub-second. Set it to `0` to always run the
agents.

Exceeding a budget never fails a run. It truncates, records `MAS-3005`, and the
report says so — a partial analysis with its limits marked beats no analysis.

## `store`

| Key | Default | Meaning |
|---|---|---|
| `type` | `fs` | `fs`, `memory` or `s3` |
| `dir` | `runs` | Directory, for `fs` |
| `s3.*` | *(none)* | The bucket, for `s3` |

Records carry a SHA-256 digest wherever they are stored. A modified or truncated
record is refused with `MAS-6003` rather than replayed as genuine.

### `store.s3` — a store every replica can see

`fs` keeps runs on one machine's disk. In Kubernetes that is usually the pod's,
so a restart loses the history and a second replica cannot see the first one's
runs — `GET /api/v1/diagnoses` then answers differently depending on which pod
takes the request.

```yaml
store:
  type: s3
  s3:
    endpoint: http://minio:9000        # or https://s3.eu-west-1.amazonaws.com
    region: us-east-1
    bucket: mas-runs
    prefix: prod                       # optional, so one bucket can hold several
    access_key_id: "${env:MAS_S3_KEY_ID}"
    secret_access_key: "${env:MAS_S3_SECRET}"
    path_style: true                   # MinIO, Ceph RGW and most self-hosted
    timeout: 30s
```

| Key | Meaning |
|---|---|
| `endpoint` | The service URL. Required |
| `region` | Signing region. Required — MinIO accepts anything, AWS does not |
| `bucket` | Required |
| `prefix` | Key prefix, so one bucket can serve several deployments |
| `access_key_id` / `secret_access_key` | Secrets. Both, or neither for an anonymous bucket |
| `path_style` | Bucket in the path rather than the hostname. `true` for most self-hosted |
| `timeout` | Per-request timeout, default `30s` |

Credentials come from configuration only — not from instance metadata and not
from `~/.aws`, because two more sources are two more ways to be surprised about
which identity is in use.

**What is stored where.** Each run is a prefix of its own:

```
<prefix>/runs/<runID>/record.json      written at Create, once more at Finish
<prefix>/runs/<runID>/steps/0001.json  written once, never again
```

Nothing is rewritten, so the append-only guarantee holds on a backend that has
no append — and a run interrupted between those two writes is still readable,
because its steps were durable when they happened. A reconstructed run keeps
`status: running`: it is what was recorded, not a claim that it completed.

Bucket policy is bucket policy. Encryption, versioning, object locking,
lifecycle and retention are configured where the bucket is, by whoever owns it.

`mas doctor` reports which store is in use and, for `s3`, whether the bucket
answers. If the store fails *after* an analysis is complete, you still get the
report, with a note saying it was not persisted — losing the answer because we
could not file it away would be the wrong trade mid-incident.

## `server`

| Key | Default | Meaning |
|---|---|---|
| `addr` | `:8080` | Listen address |
| `read_timeout` | `30s` | Request read timeout |
| `write_timeout` | `120s` | Response write timeout |
| `auth.tokens[]` | *(none)* | Bearer credentials the API accepts |
| `tls.cert_file` / `tls.key_file` | *(none)* | Serve TLS from this pair |
| `tls.terminated_by_proxy` | `false` | Something in front of this process terminates TLS |
| `ui.enabled` | `true` | Serve the read-only web console at `/ui/` |

### The rule that attaches to the address

The requirement is not a flag — it follows from what the socket can reach:

| Bind address | Authentication | TLS | Result |
|---|---|---|---|
| loopback (`127.0.0.1`, `[::1]`, `localhost`) | not required | not required | starts |
| anything else | **required** | — | refuses to start (`MAS-7010`) |
| anything else | configured | absent, no proxy declared | refuses to start (`MAS-7011`) |
| anything else | configured | served, or proxy declared | starts |

A laptop needs no configuration, and an exposed deployment cannot be
unauthenticated by someone forgetting a setting. `mas doctor` reports the
exposure as a warning; `mas serve` refuses.

`terminated_by_proxy` records a fact this process cannot verify — that an
ingress or sidecar terminates TLS in front of it. It has to be typed, because
typing it is the acknowledgement.

### `server.auth.tokens[]`

| Key | Meaning |
|---|---|
| `name` | The principal. It is what an audit line names and what a run record records as the caller |
| `token` | The credential, as a secret: a literal, `${env:VAR}` or `${file:/path}` |
| `scopes` | `read`, `diagnose`, or both |

```yaml
server:
  addr: "0.0.0.0:8080"
  auth:
    tokens:
      - name: dashboard
        token: "${env:MAS_DASHBOARD_TOKEN}"
        scopes: [read]
      - name: oncall
        token: "${file:/etc/mas/oncall.token}"
        scopes: [read, diagnose]
  tls:
    terminated_by_proxy: true
```

`read` covers everything already computed: stored diagnoses, targets,
topologies, packs and `/metrics`. `diagnose` covers `POST /api/v1/diagnoses`,
which spends model tokens and reads production telemetry — a status page needs
the first and must not have the second.

A token with no scopes, or a scope this build does not recognise, fails at load
(`MAS-7013`). An ignored scope is an authorisation you believe you granted.

`/healthz` and `/readyz` never require a credential: a liveness probe that needs
one is a liveness probe that fails during a credential problem.

### Tenants — serving several teams from one deployment

By default a credential that may diagnose may diagnose **any** configured
target. That is fine for one team looking after its own estate, and wrong when
the estate is not one team's.

Naming a `tenant` on a target says which slice of the estate it belongs to:

```yaml
targets:
  - id: payments-redis
    kind: redis
    tenant: payments
  - id: search-kafka
    kind: kafka
    tenant: search

server:
  auth:
    tokens:
      - name: payments-oncall
        token: "${env:PAYMENTS_TOKEN}"
        scopes: [read, diagnose]
        tenants: [payments]
      - name: platform
        token: "${env:PLATFORM_TOKEN}"
        scopes: [read]
        tenants: [payments, search]
```

**There is no flag.** A configuration is multi-tenant the moment any target
names a tenant, and the rest follows at load:

| Configuration | Result |
|---|---|
| No target names a tenant | Tenancy off; nothing to configure and nothing changes |
| Some targets name one, some do not | Refused (`MAS-1013`). A target belonging to nobody is one everyone or no one can reach |
| A credential names no tenants | Refused. In a partitioned deployment that is a superuser nobody declared |
| A credential names a tenant no target declares | Refused |
| A credential names tenants but nothing is tenanted | Refused — the restriction would be silently ignored, which is the exact shape of a control that looks applied and is not |

A flag would let a partitioned deployment run unpartitioned, which is the one
failure this arrangement makes impossible.

**What enforcement looks like.** Starting a diagnosis on someone else's target,
and reading someone else's run, both come back **`404`** — identical to an id
that was never configured. A `403` naming the target would confirm it exists,
which is the neighbour's information rather than the caller's, and it leaks once
per guessed id. Target and run listings are filtered to what the caller may see.

Each run records the tenant it was for, beside who asked. It is written when the
run happens rather than derived later: reading the target's tenant at query time
answers *which tenant owns it now*, and audits ask about the past.

`mas doctor` reports whether tenancy is on and what each credential reaches, by
name and never by credential — a filtered listing looks exactly like an empty
estate, so the answer is one command away.

Deliberately not here: per-tenant models, budgets, packs or telemetry sources;
tenant-scoped storage; hierarchies, groups or delegation; and quotas. Tenancy
also does not apply to the CLI, which runs as the operator with the operator's
own file — every target in it is already theirs.

### `server.ui` — the web console

`mas serve` serves a read-only web console at `/ui/`. It renders diagnoses:
summary, hypotheses with their confidences, findings, evidence, gaps and
advisory recommendations, plus the step trace, the target list and the loaded
knowledge packs.

```yaml
server:
  ui:
    enabled: false   # a hardened deployment that wants no console
```

It is on by default, because a console you must discover a configuration key to
enable is a console nobody uses. Turning it off makes `/ui/` answer `MAS-7016`
with the key that turns it back on, rather than a bare `404` — that a console is
switched off is not a fact worth withholding. The API is unaffected either way.

**What it can do is exactly what the credential can do.** The console is a
client of this API, not a second implementation of it: it holds no data path of
its own, so scope and tenancy are enforced in one place rather than two. Every
byte it displays came back from `/api/v1/…` under your token.

**It is read-only twice over.** The system never writes to a target
environment, and the console additionally cannot start a diagnosis: that spends
model tokens and reads production telemetry, and the `diagnose` scope is not one
a browser tab should hold. A `read` credential is what the console needs.

**Using it.** Open `/ui/`, paste a token that holds `read`. The token is kept in
the browser tab's `sessionStorage` and sent as `Authorization: Bearer` — never
placed in a URL, never in a cookie. Closing the tab ends it. On a plaintext
origin that is not loopback the console says so before accepting anything;
`mas serve` already refuses that combination unless TLS is served or a
terminating proxy is declared.

The console follows `run.language`, and a reader can switch language for their
own tab.

## `safety`

| Key | Default | Meaning |
|---|---|---|
| `extra_denied_binaries` | — | Remove binaries from the allow-list |
| `extra_denied_args` | — | Extra argument patterns to refuse |
| `max_response_bytes` | `8388608` | Response size ceiling |
| `max_timeout` | `120s` | Per-call timeout ceiling |

**Every setting here narrows the guard. None widens it.** There is no key that
adds a binary, adds a read path, or disables the guard, and a test asserts that
no such key exists. Extending what MAS-Turbo may do is a change to the software
and its specification, not to your configuration.

`mas tools` prints the effective allow-lists.

## Environment variables

| Variable | Overrides |
|---|---|
| `MAS_CONFIG` | Configuration file path |
| `MAS_LOG_LEVEL`, `MAS_LOG_FORMAT` | `log.*` |
| `MAS_LLM_PROVIDER`, `MAS_LLM_MODEL`, `MAS_LLM_API_KEY`, `MAS_LLM_BASE_URL`, `MAS_LLM_MOCK_SCRIPT` | `llm.*` |
| `MAS_METRICS_URL`, `MAS_LOGS_URL` | The first telemetry source's URL |
| `MAS_STORE_TYPE`, `MAS_STORE_DIR` | `store.*` |
| `MAS_SERVER_ADDR` | `server.addr` |
| `MAS_RUN_TOPOLOGY`, `MAS_RUN_MODE`, `MAS_RUN_LANGUAGE` | `run.*` |
| `MAS_RUN_MAX_STEPS`, `MAS_RUN_MAX_WALL` | `run.budget.*` |
| `MAS_SOURCE_CACHE_DIR` | `source.cache_dir` |
| `MAS_KNOWLEDGE_PACK_DIRS` | `knowledge.pack_dirs` (path-list separated) |

An unrecognised `MAS_*` variable is ignored rather than fatal, so an unrelated
variable in a shared environment cannot stop the tool starting.

## Validation

`Validate` reports the **first** problem with its path and, in the same message,
how many others were found:

```
MAS-1003  configuration is invalid at targets[1].env: target "kafka-prod" references
          unknown environment "staging" (and 2 more: …)
```

`mas doctor` goes further: it probes every endpoint and reports all of them.
Run it after any configuration change.
