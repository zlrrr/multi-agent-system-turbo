package kube

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6455 specifies SHA-1 for the handshake accept value; it is not used for security
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// This file is a minimal RFC 6455 client: enough to read the frames a
// Kubernetes apiserver sends for a remote command, and nothing more.
//
// It exists rather than a dependency for two reasons. The obvious one is
// Constitution Art. VII.4. The one that actually decided it is that the whole
// need here is "open one short-lived connection, read frames until close" —
// this project never sends a frame after the handshake, never negotiates
// extensions, and never keeps a connection alive. A general-purpose library
// would bring a great deal of surface for a use that fits on two screens.
//
// Governs: specs/004-kube-exec/design-lld.md §3

// wsGUID is the constant RFC 6455 specifies for deriving the accept value.
const wsGUID = "258EAFA5-E914-47DA-95CA-5AB0DC85B11D"

// WebSocket opcodes, from RFC 6455 §5.2.
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// wsConn reads WebSocket messages from a server. It is read-only after the
// handshake apart from the pong a well-behaved client owes a ping.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
}

// dialer opens the raw connection the handshake runs over. It is a field rather
// than a call so a test can supply a plain TCP dialler where production uses
// TLS, without either side pretending to be the other.
type dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// dialWebSocket performs the handshake and returns a reader positioned at the
// first frame.
//
// The accept value is verified. Skipping that check is the classic way a
// WebSocket client "works" against a proxy that never understood the upgrade at
// all: the proxy returns something plausible, the client reads garbage as
// frames, and the failure surfaces much later as malformed data.
func dialWebSocket(ctx context.Context, raw dialer, target string, header http.Header,
	subprotocol string) (*wsConn, error) {

	u, err := url.Parse(target)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4212", target, err.Error())
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" || u.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	conn, err := raw(ctx, "tcp", host)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4212", target, err.Error())
	}
	if deadline, ok := ctx.Deadline(); ok {
		// The deadline goes on the connection, not only on the read loop: a
		// hung apiserver must not outlive the run's timeout (RSK-001).
		_ = conn.SetDeadline(deadline)
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		_ = conn.Close()
		return nil, errs.Wrap(err, "MAS-4212", target, "could not generate a handshake key")
	}
	encodedKey := base64.StdEncoding.EncodeToString(key)

	requestPath := u.RequestURI()
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", requestPath)
	fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Version: 13\r\n")
	fmt.Fprintf(&b, "Sec-WebSocket-Key: %s\r\n", encodedKey)
	if subprotocol != "" {
		fmt.Fprintf(&b, "Sec-WebSocket-Protocol: %s\r\n", subprotocol)
	}
	for name, values := range header {
		for _, v := range values {
			fmt.Fprintf(&b, "%s: %s\r\n", name, v)
		}
	}
	b.WriteString("\r\n")

	if _, err := io.WriteString(conn, b.String()); err != nil {
		_ = conn.Close()
		return nil, errs.Wrap(err, "MAS-4212", target, err.Error())
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		return nil, errs.Wrap(err, "MAS-4212", target, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		detail := resp.Status
		if body, rerr := io.ReadAll(io.LimitReader(resp.Body, 2048)); rerr == nil && len(body) > 0 {
			detail += ": " + strings.TrimSpace(string(body))
		}
		return nil, errs.New("MAS-4212", target, detail)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		_ = conn.Close()
		return nil, errs.New("MAS-4212", target, "the server did not upgrade the connection")
	}
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), acceptValue(encodedKey); got != want {
		_ = conn.Close()
		return nil, errs.New("MAS-4212", target,
			"the handshake accept value did not verify; something between here and the "+
				"apiserver answered without understanding the upgrade")
	}

	return &wsConn{conn: conn, br: br}, nil
}

