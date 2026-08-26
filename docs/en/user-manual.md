# MAS-Turbo User Manual

> **Bilingual pair**: [`../zh/user-manual.md`](../zh/user-manual.md)
> Applies to MAS-Turbo 0.1.x · See also: [configuration](./configuration.md) · [evaluation](./evaluation.md) · [error codes](./error-codes.md)

---

## 1. What this tool does, and what it will not do

MAS-Turbo diagnoses runtime problems in open-source middleware — Redis, Kafka,
MongoDB, Pulsar, Milvus, OceanBase and others — by correlating four kinds of
evidence: metrics from a Prometheus-compatible backend, logs from Loki, live
state from Kubernetes or a host, and the middleware's own source code.

It returns ranked hypotheses, the evidence for and against each, the checks that
passed, the evidence it could not obtain, and recommended next steps.

**It performs no action against the systems it inspects.** Not a restart, not a
configuration change, not a `FLUSHALL`. This is not a default you can change: a
safety guard sits between every capability and the outside world, refuses
anything not on a read-only allow-list, and has no setting that disables it. The
guard's adversarial test suite tries to get mutating commands past it — in every
casing, through argument injection, through knowledge-pack content — and asserts
that none arrive.

That restraint is the point. A tool you can safely aim at production during an
incident is worth more than one that could fix things but that nobody dares run.

## 2. Installation

### Container image (recommended)

```bash
docker pull ghcr.io/zlrrr/multi-agent-system-turbo:latest
docker run --rm ghcr.io/zlrrr/multi-agent-system-turbo:latest version
```

The image runs as an unprivileged user (uid 65532) and needs no capabilities.

### Binary

Download the archive for your platform from the releases page, verify it, and
install:

```bash
sha256sum -c SHA256SUMS --ignore-missing
tar -xzf mas-linux-amd64.tar.gz
sudo install -m 0755 mas /usr/local/bin/mas
mas version
```

### From source

```bash
git clone https://github.com/zlrrr/multi-agent-system-turbo
cd multi-agent-system-turbo
make build          # produces bin/mas
make demo           # a complete diagnosis with no credentials required
```

## 3. Five minutes to your first report

`make demo` starts local stub telemetry and runs three diagnoses, so you can see
the output shape before configuring anything real:

```
==> 1/3 deterministic only: a confident rule short-circuits the agent phase
==> 2/3 full multi-agent investigation (supervisor topology, mock provider)
==> 3/3 the same investigation, reported in Chinese
```

Read `.demo/report.en.md`. Then configure your own environment.

## 4. Configuration

Create `mas.yaml` (see [`deploy/config/mas.example.yaml`](../../deploy/config/mas.example.yaml)
for a fully commented reference):

```yaml
version: "1"

llm:
  provider: anthropic
  model: claude-opus-5
  api_key: "${env:ANTHROPIC_API_KEY}"

telemetry:
  metrics:
    - name: primary
      type: prometheus
      url: http://prometheus.monitoring.svc:9090
  logs:
    - name: primary
      type: loki
      url: http://loki.monitoring.svc:3100

envs:
  prod-k8s:
    type: kubernetes
    namespace: middleware       # empty kubeconfig ⇒ in-cluster service account

targets:
  - id: redis-prod
    kind: redis
    env: prod-k8s
    selector: "app=redis,role=master"
    labels:
      job: redis                # becomes the PromQL selector {job="redis"}
```

Configuration is layered: **defaults → file → `MAS_*` environment → flags**, each
overriding the last.

Secrets are never written in the file. Use `${env:NAME}` or `${file:/path}`;
they are resolved at the moment a request is made and cannot be printed —
`mas config` prints the effective configuration with every secret masked.

Then check your work:

```bash
mas doctor
```

`doctor` validates the configuration and probes **every** configured endpoint,
reporting all of them rather than stopping at the first problem, because when
you are setting a tool up you want the whole list.

```
STATUS  CHECK             DETAIL
ok      configuration     valid; 2 target(s), 1 environment(s)
ok      knowledge packs   2 pack(s) covering [kafka redis]
ok      safety guard      read-only enforced; 14 allow-listed command(s), 19 allow-listed read path(s)
ok      metrics: primary  http://prometheus.monitoring.svc:9090 reachable
FAIL    logs: primary     MAS-4101  log source "primary" is unreachable: dial tcp: i/o timeout
ok      llm provider      anthropic configured with model claude-opus-5
ok      run store         fs store is writable and readable
```

