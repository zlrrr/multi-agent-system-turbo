package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// gitStub stands in for a git client and a network, so the fallback chain can be
// exercised deterministically without either.
type gitStub struct {
	calls      [][]string
	failRemote bool
	missing    bool
	populate   bool
}

func (g *gitStub) LookPath(binary string) (string, error) {
	if g.missing {
		return "", errs.New("MAS-4302", binary)
	}
	return "/usr/bin/" + binary, nil
}

func (g *gitStub) Run(_ context.Context, binary string, args []string) (string, error) {
	g.calls = append(g.calls, append([]string{binary}, args...))
	repo, dest := "", ""
	if len(args) >= 2 {
		repo, dest = args[len(args)-2], args[len(args)-1]
	}
	if g.failRemote && strings.HasPrefix(repo, "https://") {
		return "", errs.New("MAS-4402", "network", "dial tcp: network is unreachable")
	}
	if g.populate && dest != "" {
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return "", err
		}
		body := "int main() {\n  serverLog(LL_WARNING, \"OOM command not allowed when used memory > 'maxmemory'\");\n  return 0;\n}\n"
		if err := os.WriteFile(filepath.Join(dest, "server.c"), []byte(body), 0o600); err != nil {
			return "", err
		}
	}
	return "", nil
}

func newFetcher(t *testing.T, g *gitStub, mutate func(*config.SourceConfig)) *Fetcher {
	t.Helper()
	cfg := config.SourceConfig{
		Enabled: true, CacheDir: t.TempDir(),
		NetworkTimeout: config.Duration(time.Second), CacheTTL: config.Duration(time.Hour),
		Repos:   map[string]string{"redis": "https://github.com/redis/redis"},
		Mirrors: map[string]string{"redis": "/srv/mirrors/redis.git"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg, g)
}

func TestFetchFromNetwork(t *testing.T) {
	g := &gitStub{populate: true}
	f := newFetcher(t, g, nil)

	got, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4")
	if gap != nil {
		t.Fatalf("unexpected gap: %+v", gap)
	}
	if got.Origin != OriginNetwork || got.Fallback {
		t.Fatalf("fetched = %+v", got)
	}
	if got.Ref != "v7.2.4" {
		t.Fatalf("ref = %q; upstream tags are v-prefixed", got.Ref)
	}
	last := g.calls[len(g.calls)-1]
	if !containsAll(last, "clone", "--depth", "1", "--branch", "v7.2.4") {
		t.Fatalf("clone command = %v", last)
	}
}

// TestFallbackOnUnreachable is the G6.3 test: a network partition must produce a
// fallback and a recorded gap, never a failed run.
func TestFallbackOnUnreachable(t *testing.T) {
	g := &gitStub{failRemote: true, populate: true}
	f := newFetcher(t, g, nil)

	got, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4")
	if got.Path == "" {
		t.Fatal("fallback did not produce a usable source tree")
	}
	if got.Origin != OriginMirror || !got.Fallback {
		t.Fatalf("fetched = %+v, want the local mirror with Fallback set", got)
	}
	if gap == nil || gap.Code != "MAS-4401" {
		t.Fatalf("gap = %+v, want MAS-4401 so the report can state the fallback", gap)
	}
	if !strings.Contains(got.Notes, "unreachable") {
		t.Fatalf("notes should record why the network was abandoned: %q", got.Notes)
	}
	if gap.Impact == "" {
		t.Error("the gap must state what the fallback costs the conclusion")
	}
}

func TestNoMirrorGap(t *testing.T) {
	g := &gitStub{failRemote: true}
	f := newFetcher(t, g, func(c *config.SourceConfig) { c.Mirrors = map[string]string{} })

	got, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4")
	if got.Path != "" {
		t.Fatalf("no source should have been produced: %+v", got)
	}
	if gap == nil || gap.Code != "MAS-4402" {
		t.Fatalf("gap = %+v, want MAS-4402", gap)
	}
}

func TestMirrorOnlyConfiguration(t *testing.T) {
	g := &gitStub{populate: true}
	f := newFetcher(t, g, func(c *config.SourceConfig) { c.Repos = map[string]string{} })

	got, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4")
	if gap != nil {
		t.Fatalf("an air-gapped configuration should just work: %+v", gap)
	}
	if got.Origin != OriginMirror || got.Fallback {
		t.Fatalf("fetched = %+v; a mirror-only setup is not a fallback", got)
	}
	for _, call := range g.calls {
		for _, a := range call {
			if strings.HasPrefix(a, "https://") {
				t.Fatalf("the network was contacted despite no repo being configured: %v", call)
			}
		}
	}
}

func TestCacheHitSkipsNetwork(t *testing.T) {
	g := &gitStub{populate: true}
	f := newFetcher(t, g, nil)

	if _, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4"); gap != nil {
		t.Fatal(gap)
	}
	callsAfterFirst := len(g.calls)

	got, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4")
	if gap != nil {
		t.Fatal(gap)
	}
	if got.Origin != OriginCache {
		t.Fatalf("origin = %s, want cache", got.Origin)
	}
	if len(g.calls) != callsAfterFirst {
		t.Fatalf("a cache hit still ran git: %v", g.calls[callsAfterFirst:])
	}
}

