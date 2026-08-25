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
| `per_agent` | — | Per-role `model` and `temperature` overrides |

`per_agent` exists because roles have different needs. Investigators mostly
extract and summarise; correlation and critique are where judgement is required:

```yaml
llm:
  provider: anthropic
  model: claude-opus-5              # correlator, critic, reporter
  per_agent:
    investigator:
      model: claude-haiku-4-5-20251001
```

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
| `type` | `fs` | `fs` or `memory` |
| `dir` | `runs` | Directory, for `fs` |

Records carry a SHA-256 digest. A modified or truncated record is refused with
`MAS-6003` rather than replayed as genuine.

## `server`

| Key | Default | Meaning |
|---|---|---|
| `addr` | `:8080` | Listen address |
| `read_timeout` | `30s` | Request read timeout |
| `write_timeout` | `120s` | Response write timeout |

The API has no authentication yet. Do not expose it outside a trusted network.

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