## 5. Running a diagnosis

```bash
mas diagnose --target redis-prod --symptom "p99 latency spike" --since 1h
```

Describe the symptom in your own words. The wording is used to select which
diagnostic playbooks run, so "consumer lag growing" and "OOM errors on write"
lead to genuinely different investigations. Both English and Chinese phrasings
are recognised.

| Flag | Meaning |
|---|---|
| `--target`, `-t` | Target id from your configuration (required) |
| `--symptom`, `-s` | What you observed (required) |
| `--since` | Look back this far: `30m`, `1h`, `24h` |
| `--from` / `--to` | An explicit RFC3339 window instead of `--since` |
| `--mode` | `offline` (telemetry only) or `online` (also read the live environment) |
| `--topology` | Which agent arrangement to use; see `mas topologies` |
| `--format`, `-f` | `markdown` (default), `json` or `text` |
| `--output`, `-o` | Write to a file instead of stdout |
| `--force-agents` | Run the agent phase even when a deterministic check is conclusive |
| `--lang` | Report language: `en` or `zh` |

### Offline and online

**Offline** is the default and reads only your telemetry backends. It needs no
cluster credentials and is the right mode for analysing an incident after the
fact, or for a customer's exported data.

**Online** additionally reads the live environment: pods, events, nodes,
workloads, host processes and ports. It requires read-only credentials. Even
here nothing is written — "online" widens what can be *read*, never what can be
done.

## 6. How a diagnosis actually works

The pipeline has two phases, and the order matters.

**Phase 1 — deterministic.** Playbooks from the knowledge pack run first: ordered
collect → evaluate → conclude steps with **no model in the loop at all**. For a
Redis memory-pressure incident this establishes, at zero cost and with complete
reproducibility, whether used memory is above maxmemory, whether keys are being
evicted, and whether the hit ratio collapsed.

If a deterministic finding clears the configured confidence threshold
(`run.deterministic_short_circuit`, default 0.85), the run **stops there**. Your
routine incidents cost nothing and return in under two seconds.

**Phase 2 — agentic.** Only where the rules are inconclusive do agents run, and
they start from the deterministic findings rather than from nothing. Under the
default `supervisor` topology: a planner decides what is still unsettled;
specialised investigators — one per evidence domain, running concurrently —
gather targeted evidence; a correlator merges it into ranked hypotheses; a critic
challenges each one against the evidence and refutes what does not hold; a
reporter writes the summary and the recommendations.

The critic is not decoration. An explanation that has never been challenged is
just the first thing that came to mind, and the report shows you what was
refuted and why.

### When something is unavailable

A source that is down does not fail the run. It produces a recorded **gap** with
a code and a statement of what it costs the conclusion:

```markdown
## Gaps in the evidence

- **kube.nodes()** — refused by the safety guard (`MAS-4201`)
  - Effect on this analysis: node-level memory pressure could not be ruled out
```

A check whose input is missing is **skipped**, never evaluated as passing. A
missing measurement is not a healthy one, and the report never lets you confuse
the two.

## 7. Reading the report

Sections appear in the order an on-call engineer needs them:

1. **Summary** — the conclusion, first.
2. **Hypotheses** — ranked, each with status (supported / refuted /
   inconclusive), confidence, reasoning, and the evidence ids for and against.
3. **Findings** — what the deterministic checks and agents established, each
   traceable to the rule or role that produced it.
4. **Checks that passed** — what was ruled out. Often as valuable as what was found.
5. **Gaps in the evidence** — what could not be checked, and what that costs.
6. **Recommended next steps** — risk-labelled, and explicitly advisory.
7. **Evidence** — every collected item with the query that produced it.
8. **Run accounting** — model calls, tool calls, tokens, duration.

Gaps deliberately come **before** recommendations, so you never act on advice
without seeing its limits.

Recommendations carry a risk level: **low** (read-only inspection), **medium** (a
reversible change), **high** (can lose data or cause an outage). Every one is
something for you to do — the report says so explicitly, and the `advisory` field
in the JSON form is always `true`.

## 8. Choosing a topology

```bash
mas topologies
```

Five architectures ship. They differ in **control flow only**: every one of them
reads the same deterministic findings, uses the same guarded tools, and writes
through the same shared state. That is what makes choosing between them a
decision you can settle with evidence.

