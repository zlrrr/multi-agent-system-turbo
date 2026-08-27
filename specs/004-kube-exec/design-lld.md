# Low-Level Design (LLD): Read-Only Command Execution Inside Kubernetes Pods

> **Feature ID**: `004-kube-exec` · **Version**: 1.0.0 · **Status**: approved
> **Bilingual pair**: [`design-lld.zh.md`](./design-lld.zh.md) · **Upstream**: [`design-hld.md`](./design-hld.md) v1.0.0 · **Downstream**: [`tasks.md`](./tasks.md), code

## 1. Files

| Path | Content |
|---|---|
| `internal/safety/guard.go` | `ExecEffect`; `Call.Exec`; `authorizeExec`; exec path rule |
| `internal/envadapter/kube/wsconn.go` | RFC 6455 client: handshake, frame reader, close |
| `internal/envadapter/kube/exec.go` | `ExecClient`, the channel demultiplexer, `ExecResult` |
| `internal/envadapter/kube/exec_test.go` | A server that speaks the real protocol |
| `internal/envadapter/kube/adapter.go` | The `kube.exec` tool; instance binding; the switch |
| `internal/config/config.go` | `EnvConfig.Exec *bool` — disable-only |
| `internal/audit/structural_test.go` | `TestExecClientAddressesOneEndpoint` |
| `pkg/errs/registry.go` | `MAS-4210`…`MAS-4214` |

## 2. Guard

```go
// ExecEffect describes running a command inside a container. It is one effect
// with two constraints, not two effects: modelling it as two Authorize calls
// would leave the composition to the caller, and a caller that omitted the
// command check would still compile.
type ExecEffect struct {
    Namespace, Pod, Container string
    Binary                    string
    Args                      []string
}

type Call struct {
    …
    Exec *ExecEffect
}
```

`Authorize` counts `Exec` among the mutually exclusive effects (still exactly
one), then:

```go
func (g *Guard) authorizeExec(c Call) error {
    // 1 · the command must pass the same allow-list as the local adapter,
    //     reusing authorizeCommand verbatim so the two can never diverge.
    // 2 · the implied URL must match the exec path rule and nothing else.
    // 3 · namespace, pod and container must be DNS-1123-shaped: a path
    //     component containing "/" or ".." could otherwise escape the rule
    //     that step 2 just checked.
}
```

Step 3 is the subtle one. The path rule is a regex over a URL built from these
three fields; if a field could contain a slash, the built URL would still match
the rule while addressing something else. Validating the components is what
makes the rule mean what it appears to mean.

New path rule, sitting with its neighbours:

```go
{"GET", `^/api/v1/namespaces/[^/]+/pods/[^/]+/exec$`, "Kubernetes exec (read-only commands only)"},
```

It is a `GET` rule because the WebSocket upgrade is a GET — no new verb enters
the guard.

## 3. The WebSocket client

```go
// dialWebSocket performs the RFC 6455 handshake over an existing TLS transport
// and returns a frame reader. It is deliberately minimal: this project needs to
// read server frames on one short-lived connection and never needs to send
// anything after the handshake.
func dialWebSocket(ctx context.Context, hc *http.Client, url string,
    header http.Header, subprotocol string) (*wsConn, error)

func (c *wsConn) ReadMessage() (opcode byte, payload []byte, err error)
func (c *wsConn) Close() error
```

Handshake: `GET` with `Upgrade: websocket`, `Connection: Upgrade`,
`Sec-WebSocket-Version: 13`, a 16-byte random `Sec-WebSocket-Key`, and the
subprotocol. A `101` is required, and `Sec-WebSocket-Accept` is verified against
`base64(sha1(key + RFC6455 GUID))` — an unverified accept would let a proxy that
did not understand the upgrade look like success.

The connection is obtained with `http.Client.Do` on a request whose body is left
open, using `httputil`-free hijacking of the underlying connection via
`http.Transport`'s round trip on a `net.Conn` we dialled ourselves. Reads honour
the context deadline by setting it on the `net.Conn`, so a hung apiserver cannot
outlive the run's timeout (RSK-001).