func acceptValue(key string) string {
	h := sha1.New() //nolint:gosec // RFC 6455 mandates SHA-1 here; it is a handshake echo, not a security primitive
	_, _ = io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ReadMessage returns the next complete application message, reassembling
// continuation frames and answering pings along the way. io.EOF is returned when
// the server closes.
func (c *wsConn) ReadMessage() (opcode byte, payload []byte, err error) {
	var (
		assembled []byte
		msgOp     byte
		started   bool
	)
	for {
		op, data, fin, ferr := c.readFrame()
		if ferr != nil {
			return 0, nil, ferr
		}
		switch op {
		case opPing:
			if err := c.writeFrame(opPong, data); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			return 0, nil, io.EOF
		case opText, opBinary:
			if started {
				return 0, nil, errs.New("MAS-4213", c.conn.RemoteAddr().String(),
					"a new message began before the previous one was finished")
			}
			started, msgOp, assembled = true, op, append(assembled, data...)
		case opContinuation:
			if !started {
				return 0, nil, errs.New("MAS-4213", c.conn.RemoteAddr().String(),
					"a continuation frame arrived with no message to continue")
			}
			assembled = append(assembled, data...)
		default:
			return 0, nil, errs.New("MAS-4213", c.conn.RemoteAddr().String(),
				fmt.Sprintf("unknown opcode 0x%X", op))
		}
		if fin && started {
			return msgOp, assembled, nil
		}
	}
}

// readFrame reads one frame. Server-to-client frames are never masked, so a
// masked frame is a protocol violation rather than something to unmask.
func (c *wsConn) readFrame() (opcode byte, payload []byte, fin bool, err error) {
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, nil, false, io.EOF
		}
		return 0, nil, false, errs.Wrap(err, "MAS-4213", c.remote(), err.Error())
	}

	fin = head[0]&0x80 != 0
	if head[0]&0x70 != 0 {
		return 0, nil, false, errs.New("MAS-4213", c.remote(),
			"reserved bits are set; no extension was negotiated")
	}
	opcode = head[0] & 0x0F

	masked := head[1]&0x80 != 0
	if masked {
		return 0, nil, false, errs.New("MAS-4213", c.remote(),
			"the server masked a frame, which RFC 6455 forbids")
	}

	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, false, errs.Wrap(err, "MAS-4213", c.remote(), err.Error())
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, false, errs.Wrap(err, "MAS-4213", c.remote(), err.Error())
		}
		length = binary.BigEndian.Uint64(ext[:])
	}

	// A frame larger than any legitimate remote-command payload is refused
	// rather than allocated: the length field is attacker-influenced.
	const maxFrame = 8 << 20
	if length > maxFrame {
		return 0, nil, false, errs.New("MAS-4213", c.remote(),
			fmt.Sprintf("frame of %d bytes exceeds the %d-byte limit", length, maxFrame))
	}

	payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return 0, nil, false, errs.Wrap(err, "MAS-4213", c.remote(), err.Error())
	}
	return opcode, payload, fin, nil
}

// writeFrame sends a masked client frame. The only frames this client sends are
// pongs and a close, both tiny.
func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return errs.Wrap(err, "MAS-4213", c.remote(), "could not generate a frame mask")
	}

	header := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n <= 125:
		header = append(header, byte(0x80|n))
	case n <= 0xFFFF:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		return errs.New("MAS-4213", c.remote(), "refusing to send an oversized frame")
	}
	header = append(header, mask[:]...)

	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if _, err := c.conn.Write(append(header, masked...)); err != nil {
		return errs.Wrap(err, "MAS-4213", c.remote(), err.Error())
	}
	return nil
}

func (c *wsConn) remote() string {
	if c.conn == nil || c.conn.RemoteAddr() == nil {
		return "the apiserver"
	}
	return c.conn.RemoteAddr().String()
}

// Close sends a close frame and shuts the connection down. A failure to send is
// ignored: the connection is going away either way.
func (c *wsConn) Close() error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = c.writeFrame(opClose, []byte{0x03, 0xE8}) // 1000 normal closure
	return c.conn.Close()
}