| Topology | Shape | Cost | Choose it when | Avoid it when |
|---|---|---|---|---|
| `supervisor` | Planner → concurrent per-domain investigators → correlator → critic → reporter | Moderate, predictable; domain calls overlap | The default: evidence is spread across domains and one broad pass reaches it | Evidence is expensive and one check would probably settle it |
| `single` | One generalist with every tool | Cheapest: one conversation | You want the control condition — the baseline others must beat | The incident is ambiguous; nothing challenges a plausible first answer |
| `plan-execute` | Strategist names an objective → executor pursues it → strategist re-plans → … | Lowest when the first objective settles it; highest when it does not | Evidence is expensive, or one check would probably answer it. The only topology that can stop after one | You already know several domains are involved; then it costs the same as `supervisor` and runs in series |
| `debate` | Investigate → correlate → an advocate argues each position → judge decides | Dearest: `supervisor` plus one call per position, up to three | Two explanations fit the same evidence and choosing wrong is expensive | The evidence already points one way; a staged debate can lend a weak position standing it did not earn |
| `blackboard` | Contributors act when the shared state makes them eligible, in rounds, until a round changes nothing | Varies with what evidence exists; control itself costs no model calls | Evidence arrives unevenly and a fixed script would spend calls discovering that | You want a predictable transcript: what runs depends on the state |

`mas topologies` prints the same table in your configured language, and
`mas topologies --json` carries both languages for an integration to render.

### Comparing them on your own incidents

Because the topology is the *only* thing that changes between runs of the same
case, running one case through several is a genuine comparison rather than an
impression:

```bash
for t in single supervisor plan-execute debate blackboard; do
  mas diagnose -t redis-prod -s "latency spike" --since 1h \
    --topology "$t" -f json -o "runs/$t.json"
done

# What each one cost, and what it concluded:
jq -r '[.topology, (.usage.llm_calls|tostring), (.usage.tool_calls|tostring),
        (.usage.wall_millis|tostring), .hypotheses[0].statement] | @tsv' runs/*.json
```

The cost figures above are measurements; the conclusions on your own incidents
are yours to judge. For a repeatable comparison, `mas eval` runs a corpus of
cases whose causes are known.

### Measuring against cases with known causes

```bash
mas eval             # the shipped corpus, your configured topology
mas eval --matrix    # every topology, the same cases
```

Each case carries synthetic telemetry and the failure modes a correct diagnosis
reaches. The whole pipeline runs against it, so what is measured is the system
you actually run rather than a stubbed version of it.

```
CASE                                 TOPOLOGY    RESULT  FALSE  GAPS  CALLS  COST
kafka-broker-loss-under-replicated   supervisor  hit     0      ok    8      unpriced

supervisor     7/7 hit · 0 miss · 0 false conclusion(s) · 0 gap(s) missed
```

Four outcomes are reported side by side and **never combined into one score**: a
miss leaves you where you started, a false conclusion sends you somewhere wrong
with confidence, and any weighted sum would let a change that trades the first
for the second look like an improvement.

The corpus is synthetic — it measures agreement with its own labels, not
accuracy on real incidents — and every rendering says so. The exit status is
non-zero when a case misses or reaches a ruled-out conclusion, which is what
makes it usable as a CI gate. Point `--cases` at a directory to add your own;
[the evaluation guide](./evaluation.md) is the format and the reasoning.

To see **what moved** rather than only whether everything is green, record a
baseline and compare against it:

```bash
mas eval --matrix --write-baseline baseline.json   # record (a person's act)
mas eval --matrix --baseline baseline.json         # compare
mas eval --matrix --models fast-model,strong-model # the model axis
```

Regressions and improvements are reported side by side and never netted, and a
cell that fails exactly as recorded stays listed without failing the build — so
a case that legitimately cannot pass does not have to be deleted to keep CI
green. Each cell is one sample: under a real model that is a draw, not a
measurement, and the comparison says so every time.

### What a run cost, and who spent it

Model calls and tool calls are always counted. Money is only reported when you
have said what a model costs:

```yaml
llm:
  pricing:
    claude-opus-5: { input_per_mtok: 5.00, output_per_mtok: 25.00 }
```

**This project ships no price list**, because prices change and a stale number
that looks authoritative is a false claim. A model with no price makes the run's
cost *unknown* — never `$0.00`, which you would read as free. `mas models` shows
which models are priced and which are not.

Once priced, the report carries a per-role breakdown:

