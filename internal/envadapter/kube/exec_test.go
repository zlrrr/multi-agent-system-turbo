package kube

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // RFC 6455 handshake echo, mirroring the client
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// wsServer speaks v4.channel.k8s.io from the server side, framing its own
// output rather than replaying what the client expects.
//
// That direction matters (NFR-002). A server built by asking the client what it
// wants to read would test the client against a mirror of its own assumptions,
// and a demultiplexer that swapped stdout for stderr would pass.
type wsServer struct {
	t *testing.T

	// script is what the server sends once the handshake completes.
	script []wsFrame
	// handshake lets a test break the upgrade deliberately.
	statusLine   string
	badAccept    bool
	omitUpgrade  bool
	requestQuery chan string

	ln net.Listener
}

// wsFrame is one frame the server will send, described in protocol terms.
type wsFrame struct {
	opcode  byte
	channel int // -1 for a frame with no channel prefix
	payload string
	fin     bool
	rsv     bool // set a reserved bit, which no extension negotiated
	masked  bool // mask a server frame, which RFC 6455 forbids
	raw     []byte
	delay   time.Duration
}

func newWSServer(t *testing.T, script ...wsFrame) *wsServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &wsServer{t: t, script: script, ln: ln, requestQuery: make(chan string, 1)}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *wsServer) addr() string { return s.ln.Addr().String() }

func (s *wsServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *wsServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)

	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	select {
	case s.requestQuery <- req.URL.RequestURI():
	default:
	}

	if s.statusLine != "" {
		fmt.Fprintf(conn, "HTTP/1.1 %s\r\nContent-Length: 9\r\n\r\nforbidden", s.statusLine)
		return
	}

	accept := serverAccept(req.Header.Get("Sec-WebSocket-Key"))
	if s.badAccept {
		accept = "not-the-right-value"
	}
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	if !s.omitUpgrade {
		b.WriteString("Upgrade: websocket\r\n")
	}
	b.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&b, "Sec-WebSocket-Accept: %s\r\n", accept)
	fmt.Fprintf(&b, "Sec-WebSocket-Protocol: %s\r\n", remoteCommandProtocol)
	b.WriteString("\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return
	}

	for _, f := range s.script {
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		if _, err := conn.Write(f.encode()); err != nil {
			return
		}
	}
	// Close the way a real apiserver does, once the command is done.
	_, _ = conn.Write(wsFrame{opcode: opClose, fin: true, channel: -1}.encode())
}

