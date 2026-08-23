// Package obs provides the tool's own observability: structured logging that is
// correlatable by run_id, credential redaction at the handler, and a small
// Prometheus exposition for the server surface.
//
// Governs: specs/001-mvp-core/design-lld.md §2.4, design-hld.md §7.2
package obs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
)

// redactingHandler wraps another slog.Handler and scrubs every message and
// attribute value on the way out. Placing redaction here rather than at each
// call site means a new log statement cannot leak by omission (HLD §7.3.5).
type redactingHandler struct {
	inner slog.Handler
	r     *safety.Redactor
}

func (h *redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h *redactingHandler) redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		grp := a.Value.Group()
		next := make([]slog.Attr, 0, len(grp))
		for _, sub := range grp {
			next = append(next, h.redactAttr(sub))
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(next...)}
	}
	if isSensitiveLogKey(a.Key) {
		return slog.String(a.Key, safety.Mask)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.r.Redact(a.Value.String()))
	case slog.KindAny:
		return slog.Any(a.Key, h.r.RedactAny(a.Value.Any()))
	default:
		return a
	}
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		next = append(next, h.redactAttr(a))
	}
	return &redactingHandler{inner: h.inner.WithAttrs(next), r: h.r}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name), r: h.r}
}

var sensitiveLogKeys = map[string]bool{
	"api_key": true, "apikey": true, "token": true, "password": true,
	"secret": true, "authorization": true, "credential": true, "cookie": true,
}

func isSensitiveLogKey(k string) bool { return sensitiveLogKeys[strings.ToLower(k)] }

// Setup builds the process logger. It never returns nil.
func Setup(cfg config.LogConfig, r *safety.Redactor, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	if r == nil {
		r = safety.NewRedactor(cfg.Redact, nil)
	}
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}
	var base slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		base = slog.NewTextHandler(w, opts)
	} else {
		base = slog.NewJSONHandler(w, opts)
	}
	return slog.New(&redactingHandler{inner: base, r: r})
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// RunContext carries everything a diagnostic run needs to be observable:
// its identity, its logger and the process metrics registry.
type RunContext struct {
	RunID   string
	Logger  *slog.Logger
	Metrics *Metrics

	stepSeq atomic.Int64
}

// NextStepID allocates a monotonically increasing step identifier within a run.
func (rc *RunContext) NextStepID() string {
	return "step-" + itoa(int(rc.stepSeq.Add(1)))
}

type ctxKey struct{}

// WithRun attaches a run context.
func WithRun(ctx context.Context, rc *RunContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, rc)
}

// FromContext returns the attached run context, or nil.
func FromContext(ctx context.Context) *RunContext {
	rc, _ := ctx.Value(ctxKey{}).(*RunContext)
	return rc
}

// RunID returns the current run identifier, or "" outside a run.
func RunID(ctx context.Context) string {
	if rc := FromContext(ctx); rc != nil {
		return rc.RunID
	}
	return ""
}

// fallback is used when logging happens outside a run context. It is discarded
// by default so library code can log unconditionally without polluting output.
var fallback = slog.New(slog.NewJSONHandler(io.Discard, nil))

// SetFallbackLogger installs the process-wide logger used outside a run.
func SetFallbackLogger(l *slog.Logger) {
	if l != nil {
		fallback = l
	}
}

// Log returns the logger for this context, already carrying run_id. It never
// returns nil, so callers never need a nil check (LLD §2.4 invariant).
func Log(ctx context.Context) *slog.Logger {
	if rc := FromContext(ctx); rc != nil && rc.Logger != nil {
		return rc.Logger.With("run_id", rc.RunID)
	}
	return fallback
}

// Metrics returns the metrics registry for this context, or the process default.
func MetricsOf(ctx context.Context) *Metrics {
	if rc := FromContext(ctx); rc != nil && rc.Metrics != nil {
		return rc.Metrics
	}
	return Default()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