| Role | Model calls | Tokens | Duration | Cost |
|---|---:|---:|---:|---|
| advocate | 3 | 4,200 | 2.1s | $0.0203 |
| investigator | 4 | 3,100 | 0.8s | $0.0044 |

That is what makes the topology comparison actionable. "Debate costs more" is a
fact you can only act on once you know *where* the extra went: if it is all in
the advocates, routing that one role to a cheaper model is a smaller change than
abandoning the topology.

## 9. Extending the knowledge

Middleware expertise lives in YAML packs, not in code. Adding a middleware needs
no rebuild.

```bash
mas packs                  # what is loaded
mas packs --show redis     # signals, log patterns and failure modes in detail
```

A pack declares:

- **signals** — named PromQL fragments, parameterised by the target's selector;
- **logPatterns** — regexes with their meaning;
- **failureModes** — how this middleware goes wrong, and the vetted advice for each;
- **playbooks** — the deterministic checks;
- **inspect** — read-only commands the adapters may run.

Put your packs in a directory listed under `knowledge.pack_dirs`. A pack with the
same id as a shipped one replaces it, so you can correct our knowledge without
forking the binary.

Every operator-facing string must be present in **both** English and Chinese; the
loader rejects a pack that translates only half of itself. An `inspect` command
is re-validated by the safety guard at call time, so a pack cannot introduce a
mutating command regardless of what it claims.

## 10. Serving the API

```bash
mas serve --addr :8080
```

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/v1/diagnoses` | Create a run; `?wait=true` blocks and returns the report |
| GET | `/api/v1/diagnoses` | List runs, newest first |
| GET | `/api/v1/diagnoses/{id}` | Run status and report; `?steps=true` adds the audit trail |
| GET | `/api/v1/targets` | Configured targets |
| GET | `/api/v1/topologies` | Available topologies |
| GET | `/api/v1/packs` | Loaded knowledge packs |
| GET | `/healthz` `/readyz` | Liveness and readiness |
| GET | `/metrics` | Prometheus exposition of MAS-Turbo's own metrics |

```bash
curl -s localhost:8080/api/v1/diagnoses?wait=true \
  -H 'Content-Type: application/json' \
  -d '{"target":"redis-prod","symptom":"p99 latency spike","since":"1h"}' | jq .
```

Every failure response carries a `code`, a `message` in the configured language,
and a `remedy`.

### Authentication

Binding anything other than loopback requires credentials, and the process
refuses to start without them:

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
    terminated_by_proxy: true    # or set cert_file and key_file
```

```bash
curl -s https://mas.internal/api/v1/targets -H "Authorization: Bearer $TOKEN"
```

`read` covers everything already computed; `diagnose` covers starting a run,
which spends model tokens and reads production telemetry. A status page needs
the first and must not have the second.

`127.0.0.1` needs none of this — the host is already the boundary, so the local
workflow is unchanged. What you cannot do is expose the API and forget:
`mas serve` refuses (`MAS-7010`), and serving a bearer token over plaintext
off-host refuses too (`MAS-7011`) unless you state that a proxy terminates TLS.

`mas doctor` shows the exposure and what protects it — by principal and scope,
never the credential. Every run records who asked for it, and that appears in
`GET /api/v1/diagnoses/{id}` as `principal`.

`/healthz` and `/readyz` stay anonymous. `/metrics` does not: it carries target
names and run counts.

## 11. Auditing and replaying a run

Every run is persisted: the request, every tool call with its arguments and
redacted result, every model exchange, and the final report.

```bash
mas runs                       # list stored runs
mas replay <run-id>            # reproduce the report
mas replay <run-id> --steps    # the complete audit trail
```

Replay contacts **nothing** — no telemetry, no cluster, no model. A stored run is
reproducible on a laptop with the network off, which is what makes it an audit
trail rather than an archive. Records carry an integrity digest; a modified or
truncated record is refused with `MAS-6003` rather than replayed as genuine.

### Where runs are kept

`store.type: fs` keeps them on one machine's disk. That is right for a laptop
and wrong for more than one replica: in Kubernetes the filesystem is usually the
pod's, so a restart loses the history and a second replica cannot see the first
one's runs.

```yaml
store:
  type: s3
  s3:
    endpoint: http://minio:9000
    region: us-east-1
    bucket: mas-runs
    access_key_id: "${env:MAS_S3_KEY_ID}"
    secret_access_key: "${env:MAS_S3_SECRET}"
    path_style: true
```