func TestStaleCacheIsRefetched(t *testing.T) {
	g := &gitStub{populate: true}
	f := newFetcher(t, g, func(c *config.SourceConfig) { c.CacheTTL = config.Duration(time.Minute) })
	if _, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4"); gap != nil {
		t.Fatal(gap)
	}
	before := len(g.calls)
	f.now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	got, gap := f.Fetch(context.Background(), core.KindRedis, "7.2.4")
	if gap != nil {
		t.Fatal(gap)
	}
	if got.Origin == OriginCache {
		t.Fatal("a stale cache entry was reused")
	}
	if len(g.calls) == before {
		t.Fatal("a stale cache entry did not trigger a refetch")
	}
}

func TestUnknownRefFallsBackToDefaultBranch(t *testing.T) {
	// The first clone (with --branch) fails; the retry without it succeeds.
	g := &gitStub{populate: true}
	failing := &refFailingStub{inner: g}
	f := New(config.SourceConfig{
		Enabled: true, CacheDir: t.TempDir(),
		NetworkTimeout: config.Duration(time.Second),
		Repos:          map[string]string{"redis": "https://github.com/redis/redis"},
	}, failing)

	got, gap := f.Fetch(context.Background(), core.KindRedis, "99.0.0")
	if gap != nil {
		t.Fatalf("a missing tag should degrade to the default branch: %+v", gap)
	}
	if got.Origin != OriginNetwork {
		t.Fatalf("fetched = %+v", got)
	}
}

type refFailingStub struct{ inner *gitStub }

func (r *refFailingStub) LookPath(b string) (string, error) { return r.inner.LookPath(b) }
func (r *refFailingStub) Run(ctx context.Context, b string, args []string) (string, error) {
	for _, a := range args {
		if a == "--branch" {
			r.inner.calls = append(r.inner.calls, append([]string{b}, args...))
			return "", errs.New("MAS-4403", "v99.0.0", "redis")
		}
	}
	return r.inner.Run(ctx, b, args)
}

func TestDisabledAndMissingGitAreCodedGaps(t *testing.T) {
	disabled := newFetcher(t, &gitStub{}, func(c *config.SourceConfig) { c.Enabled = false })
	if _, gap := disabled.Fetch(context.Background(), core.KindRedis, ""); gap == nil || gap.Code != "MAS-4402" {
		t.Fatalf("disabled: got %+v, want MAS-4402", gap)
	}

	noGit := newFetcher(t, &gitStub{missing: true}, nil)
	if _, gap := noGit.Fetch(context.Background(), core.KindRedis, ""); gap == nil || gap.Code != "MAS-4405" {
		t.Fatalf("missing git: got %+v, want MAS-4405", gap)
	}
}

func TestUnconfiguredMiddlewareIsCodedGap(t *testing.T) {
	f := newFetcher(t, &gitStub{}, nil)
	if _, gap := f.Fetch(context.Background(), core.KindMilvus, ""); gap == nil || gap.Code != "MAS-4402" {
		t.Fatalf("got %+v, want MAS-4402", gap)
	}
}

// ── search ──────────────────────────────────────────────────────────────────

func fixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"server.c": `void freeMemoryIfNeeded(void) {
    if (mem_used > server.maxmemory) {
        serverLog(LL_WARNING, "OOM command not allowed when used memory > 'maxmemory'");
        return C_ERR;
    }
}
`,
		"deps/hiredis/read.c": "/* vendored dependency */\nint oom_marker;\n",
		"src/replication.c":   "void replicationFeedSlaves(void) {\n    serverLog(LL_NOTICE, \"MASTER <-> REPLICA sync started\");\n}\n",
		"README.md":           "# Redis\nOOM behaviour is documented here.\n",
		".git/config":         "[core]\n  oom = true\n",
		"binary.bin":          "\x00\x01OOM\x02",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSearchFixture(t *testing.T) {
	root := fixtureTree(t)
	matches, err := Search(root, `OOM command not allowed`, DefaultSearchOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %+v", matches)
	}
	m := matches[0]
	if m.File != "server.c" || m.Line != 3 {
		t.Fatalf("location wrong: %+v", m)
	}
	if len(m.Before) == 0 || !strings.Contains(strings.Join(m.Before, "\n"), "maxmemory") {
		t.Fatalf("leading context missing: %+v", m.Before)
	}
	if len(m.After) == 0 || !strings.Contains(strings.Join(m.After, "\n"), "C_ERR") {
		t.Fatalf("trailing context missing: %+v", m.After)
	}
}

func TestSearchSkipsGitAndBinaries(t *testing.T) {
	root := fixtureTree(t)
	matches, err := Search(root, `oom`, DefaultSearchOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if strings.Contains(m.File, ".git") {
			t.Errorf(".git was searched: %+v", m)
		}
		if strings.HasSuffix(m.File, ".bin") {
			t.Errorf("a binary file was searched: %+v", m)
		}
	}
	if len(matches) == 0 {
		t.Fatal("case-insensitive search found nothing")
	}
}

func TestSearchCaseSensitivity(t *testing.T) {
	root := fixtureTree(t)
	opts := DefaultSearchOptions()
	opts.CaseSensitive = true
	sensitive, err := Search(root, `oom_marker`, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(sensitive) != 1 {
		t.Fatalf("case-sensitive search: %+v", sensitive)
	}
	if got, _ := Search(root, `OOM_MARKER`, opts); len(got) != 0 {
		t.Fatalf("case-sensitive search matched the wrong case: %+v", got)
	}
}

func TestSearchRespectsMaxMatches(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 0; i < 200; i++ {
		body.WriteString("needle here\n")
	}
	if err := os.WriteFile(filepath.Join(root, "big.c"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := DefaultSearchOptions()
	opts.MaxMatches = 5
	matches, err := Search(root, "needle", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > 5 {
		t.Fatalf("returned %d matches above the ceiling of 5", len(matches))
	}
}

func TestSearchInvalidPatternIsCoded(t *testing.T) {
	root := fixtureTree(t)
	if _, err := Search(root, "([unclosed", DefaultSearchOptions()); errs.CodeOf(err) != "MAS-4404" {
		t.Fatalf("got %v, want MAS-4404", err)
	}
}

func TestSearchMissingTreeIsCoded(t *testing.T) {
	if _, err := Search(filepath.Join(t.TempDir(), "absent"), "x", DefaultSearchOptions()); errs.CodeOf(err) != "MAS-4402" {
		t.Fatalf("got %v, want MAS-4402", err)
	}
}

func TestSearchRefusesTraversal(t *testing.T) {
	if _, err := Search("/var/lib/../../etc", "x", DefaultSearchOptions()); errs.CodeOf(err) != "MAS-4404" {
		t.Fatalf("got %v, want MAS-4404", err)
	}
}

func TestRefFor(t *testing.T) {
	for in, want := range map[string]string{
		"7.2.4": "v7.2.4", "v7.2.4": "v7.2.4", "": "", "  3.6.0 ": "v3.6.0",
	} {
		if got := refFor(in); got != want {
			t.Errorf("refFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitiseRefIsFilesystemSafe(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "release/7.2", "", "a b"} {
		got := sanitiseRef(in)
		if strings.ContainsAny(got, `/\ `) || strings.Contains(got, "..") {
			t.Errorf("sanitiseRef(%q) = %q, which is not filesystem-safe", in, got)
		}
	}
}

func containsAll(hay []string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for _, h := range hay {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
