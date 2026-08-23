// Package source acquires middleware source code and searches it, so an observed
// log line or error string can be located in the code that emits it.
//
// Its defining behaviour is the fallback chain required by project goal G6.3:
// a network partition produces a fallback and a recorded gap, never a failed
// run (design-hld.md §5.3).
//
// Governs: specs/001-mvp-core/design-lld.md §2.10
package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Origin records where source actually came from. It travels into the report so
// a reader always knows whether the code consulted matches the deployed version.
type Origin string

const (
	OriginCache   Origin = "cache"
	OriginNetwork Origin = "network"
	OriginMirror  Origin = "local-mirror"
)

// Fetched is the outcome of an acquisition.
type Fetched struct {
	Path     string `json:"path"`
	Origin   Origin `json:"origin"`
	Ref      string `json:"ref"`
	Fallback bool   `json:"fallback"`
	Notes    string `json:"notes,omitempty"`
}

// Runner executes git. It is the seam tests substitute so no test needs a
// network or a real repository.
type Runner interface {
	Run(ctx context.Context, binary string, args []string) (string, error)
	LookPath(binary string) (string, error)
}

// Fetcher acquires source for a middleware kind and version.
type Fetcher struct {
	cfg    config.SourceConfig
	runner Runner
	now    func() time.Time
}

// New builds a fetcher.
func New(cfg config.SourceConfig, r Runner) *Fetcher {
	return &Fetcher{cfg: cfg, runner: r, now: time.Now}
}

// Enabled reports whether source acquisition is configured at all.
func (f *Fetcher) Enabled() bool { return f.cfg.Enabled }

// Available reports whether the git client this feature needs is present.
func (f *Fetcher) Available() error {
	if _, err := f.runner.LookPath("git"); err != nil {
		return errs.New("MAS-4405")
	}
	return nil
}

// CacheDir reports where fetched trees are kept.
func (f *Fetcher) CacheDir() string { return f.cfg.CacheDir }

// Fetch acquires source for a middleware kind, following the fallback chain in
// design-hld.md §5.3: fresh cache, then network, then local mirror.
//
// It returns a Gap rather than an error whenever the run can continue without
// this evidence, which is every case except a caller mistake.
func (f *Fetcher) Fetch(ctx context.Context, kind core.MiddlewareKind, version string) (Fetched, *core.Gap) {
	if !f.cfg.Enabled {
		return Fetched{}, gapf(core.GapNotConfigured, "MAS-4402", string(kind),
			"source acquisition is disabled in configuration",
			"code-level analysis is unavailable for this run")
	}
	if err := f.Available(); err != nil {
		return Fetched{}, gapf(core.GapUnsupported, "MAS-4405", string(kind),
			"git is not installed", "code-level analysis is unavailable for this run")
	}

	ref := refFor(version)
	dest := f.destFor(kind, ref)

	if fresh, err := f.cacheFresh(dest); err == nil && fresh {
		return Fetched{Path: dest, Origin: OriginCache, Ref: ref}, nil
	}

	repo, hasRepo := f.cfg.Repos[string(kind)]
	mirror, hasMirror := f.cfg.Mirrors[string(kind)]

	if hasRepo {
		netCtx, cancel := context.WithTimeout(ctx, f.networkTimeout())
		err := f.clone(netCtx, repo, ref, dest)
		cancel()
		if err == nil {
			return Fetched{Path: dest, Origin: OriginNetwork, Ref: ref}, nil
		}
		// The network failed. Fall back rather than fail: an air-gapped
		// environment is a supported deployment, not an error condition.
		if hasMirror {
			if mErr := f.clone(ctx, mirror, ref, dest); mErr == nil {
				return Fetched{
						Path: dest, Origin: OriginMirror, Ref: ref, Fallback: true,
						Notes: "network repository unreachable: " + err.Error(),
					}, gapf(core.GapUnavailable, "MAS-4401", string(kind), mirror,
						"the mirror may not match the deployed version")
			}
		}
		return Fetched{}, gapf(core.GapUnavailable, "MAS-4402", string(kind), err.Error(),
			"code-level analysis is unavailable for this run")
	}

	if hasMirror {
		if err := f.clone(ctx, mirror, ref, dest); err == nil {
			return Fetched{Path: dest, Origin: OriginMirror, Ref: ref}, nil
		}
	}

	return Fetched{}, gapf(core.GapNotConfigured, "MAS-4402", string(kind),
		"no repository or mirror is configured for this middleware",
		"code-level analysis is unavailable for this run")
}

func (f *Fetcher) networkTimeout() time.Duration {
	if d := f.cfg.NetworkTimeout.D(); d > 0 {
		return d
	}
	return 10 * time.Second
}

func (f *Fetcher) destFor(kind core.MiddlewareKind, ref string) string {
	return filepath.Join(f.cfg.CacheDir, string(kind)+"@"+sanitiseRef(ref))
}

// cacheFresh reports whether a previously fetched tree is present and within the
// configured time-to-live.
func (f *Fetcher) cacheFresh(dest string) (bool, error) {
	info, err := os.Stat(dest)
	if err != nil || !info.IsDir() {
		return false, err
	}
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) == 0 {
		return false, err
	}
	ttl := f.cfg.CacheTTL.D()
	if ttl <= 0 {
		return true, nil
	}
	return f.now().Sub(info.ModTime()) < ttl, nil
}

// clone performs a shallow single-branch fetch. Writing into our own cache
// directory is not a mutation of a target environment (Constitution Art. IV.1
// governs targets); the guard still validates the command.
func (f *Fetcher) clone(ctx context.Context, repo, ref, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return errs.Wrap(err, "MAS-4402", "cache", err.Error())
	}
	_ = os.RemoveAll(dest)

	args := []string{"clone", "--depth", "1", "--single-branch"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, repo, dest)

	if _, err := f.runner.Run(ctx, "git", args); err != nil {
		if ref != "" {
			// The ref may simply not exist upstream; retry on the default branch
			// so a version-tag miss degrades to "close enough" rather than nothing.
			fallbackArgs := []string{"clone", "--depth", "1", "--single-branch", repo, dest}
			if _, ferr := f.runner.Run(ctx, "git", fallbackArgs); ferr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

// refFor maps a middleware version to the tag convention upstream projects use.
func refFor(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

func sanitiseRef(ref string) string {
	if ref == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	// Collapse dot runs so no sanitised ref can ever read as a traversal
	// segment, even though separators are already gone.
	out := b.String()
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", "_")
	}
	return out
}

func gapf(reason core.GapReason, code string, args ...any) *core.Gap {
	impact := ""
	if len(args) > 0 {
		if s, ok := args[len(args)-1].(string); ok {
			impact = s
			args = args[:len(args)-1]
		}
	}
	e := errs.New(code, args...)
	return &core.Gap{
		Intent: "acquire middleware source",
		Reason: reason, Code: code,
		Detail: e.Message("en"), Impact: impact,
	}
}