Any S3-compatible bucket works — AWS S3, MinIO, Ceph RGW. Each run gets its own
prefix, with the record written at the start and again at the end and each step
as its own immutable object, so nothing is ever rewritten and a run interrupted
part-way is still readable. A reconstructed run stays marked `running`: it is
what was recorded, not a claim that it finished.

`mas doctor` says which store is in use and whether the bucket answers. If the
store fails after an analysis is complete you still get the report, with a note
saying it was not persisted — see [configuration](./configuration.md) for the
full reference.

## 12. When something goes wrong

Every error carries a stable code, a message and a remedy:

```
MAS-4001  metrics source "primary" is unreachable: dial tcp 10.0.0.5:9090: i/o timeout
          Check telemetry.metrics[].url and network policy; analysis continues without metrics.
```

```bash
mas errcodes                   # the whole registry
mas errcodes --filter 8001     # one code
mas errcodes --lang zh         # in Chinese
```

The exit status distinguishes the domain, so a script can react appropriately:

| Status | Meaning |
|---|---|
| 0 | Success |
| 1 | Unclassified failure |
| 2 | Configuration or request problem |
| 3 | Refused by the safety guard |
| 4 | A collector or adapter failed |
| 5 | Model provider or orchestration failure |
| 6 | Run storage failure |

For deeper investigation, `--log-level debug` logs prompts and tool arguments —
after redaction. Every log line carries the `run_id`, so a whole run can be
extracted from a shared log stream.

## 13. Kubernetes deployment

MAS-Turbo needs only read permission. Grant it no more:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mas-turbo-reader
rules:
  - apiGroups: [""]
    resources: ["pods", "pods/log", "events", "nodes", "services", "endpoints"]
    verbs: ["get", "list"]
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "daemonsets", "replicasets"]
    verbs: ["get", "list"]
  - apiGroups: ["metrics.k8s.io"]
    resources: ["pods", "nodes"]
    verbs: ["get", "list"]
```

No `create`, `update`, `patch` or `delete` on any resource. If you ever see
MAS-Turbo request one of those, that is a bug worth reporting.

### In-container inspection (optional)

Reading a middleware's own diagnostics — `redis-cli INFO all`,
`mongosh --eval "rs.status()"` — means running a command inside its container,
and in Kubernetes that needs one more permission:

```yaml
  # Optional. Grant this only if you want in-container inspection.
  - apiGroups: [""]
    resources: ["pods/exec"]
    verbs: ["create"]
```

This is a real widening and it is stated plainly rather than buried:
`pods/exec` lets the holder run *any* command in *any* pod the role covers. What
bounds MAS-Turbo is not the RBAC rule but its own guard, and the bound is four
independent things, none of which any configuration or prompt can widen:

| Bound | Set by |
|---|---|
| Which binaries | The read-only command allow-list — `redis-cli`, `mongosh`, `kafka-*.sh`, … and nothing else |
| Which arguments | Mutating-verb detection and per-flag value allow-lists: `INFO` runs, `FLUSHALL` does not |
| Which pods | Only the pods the target you named resolved to |
| Which endpoint | Only the exec subresource; the code cannot address another |

**Exec changes where vetted commands run. It never changes which commands are
vetted.** `kubectl` is not on the allow-list and will not be added: one binary
name would put the whole Kubernetes API behind it, which is exactly what
deny-by-default exists to prevent.

If your policy forbids exec regardless of the command, turn it off and grant
nothing:

```yaml
envs:
  prod:
    type: kubernetes
    exec: false
```

The tool is then not registered at all, so it cannot be called however a prompt
is phrased. `mas doctor` reports it as a policy decision (`MAS-4210`) rather
than a missing capability, so nobody spends an afternoon debugging RBAC for a
switch someone deliberately set.

## 14. What is deliberately not here yet

Honest scope, so you can plan around it:

- **A web UI** — CLI and API only for now.
- **Per-target authorisation** — a credential that may diagnose may diagnose
  any configured target. Splitting that needs a tenancy model, which is not
  here yet.
- **Rate limiting** — a run's budget bounds what one call can spend, but
  nothing bounds how many calls arrive.

## 15. Getting help

- `mas <command> --help` for any command
- [Configuration reference](./configuration.md)
- [Error-code reference](./error-codes.md)
- Attach the output of `mas doctor` and, where you can share it, the run record
  from `mas replay <run-id> --steps` — it contains everything needed to
  understand what happened, with credentials already redacted.
