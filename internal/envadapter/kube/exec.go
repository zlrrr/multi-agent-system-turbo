package kube

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// The remote-command subprotocol multiplexes streams onto one WebSocket: the
// first byte of each binary message is the channel.
const (
	channelStdin  = 0
	channelStdout = 1
	channelStderr = 2
	channelStatus = 3

	remoteCommandProtocol = "v4.channel.k8s.io"
)

// ExecClient runs one read-only command inside one container.
//
// It is a separate type from Client, and that separation is the design rather
// than an accident of packaging. Client's invariant — "no method can express
// mutation", checked by name — is worth keeping literally intact, so execution
// does not join it. ExecClient earns an invariant of the same mechanical kind:
// **one exported method, no path argument**. The URL is assembled here from a
// namespace, a pod and a container, so no caller input can point it at another
// endpoint (design-lld.md §4; asserted by TestExecClientAddressesOneEndpoint).
type ExecClient struct {
	creds     credentials
	tlsConfig *tls.Config
	dial      dialer
}

// NewExecClient builds an exec client from the same credentials the read client
// resolved, so a run cannot execute anywhere it could not already read.
func NewExecClient(c *Client) *ExecClient {
	e := &ExecClient{creds: c.creds}
	if tr, ok := c.hc.Transport.(*http.Transport); ok {
		e.tlsConfig = tr.TLSClientConfig
	}
	e.dial = e.dialTLS
	return e
}

// ExecRequest is one command in one container.
//
// Command is an argument vector and never a string: there is nothing for a
// shell to interpret even if one were reachable, which is the same rule the
// local runner follows.
type ExecRequest struct {
	Namespace string
	Pod       string
	Container string
	Command   []string
	MaxBytes  int
}

// ExecResult is what the command produced.
//
// ExitKnown is separate from ExitCode on purpose. A stream that ends without a
// status frame leaves the outcome unknown, and reporting unknown as success is
// exactly the dishonesty this project exists to avoid.
type ExecResult struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	ExitKnown bool
	Truncated bool
}

// Run executes the command and returns what it wrote.
//
// This is the only exported method, and it takes no path: see the type comment.
func (e *ExecClient) Run(ctx context.Context, req ExecRequest) (ExecResult, error) {
	target := e.creds.server + "/api/v1/namespaces/" + req.Namespace +
		"/pods/" + req.Pod + "/exec?" + execQuery(req).Encode()

	// The same credentials the read client uses, applied the same way: a run
	// must never be able to execute somewhere it could not already read.
	header := http.Header{}
	switch {
	case e.creds.bearerToken != "":
		header.Set("Authorization", "Bearer "+e.creds.bearerToken)
	case e.creds.username != "":
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
			[]byte(e.creds.username+":"+e.creds.password)))
	}

	conn, err := dialWebSocket(ctx, e.dial, target, header, remoteCommandProtocol)
	if err != nil {
		return ExecResult{}, err
	}
	defer func() { _ = conn.Close() }()

	return readRemoteCommand(conn, req)
}

// execQuery builds the subresource's parameters. stdin and tty are absent, and
// that absence is load-bearing: either would turn a one-shot read into a session
// a prompt could steer (design-hld.md §4).
func execQuery(req ExecRequest) url.Values {
	q := url.Values{}
	for _, arg := range req.Command {
		q.Add("command", arg)
	}
	if req.Container != "" {
		q.Set("container", req.Container)
	}
	q.Set("stdout", "true")
	q.Set("stderr", "true")
	q.Set("stdin", "false")
	q.Set("tty", "false")
	return q
}

// readRemoteCommand demultiplexes the channels until the server closes.
func readRemoteCommand(conn *wsConn, req ExecRequest) (ExecResult, error) {
	var (
		out, errOut strings.Builder
		res         ExecResult
		budget      = req.MaxBytes
	)
	where := req.Namespace + "/" + req.Pod

	for {
		opcode, payload, err := conn.ReadMessage()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, err
		}
		if opcode != opBinary || len(payload) == 0 {
			// The apiserver sends binary frames only. A text or empty frame
			// means something in the path is not speaking this protocol.
			return res, errs.New("MAS-4213", where, "a non-binary frame arrived on a binary channel")
		}

		channel, data := payload[0], payload[1:]
		switch channel {
		case channelStdout, channelStderr:
			sink := &out
			if channel == channelStderr {
				sink = &errOut
			}
			if budget > 0 {
				remaining := budget - (out.Len() + errOut.Len())
				if remaining <= 0 {
					res.Truncated = true
					continue
				}
				if len(data) > remaining {
					data, res.Truncated = data[:remaining], true
				}
			}
			sink.Write(data)
		case channelStatus:
			code, err := exitCodeFrom(data, where)
			if err != nil {
				return res, err
			}
			res.ExitCode, res.ExitKnown = code, true
		case channelStdin:
			return res, errs.New("MAS-4213", where, "the server wrote to the stdin channel")
		default:
			return res, errs.New("MAS-4213", where,
				fmt.Sprintf("unknown stream channel %d", channel))
		}
	}

	res.Stdout, res.Stderr = out.String(), errOut.String()
	if !res.ExitKnown {
		return res, errs.New("MAS-4214", where)
	}
	return res, nil
}

// remoteStatus is the subset of metav1.Status the exit code lives in.
type remoteStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
	Details struct {
		Causes []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"causes"`
	} `json:"details"`
}

// exitCodeFrom reads the exit code out of the status frame. "Success" means 0;
// otherwise the code arrives as a cause whose reason is ExitCode.
func exitCodeFrom(data []byte, where string) (int, error) {
	var status remoteStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return 0, errs.Wrap(err, "MAS-4213", where, "the status frame was not valid JSON")
	}
	if strings.EqualFold(status.Status, "Success") {
		return 0, nil
	}
	for _, cause := range status.Details.Causes {
		if !strings.EqualFold(cause.Reason, "ExitCode") {
			continue
		}
		code, err := strconv.Atoi(strings.TrimSpace(cause.Message))
		if err != nil {
			return 0, errs.Wrap(err, "MAS-4213", where,
				"the exit code was not a number: "+cause.Message)
		}
		return code, nil
	}
	// A failure with no exit code — the container could not start the binary,
	// say. Reporting 0 would read as success, so this is a non-zero unknown.
	return 1, nil
}

// dialTLS opens the connection the handshake runs over, with the same TLS
// settings the read client uses.
func (e *ExecClient) dialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{}
	if e.tlsConfig == nil {
		return d.DialContext(ctx, network, addr)
	}
	return (&tls.Dialer{NetDialer: d, Config: e.tlsConfig}).DialContext(ctx, network, addr)
}
