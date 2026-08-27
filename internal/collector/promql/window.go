package promql

import (
	"context"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// windowKey carries the run's default time window into tool invocations, so a
// model need not restate it on every call and cannot accidentally widen it.
type windowKey struct{}

// WithWindow attaches the run's window to a context.
func WithWindow(ctx context.Context, w core.Window) context.Context {
	return context.WithValue(ctx, windowKey{}, w)
}

// WindowFromContext reads the attached window.
func WindowFromContext(ctx context.Context) (core.Window, bool) {
	w, ok := ctx.Value(windowKey{}).(core.Window)
	return w, ok
}

func windowFrom(ctx context.Context, args map[string]any) (core.Window, error) {
	w, ok := WindowFromContext(ctx)
	if !ok {
		now := time.Now().UTC()
		w = core.Window{From: now.Add(-time.Hour), To: now}
	}
	if s := tool.Str(args, "from", ""); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return w, errs.New("MAS-1010", "from must be RFC3339, got "+s)
		}
		w.From = t
	}
	if s := tool.Str(args, "to", ""); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return w, errs.New("MAS-1010", "to must be RFC3339, got "+s)
		}
		w.To = t
	}
	if err := w.Validate(); err != nil {
		return w, err
	}
	return w, nil
}

func parseInstant(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errs.New("MAS-1010", "at must be RFC3339, got "+s)
	}
	return t, nil
}

func errsInvalidStep(s string) error {
	return errs.New("MAS-1010", "step must be a Go duration such as 30s, got "+s)
}