func serverAccept(key string) string {
	h := sha1.New() //nolint:gosec // RFC 6455 handshake echo
	_, _ = io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// encode renders the frame on the wire, including the deliberately wrong shapes
// a test may ask for.
func (f wsFrame) encode() []byte {
	if f.raw != nil {
		return f.raw
	}
	payload := []byte(f.payload)
	if f.channel >= 0 {
		payload = append([]byte{byte(f.channel)}, payload...)
	}

	first := f.opcode
	if f.fin {
		first |= 0x80
	}
	if f.rsv {
		first |= 0x40
	}

	out := []byte{first}
	switch n := len(payload); {
	case n <= 125:
		out = append(out, byte(n))
	case n <= 0xFFFF:
		ext := make([]byte, 2)
		binary.BigEndian.PutUint16(ext, uint16(n))
		out = append(out, 126)
		out = append(out, ext...)
	default:
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(n))
		out = append(out, 127)
		out = append(out, ext...)
	}
	if f.masked {
		out[1] |= 0x80
		mask := []byte{1, 2, 3, 4}
		out = append(out, mask...)
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return append(out, payload...)
}

// stdout, stderr and status build the frames a real exec produces.
func stdout(text string) wsFrame {
	return wsFrame{opcode: opBinary, channel: channelStdout, payload: text, fin: true}
}
func stderr(text string) wsFrame {
	return wsFrame{opcode: opBinary, channel: channelStderr, payload: text, fin: true}
}
func successStatus() wsFrame {
	return wsFrame{opcode: opBinary, channel: channelStatus, fin: true,
		payload: `{"metadata":{},"status":"Success"}`}
}
func exitStatus(code int) wsFrame {
	return wsFrame{opcode: opBinary, channel: channelStatus, fin: true,
		payload: fmt.Sprintf(
			`{"metadata":{},"status":"Failure","message":"command terminated with non-zero exit code",`+
				`"reason":"NonZeroExitCode","details":{"causes":[{"reason":"ExitCode","message":"%d"}]}}`,
			code)}
}

// execClientFor points an ExecClient at the test server over plain TCP. The
// handshake and framing are what is under test; TLS is the read client's
// already-tested concern.
func execClientFor(s *wsServer) *ExecClient {
	e := &ExecClient{creds: credentials{server: "http://" + s.addr(), bearerToken: "test-token"}}
	e.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	return e
}

func run(t *testing.T, s *wsServer, req ExecRequest) (ExecResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if req.Namespace == "" {
		req.Namespace, req.Pod, req.Container = "middleware", "redis-0", "redis"
	}
	if len(req.Command) == 0 {
		req.Command = []string{"redis-cli", "INFO", "all"}
	}
	return execClientFor(s).Run(ctx, req)
}

// TestExecCapturesStreamsAndExitStatus is FR-006: the streams must not be
// confused with each other, and the exit status must come from the status
// channel rather than being assumed.
func TestExecCapturesStreamsAndExitStatus(t *testing.T) {
	s := newWSServer(t,
		stdout("# Memory\r\nused_memory:1048576\r\n"),
		stderr("warning: NOAUTH not required\n"),
		stdout("maxmemory_policy:allkeys-lru\r\n"),
		successStatus(),
	)

	res, err := run(t, s, ExecRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "used_memory:1048576") ||
		!strings.Contains(res.Stdout, "maxmemory_policy") {
		t.Errorf("stdout lost content: %q", res.Stdout)
	}
	if strings.Contains(res.Stdout, "warning:") {
		t.Errorf("stderr was mixed into stdout: %q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "NOAUTH") {
		t.Errorf("stderr lost content: %q", res.Stderr)
	}
	if !res.ExitKnown || res.ExitCode != 0 {
		t.Errorf("exit = %d (known %v), want 0 known", res.ExitCode, res.ExitKnown)
	}

	// The query must carry the argv one element at a time, with no stdin and no
	// tty: both would turn a read into a session.
	query := <-s.requestQuery
	for _, want := range []string{
		"command=redis-cli", "command=INFO", "command=all",
		"container=redis", "stdout=true", "stderr=true", "stdin=false", "tty=false",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
}

// TestExecReportsNonZeroExit: a command that ran and disagreed is a result, not
// a transport failure.
func TestExecReportsNonZeroExit(t *testing.T) {
	s := newWSServer(t, stderr("ERR unknown command\n"), exitStatus(1))

	res, err := run(t, s, ExecRequest{})
	if err != nil {
		t.Fatalf("a non-zero exit must not fail the call: %v", err)
	}
	if !res.ExitKnown || res.ExitCode != 1 {
		t.Errorf("exit = %d (known %v), want 1 known", res.ExitCode, res.ExitKnown)
	}
	if !strings.Contains(res.Stderr, "unknown command") {
		t.Errorf("stderr = %q", res.Stderr)
	}
}

// TestExecMissingStatusIsCoded is the honesty case: a stream that ends without
// a status leaves the outcome unknown, and unknown must not read as success.
func TestExecMissingStatusIsCoded(t *testing.T) {
	s := newWSServer(t, stdout("partial output"))

	res, err := run(t, s, ExecRequest{})
	if errs.CodeOf(err) != "MAS-4214" {
		t.Fatalf("got %v (%s), want MAS-4214", err, errs.CodeOf(err))
	}
	if res.ExitKnown {
		t.Error("the exit status was reported as known when none arrived")
	}
	if !strings.Contains(res.Stdout, "partial output") {
		t.Error("output collected before the stream ended was discarded")
	}
}

// TestExecTruncatesAtCeiling is FR-007.
func TestExecTruncatesAtCeiling(t *testing.T) {
	s := newWSServer(t,
		stdout(strings.Repeat("a", 400)),
		stdout(strings.Repeat("b", 400)),
		successStatus(),
	)

	res, err := run(t, s, ExecRequest{MaxBytes: 500})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.Stdout) + len(res.Stderr); got > 500 {
		t.Errorf("captured %d bytes against a 500-byte ceiling", got)
	}
	if !res.Truncated {
		t.Error("output was cut without the result saying so")
	}
}

// TestExecReassemblesContinuationFrames: a long INFO reply arrives fragmented,
// and a demultiplexer that ignored continuation frames would silently lose the
// tail of it.
func TestExecReassemblesContinuationFrames(t *testing.T) {
	s := newWSServer(t,
		wsFrame{opcode: opBinary, channel: channelStdout, payload: "used_memory:", fin: false},
		wsFrame{opcode: opContinuation, channel: -1, payload: "1048576", fin: true},
		successStatus(),
	)

	res, err := run(t, s, ExecRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "used_memory:1048576" {
		t.Errorf("stdout = %q, want the reassembled message", res.Stdout)
	}
}

// TestExecAnswersPing keeps a long-running command alive: an apiserver that
// pings an unresponsive client may drop the connection mid-command.
func TestExecAnswersPing(t *testing.T) {
	s := newWSServer(t,
		wsFrame{opcode: opPing, channel: -1, payload: "keepalive", fin: true},
		stdout("ok"),
		successStatus(),
	)

	res, err := run(t, s, ExecRequest{})
	if err != nil {
		t.Fatalf("a ping mid-stream broke the read: %v", err)
	}
	if res.Stdout != "ok" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestExecMalformedFrameIsCoded(t *testing.T) {
	for name, script := range map[string][]wsFrame{
		"reserved bit set": {{opcode: opBinary, channel: channelStdout, payload: "x", fin: true, rsv: true}},
		"server masked a frame": {
			{opcode: opBinary, channel: channelStdout, payload: "x", fin: true, masked: true}},
		"text frame": {{opcode: opText, channel: -1, payload: "not binary", fin: true}},
		"unknown channel": {
			{opcode: opBinary, channel: 9, payload: "x", fin: true}, successStatus()},
		"orphan continuation": {{opcode: opContinuation, channel: -1, payload: "x", fin: true}},
		"status is not JSON": {
			{opcode: opBinary, channel: channelStatus, payload: "not json", fin: true}},
	} {
		t.Run(name, func(t *testing.T) {
			s := newWSServer(t, script...)
			_, err := run(t, s, ExecRequest{})
			if code := errs.CodeOf(err); code != "MAS-4213" {
				t.Fatalf("got %v (%s), want MAS-4213", err, code)
			}
		})
	}
}

func TestExecUpgradeFailureIsCoded(t *testing.T) {
	for name, mutate := range map[string]func(*wsServer){
		"forbidden":              func(s *wsServer) { s.statusLine = "403 Forbidden" },
		"no upgrade header":      func(s *wsServer) { s.omitUpgrade = true },
		"accept does not verify": func(s *wsServer) { s.badAccept = true },
	} {
		t.Run(name, func(t *testing.T) {
			s := newWSServer(t, successStatus())
			mutate(s)
			_, err := run(t, s, ExecRequest{})
			if code := errs.CodeOf(err); code != "MAS-4212" {
				t.Fatalf("got %v (%s), want MAS-4212", err, code)
			}
		})
	}
}

// TestExecHonoursTimeout is NFR-006: a server that accepts the connection and
// then says nothing must not outlive the run's deadline.
func TestExecHonoursTimeout(t *testing.T) {
	s := newWSServer(t, wsFrame{
		opcode: opBinary, channel: channelStdout, payload: "late", fin: true,
		delay: 3 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := execClientFor(s).Run(ctx, ExecRequest{
		Namespace: "middleware", Pod: "redis-0", Container: "redis",
		Command: []string{"redis-cli", "INFO"},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a silent server was reported as a successful command")
	}
	if elapsed > 2*time.Second {
		t.Errorf("the call took %s; the deadline was 300ms", elapsed)
	}
}

// TestExecClientAddressesOneEndpoint is the invariant that lets kube.Client keep
// its own. ExecClient must expose exactly one method and take no path argument,
// so no caller input can point it anywhere but the exec subresource.
func TestExecClientAddressesOneEndpoint(t *testing.T) {
	typ := reflect.TypeOf(&ExecClient{})
	var exported []string
	for i := 0; i < typ.NumMethod(); i++ {
		exported = append(exported, typ.Method(i).Name)
	}
	if len(exported) != 1 || exported[0] != "Run" {
		t.Fatalf("ExecClient exposes %v; it must expose exactly Run, so there is no second "+
			"way to reach the apiserver", exported)
	}

	// ExecRequest must carry no free-form path or URL: a field like that would
	// reintroduce exactly the addressing freedom this type exists to remove.
	reqType := reflect.TypeOf(ExecRequest{})
	for i := 0; i < reqType.NumField(); i++ {
		name := strings.ToLower(reqType.Field(i).Name)
		for _, banned := range []string{"path", "url", "endpoint", "query", "raw"} {
			if strings.Contains(name, banned) {
				t.Errorf("ExecRequest.%s lets a caller influence the endpoint", reqType.Field(i).Name)
			}
		}
	}
}

// execAdapter wires an adapter to the test server, with a pack's inspect
// command installed and one resolved instance — the state a real run reaches
// after resolution.
func execAdapter(t *testing.T, s *wsServer) *Adapter {
	t.Helper()
	a := NewAdapter("prod", &Client{
		creds:   credentials{server: "http://" + s.addr(), namespace: "middleware"},
		timeout: 5 * time.Second,
		hc:      &http.Client{Transport: &http.Transport{}},
	}, "middleware")
	a.SetInspectCommands([]InspectCommand{{
		ID: "server-info", Binary: "redis-cli",
		Args:        []string{"-h", "{{.host}}", "-p", "{{.port}}", "INFO", "all"},
		Description: "Redis INFO",
	}})
	a.SetInstances("middleware", []core.Instance{
		{Name: "redis-0", Address: "10.0.0.1"},
		{Name: "redis-1", Address: "10.0.0.2"},
	})
	// The adapter would otherwise dial TLS; the handshake and framing are what
	// this exercises, and the read client's TLS is tested separately.
	a.exec = execClientFor(s)
	a.execOnce.Do(func() {})
	return a
}

func execToolOf(t *testing.T, a *Adapter) tool.Tool {
	t.Helper()
	for _, tl := range a.Tools() {
		if tl.Name() == "kube.exec" {
			return tl
		}
	}
	t.Fatal("the adapter registered no kube.exec tool")
	return nil
}

// TestExecRunsPackInspectCommand is FR-001, end to end through the tool: a
// pack's declared command reaches the pod and its output comes back as evidence.
func TestExecRunsPackInspectCommand(t *testing.T) {
	s := newWSServer(t, stdout("# Memory\r\nused_memory:1048576\r\n"), successStatus())
	a := execAdapter(t, s)
	tl := execToolOf(t, a)

	// The declared effect must be an exec — one effect the guard checks twice.
	call, err := tl.Plan(map[string]any{"id": "server-info"})
	if err != nil {
		t.Fatal(err)
	}
	if call.Exec == nil {
		t.Fatal("the tool declared no exec effect, so the guard would not check the command")
	}
	if call.Exec.Pod != "redis-0" || call.Exec.Namespace != "middleware" {
		t.Errorf("effect targets %s/%s, want middleware/redis-0", call.Exec.Namespace, call.Exec.Pod)
	}
	if call.Exec.Binary != "redis-cli" {
		t.Errorf("binary = %q", call.Exec.Binary)
	}

	// And the guard must actually accept it: a pack command that passes the
	// allow-list on a host must pass it in a pod.
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	call.Class = safety.ClassReadOnly
	if err := g.Authorize(context.Background(), call); err != nil {
		t.Fatalf("the guard refused a pack command it allows locally: %v", err)
	}

	ev, err := tl.Invoke(context.Background(), map[string]any{"id": "server-info"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := ev.Payload.(map[string]any)
	if out, _ := payload["output"].(string); !strings.Contains(out, "used_memory:1048576") {
		t.Errorf("evidence lost the command output: %+v", payload)
	}
	if payload["pod"] != "redis-0" {
		t.Errorf("evidence does not name the pod it came from: %+v", payload)
	}
}

// TestExecUsesNamedInstance proves the instance argument is honoured, which is
// what makes the refusal below meaningful rather than vacuous.
func TestExecUsesNamedInstance(t *testing.T) {
	s := newWSServer(t, stdout("ok"), successStatus())
	tl := execToolOf(t, execAdapter(t, s))

	call, err := tl.Plan(map[string]any{"id": "server-info", "instance": "redis-1"})
	if err != nil {
		t.Fatal(err)
	}
	if call.Exec.Pod != "redis-1" {
		t.Errorf("pod = %q, want redis-1", call.Exec.Pod)
	}
}

// TestExecRefusesPodOutsideTarget is FR-008. An agent that can name any pod can
// read any pod, so exec is bound to what the target resolved to.
func TestExecRefusesPodOutsideTarget(t *testing.T) {
	s := newWSServer(t, successStatus())
	tl := execToolOf(t, execAdapter(t, s))

	for _, name := range []string{"kube-apiserver", "vault-0", "redis-0-shadow", "../redis-0"} {
		_, err := tl.Plan(map[string]any{"id": "server-info", "instance": name})
		if code := errs.CodeOf(err); code != "MAS-4211" {
			t.Errorf("instance %q: got %v (%s), want MAS-4211", name, err, code)
		}
	}
}

// TestExecRefusesUnknownCommandID: the model may name only a command the pack
// declared, never an argument vector of its own.
func TestExecRefusesUnknownCommandID(t *testing.T) {
	s := newWSServer(t, successStatus())
	tl := execToolOf(t, execAdapter(t, s))

	_, err := tl.Plan(map[string]any{"id": "run-anything"})
	if code := errs.CodeOf(err); code != "MAS-8002" {
		t.Fatalf("got %v (%s), want MAS-8002", err, code)
	}
}

// TestExecWithNoResolvedInstancesIsCoded: before resolution there is no pod to
// address, and guessing one would be exactly the freedom this design removes.
func TestExecWithNoResolvedInstancesIsCoded(t *testing.T) {
	s := newWSServer(t, successStatus())
	a := execAdapter(t, s)
	a.SetInstances("middleware", nil)

	_, err := execToolOf(t, a).Plan(map[string]any{"id": "server-info"})
	if code := errs.CodeOf(err); code != "MAS-4211" {
		t.Fatalf("got %v (%s), want MAS-4211", err, code)
	}
}

// TestExecCanBeDisabledPerEnvironment is FR-009. A disabled environment must not
// register the tool at all: a capability that is absent cannot be called however
// a prompt is phrased.
func TestExecCanBeDisabledPerEnvironment(t *testing.T) {
	s := newWSServer(t, successStatus())
	a := execAdapter(t, s)
	a.SetExecEnabled(false)

	for _, tl := range a.Tools() {
		if tl.Name() == "kube.exec" {
			t.Fatal("exec is disabled but the tool is still registered")
		}
	}
	if ok, err := a.ExecAvailable(); ok || errs.CodeOf(err) != "MAS-4210" {
		t.Errorf("ExecAvailable() = %v, %v; want false with MAS-4210 so doctor can explain it", ok, err)
	}

	a.SetExecEnabled(true)
	if ok, err := a.ExecAvailable(); !ok || err != nil {
		t.Errorf("ExecAvailable() = %v, %v; want true", ok, err)
	}
}

// TestExecRequiresOnlineMode is FR-010: exec reads the live environment, so it
// is unavailable in an offline run like every other live tool.
func TestExecRequiresOnlineMode(t *testing.T) {
	s := newWSServer(t, successStatus())
	tl := execToolOf(t, execAdapter(t, s))

	gated, ok := tl.(interface{ RequiredMode() core.Mode })
	if !ok {
		t.Fatal("kube.exec does not declare a required mode, so an offline run could call it")
	}
	if got := gated.RequiredMode(); got != core.ModeOnline {
		t.Errorf("RequiredMode() = %q, want %q", got, core.ModeOnline)
	}
}

// TestEveryPackInspectCommandSurvivesContainerSubstitution is the check that
// would have caught the bug the tool tests above found. A pack's template is
// written for a client on a host; run inside the container it loses the port,
// and an argument vector that loses a value while keeping its flag makes the
// *next* argument the value — turning a vetted command into one the guard
// refuses, for reasons that look like a bad allow-list.
func TestEveryPackInspectCommandSurvivesContainerSubstitution(t *testing.T) {
	lib, err := knowledge.LoadDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	for _, pack := range lib.All() {
		for _, in := range pack.InspectCommands() {
			t.Run(pack.Metadata.Middleware+"/"+in.ID, func(t *testing.T) {
				args := substituteInContainer(in.Args)
				for _, a := range args {
					if strings.Contains(a, "{{") {
						t.Errorf("argument %q still carries a template", a)
					}
				}
				err := g.Authorize(ctx, safety.Call{
					Tool: "kube.exec", Class: safety.ClassReadOnly,
					Exec: &safety.ExecEffect{
						Namespace: "middleware", Pod: "instance-0", Container: "app",
						Binary: in.Binary, Args: args,
					},
				})
				if err != nil {
					t.Errorf("a command the pack declares is refused when run in-container: %v\n  %s %s",
						err, in.Binary, strings.Join(args, " "))
				}
			})
		}
	}
}

// TestExecOutputIsRedacted is NFR-004. A middleware's own output can contain a
// credential — a replication link, a connection string in a config dump — and
// the exec path must be no more trusting of it than any other evidence source.
func TestExecOutputIsRedacted(t *testing.T) {
	s := newWSServer(t,
		stdout("master_host:10.0.0.9\r\nmasterauth:hunter2\r\n"),
		stdout("replica_link:redis://admin:s3cr3t@10.0.0.9:6379\r\n"),
		successStatus(),
	)
	a := execAdapter(t, s)

	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	reg.MustRegister(execToolOf(t, a))
	inv, err := tool.NewInvoker(reg, tool.InvokerOptions{
		Guard:    g,
		Mode:     core.ModeOnline,
		Timeout:  5 * time.Second,
		Redactor: safety.NewRedactor(nil, nil),
	})
	if err != nil {
		t.Fatal(err)
	}

	ev, gap := inv.Invoke(context.Background(), "kube.exec", map[string]any{"id": "server-info"})
	if gap != nil {
		t.Fatalf("the command failed: %+v", gap)
	}
	rendered := fmt.Sprintf("%v", ev.Payload)
	for _, secret := range []string{"s3cr3t", "admin:s3cr3t"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("a credential from the container's own output reached the evidence: %q", rendered)
		}
	}
	if !strings.Contains(rendered, "master_host:10.0.0.9") {
		t.Errorf("redaction removed the diagnostic content too: %q", rendered)
	}
}

// TestExecStepRecordsCommandPodAndExit is NFR-003: an operator reading the run
// record must be able to see exactly what was run, where, and what it returned.
func TestExecStepRecordsCommandPodAndExit(t *testing.T) {
	s := newWSServer(t, stdout("# Server\r\nredis_version:7.2.4\r\n"), exitStatus(3))
	a := execAdapter(t, s)

	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	reg.MustRegister(execToolOf(t, a))

	steps := &stepCollector{}
	inv, err := tool.NewInvoker(reg, tool.InvokerOptions{
		Guard: g, Mode: core.ModeOnline, Timeout: 5 * time.Second, Sink: steps,
	})
	if err != nil {
		t.Fatal(err)
	}

	ev, gap := inv.Invoke(context.Background(), "kube.exec", map[string]any{"id": "server-info"})
	if gap != nil {
		t.Fatalf("a non-zero exit must still produce evidence: %+v", gap)
	}
	payload, _ := ev.Payload.(map[string]any)
	if payload["exit_code"] != 3 {
		t.Errorf("exit_code = %v, want 3", payload["exit_code"])
	}
	if payload["pod"] != "redis-0" || payload["namespace"] != "middleware" {
		t.Errorf("the evidence does not say where it came from: %+v", payload)
	}
	if !strings.Contains(ev.Query, "redis-cli") || !strings.Contains(ev.Query, "redis-0") {
		t.Errorf("query = %q; it must name the command and the pod", ev.Query)
	}

	if len(steps.steps) == 0 {
		t.Fatal("the exec was not recorded as a step")
	}
	if steps.steps[0].Name != "kube.exec" {
		t.Errorf("step names %q", steps.steps[0].Name)
	}
}

type stepCollector struct{ steps []core.Step }

func (c *stepCollector) AppendStep(_ context.Context, s core.Step) { c.steps = append(c.steps, s) }