Frames: server-to-client frames are never masked; continuation frames are
reassembled; `Ping` is answered with `Pong`; `Close` ends the read loop. A
reserved bit or an unexpected opcode is `MAS-4213`.

## 4. `ExecClient`

```go
// ExecClient runs one read-only command inside one container.
//
// It has exactly one method and takes no path argument: the URL is built from
// namespace, pod and container, so there is no input by which a caller could
// address a different endpoint. TestExecClientAddressesOneEndpoint asserts that
// shape, which is what lets kube.Client keep its own name-based invariant
// unchanged (plan.md §1).
type ExecClient struct { … }

type ExecRequest struct {
    Namespace, Pod, Container string
    Command                   []string // argv; never a string, so nothing needs a shell
    MaxBytes                  int
}

type ExecResult struct {
    Stdout, Stderr string
    ExitCode       int
    Truncated      bool
}

func (c *ExecClient) Run(ctx context.Context, req ExecRequest) (ExecResult, error)
```

Query: one `command=` per argv element, `container=`, `stdout=true`,
`stderr=true`, `stdin=false`, `tty=false`. Absent `stdin` is not an optimisation
— it is the difference between a read and a session (HLD §4).

Demultiplexing: byte 0 of each binary frame is the channel — 1 stdout, 2 stderr,
3 status. The status frame carries a JSON `Status`; `status: "Success"` is exit
0, otherwise the exit code comes from `details.causes[reason="ExitCode"].message`.
A stream that ends with no status frame is `MAS-4214`: the command's outcome is
unknown, and reporting unknown as success is exactly what this project must not
do.

## 5. The tool

```
kube.exec(instance, command_id) → evidence
```

The tool takes an **instance name** from the resolved target and a **command id**
from the knowledge pack — never a pod name and never an argv from the model. A
model that has read a hostile log line can therefore ask only for a command the
pack already declared, in a pod the target already resolved to (HLD §4, D-5).

Container: the pack's command may name one; otherwise the pod's first container.

## 6. Configuration

```yaml
envs:
  prod:
    type: kubernetes
    exec: false        # narrowing only: absent or true means the guard decides
```

There is no key that widens anything. `exec: false` removes the tool from the
registry entirely, so a disabled environment cannot execute even if a prompt asks
— and `doctor` reports it as a policy decision rather than a missing capability.

## 7. Errors

| Code | Meaning |
|---|---|
| `MAS-4210` | Exec is disabled for this environment by configuration |
| `MAS-4211` | The pod named is not an instance of the resolved target |
| `MAS-4212` | The connection could not be upgraded (RBAC, policy, or no such pod) |
| `MAS-4213` | The remote-command stream was malformed |
| `MAS-4214` | The command's exit status never arrived |

Refusals reuse the guard's existing codes: this feature adds no way to be
refused that did not exist before.

## 8. Tests

| Test | Property |
|---|---|
| `TestGuardAuthorisesExecAsOneEffect` | FR-002 — both constraints, one call |
| `TestExecRefusesUnlistedBinary`, `TestExecRefusesMutatingCommand` | FR-003, FR-004 — before any connection |
| `TestExecPathComponentsCannotEscape` | §2 step 3 — a slash or `..` in a component is refused |
| `TestClientHasNoMutatingMethods` | FR-005 — unchanged, still passing |
| `TestExecClientAddressesOneEndpoint` | The replacement invariant |
| `TestExecCapturesStreamsAndExitStatus` | FR-006, against the real-protocol server |
| `TestExecTruncatesAtCeiling` | FR-007 |
| `TestExecRefusesPodOutsideTarget` | FR-008 |
| `TestExecCanBeDisabledPerEnvironment` | FR-009 |
| `TestExecRequiresOnlineMode` | FR-010 |
| `TestExecUpgradeFailureIsCoded`, `TestExecMalformedFrameIsCoded`, `TestExecMissingStatusIsCoded` | FR-012 |
| `TestExecHonoursTimeout` | NFR-006 |

## Change Log

| Version | Date | Change | Impact |
|---|---|---|---|
| 1.0.0 | 2026-08-25 | Initial low-level design | tasks, code |
