// Package local inspects a host on which middleware runs outside Kubernetes.
//
// Every command it runs is built as an argument vector and passed to the safety
// guard first; no shell is ever involved, so argument content cannot become
// executable syntax (design-hld.md §7.3.2).
//
// Governs: specs/001-mvp-core/design-lld.md §2.9
package local

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Runner executes an allow-listed command. The interface is the seam tests
// substitute so no test needs a real host process.
type Runner interface {
	Run(ctx context.Context, binary string, args []string) (stdout string, err error)
	LookPath(binary string) (string, error)
}

// ExecRunner runs commands with os/exec, never through a shell.
type ExecRunner struct {
	MaxOutputBytes int
}

// Run executes the command and returns its combined output.
func (r ExecRunner) Run(ctx context.Context, binary string, args []string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", errs.Wrap(err, "MAS-4302", binary)
	}
	// #nosec G204 — binary and args have already passed the guard's
	// deny-by-default allow-list, and no shell interprets them.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = minimalEnv()
	out, err := cmd.CombinedOutput()
	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if err != nil {
		return string(out), errs.Wrap(err, "MAS-4301", binary, strings.TrimSpace(truncate(string(out), 200)))
	}
	return string(out), nil
}

// LookPath reports whether a binary is available.
func (r ExecRunner) LookPath(binary string) (string, error) {
	p, err := exec.LookPath(binary)
	if err != nil {
		return "", errs.Wrap(err, "MAS-4302", binary)
	}
	return p, nil
}

// minimalEnv gives child processes only what they need to run, so nothing in
// this process's environment — including credentials — is inherited.
func minimalEnv() []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
		"HOME=/tmp",
	}
}

// Supported reports whether host inspection is meaningful on this platform.
func Supported() bool { return runtime.GOOS == "linux" || runtime.GOOS == "darwin" }

// DefaultTimeout bounds a single host command.
const DefaultTimeout = 10 * time.Second

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
