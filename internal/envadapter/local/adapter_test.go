package local

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// stubRunner substitutes for host processes so no test needs a real system.
type stubRunner struct {
	outputs map[string]string
	errs    map[string]error
	calls   [][]string
	missing map[string]bool
}

func (s *stubRunner) key(binary string, args []string) string {
	return binary + " " + strings.Join(args, " ")
}

func (s *stubRunner) Run(_ context.Context, binary string, args []string) (string, error) {
	s.calls = append(s.calls, append([]string{binary}, args...))
	k := s.key(binary, args)
	if err, ok := s.errs[k]; ok {
		return "", err
	}
	if err, ok := s.errs[binary]; ok {
		return "", err
	}
	if out, ok := s.outputs[k]; ok {
		return out, nil
	}
	if out, ok := s.outputs[binary]; ok {
		return out, nil
	}
	return "", errs.New("MAS-4302", binary)
}

func (s *stubRunner) LookPath(binary string) (string, error) {
	if s.missing[binary] {
		return "", errs.New("MAS-4302", binary)
	}
	return "/usr/bin/" + binary, nil
}

const psOutput = `    PID  %CPU %MEM   RSS COMMAND         COMMAND
      1   0.0  0.1  1024 systemd         /sbin/init
    412   3.5 45.2 921600 redis-server    redis-server *:6379
    900   0.1  0.3  8192 sshd            /usr/sbin/sshd -D
`

const ssOutput = `Netid State  Recv-Q Send-Q Local-Address:Port Peer-Address:Port Process
tcp   LISTEN 0      511    0.0.0.0:6379       0.0.0.0:*         users:(("redis-server",pid=412,fd=6))
tcp   LISTEN 0      128    0.0.0.0:22         0.0.0.0:*         users:(("sshd",pid=900,fd=3))
`

func newTestAdapter(t *testing.T) (*Adapter, *stubRunner) {
	t.Helper()
	r := &stubRunner{outputs: map[string]string{
		"ps":                                 psOutput,
		"ss":                                 ssOutput,
		"free":                               "              total        used        free\nMem:           2000        1900          80\n",
		"df":                                 "Filesystem  Size  Used Avail Use% Mounted on\n/dev/sda1    50G   47G  1.0G  98% /\n",
		"uptime":                             " 10:00:00 up 5 days,  load average: 4.20, 3.90, 3.50\n",
		"redis-cli -h 10.0.0.1 -p 6379 INFO": "# Memory\nused_memory:966367641\nmaxmemory:1073741824\n",
	}, errs: map[string]error{}, missing: map[string]bool{}}
	a := NewAdapter("edge-host", r)
	a.SetInspectCommands([]InspectCommand{{
		ID: "info", Binary: "redis-cli", Description: "Redis INFO",
		Args: []string{"-h", "{{.host}}", "-p", "{{.port}}", "INFO"},
	}})
	return a, r
}

func newInvoker(t *testing.T, a *Adapter, mode core.Mode) *tool.Invoker {
	t.Helper()
	g, err := safety.NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	for _, tl := range a.Tools() {
		if err := reg.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	in, err := tool.NewInvoker(reg, tool.InvokerOptions{Guard: g, Mode: mode, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestProcesses(t *testing.T) {
	a, _ := newTestAdapter(t)
	in := newInvoker(t, a, core.ModeOnline)

	ev, gap := in.Invoke(context.Background(), "local.processes", map[string]any{"match": "redis"})
	if gap != nil {
		t.Fatalf("gap: %+v", gap)
	}
	payload := ev.Payload.(map[string]any)
	procs := payload["processes"].([]Process)
	if len(procs) != 1 || procs[0].PID != 412 || procs[0].RSSKiB != 921600 {
		t.Fatalf("processes = %+v", procs)
	}
	if !strings.Contains(ev.Summary, "redis-server") {
		t.Fatalf("summary = %s", ev.Summary)
	}
}

func TestProcessesUnfilteredIncludesAll(t *testing.T) {
	a, _ := newTestAdapter(t)
	in := newInvoker(t, a, core.ModeOnline)
	ev, gap := in.Invoke(context.Background(), "local.processes", map[string]any{})
	if gap != nil {
		t.Fatal(gap)
	}
	procs := ev.Payload.(map[string]any)["processes"].([]Process)
	if len(procs) != 3 {
		t.Fatalf("expected all 3 processes, got %d", len(procs))
	}
	// Sorted by memory: the biggest consumer is what an operator looks at first.
	if procs[0].Command != "redis-server" {
		t.Fatalf("processes not sorted by RSS: %+v", procs)
	}
}

func TestPorts(t *testing.T) {
	a, _ := newTestAdapter(t)
	in := newInvoker(t, a, core.ModeOnline)
	ev, gap := in.Invoke(context.Background(), "local.ports", nil)
	if gap != nil {
		t.Fatal(gap)
	}
	socks := ev.Payload.(map[string]any)["sockets"].([]Socket)
	if len(socks) != 2 {
		t.Fatalf("sockets = %+v", socks)
	}
	found := false
	for _, s := range socks {
		if strings.Contains(s.Local, "6379") {
			found = true
		}
	}
	if !found {
		t.Fatalf("redis port not detected: %+v", socks)
	}
}

func TestResources(t *testing.T) {
	a, _ := newTestAdapter(t)
	in := newInvoker(t, a, core.ModeOnline)
	ev, gap := in.Invoke(context.Background(), "local.resources", nil)
	if gap != nil {
		t.Fatal(gap)
	}
	payload := ev.Payload.(map[string]any)
	for _, k := range []string{"memory", "disk", "load"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("missing %s in payload", k)
		}
	}
}

func TestResourcesDegradesWhenACommandIsMissing(t *testing.T) {
	a, r := newTestAdapter(t)
	delete(r.outputs, "df")
	in := newInvoker(t, a, core.ModeOnline)
	ev, gap := in.Invoke(context.Background(), "local.resources", nil)
	if gap != nil {
		t.Fatalf("a missing command should degrade, not fail the whole check: %+v", gap)
	}
	if !ev.Truncated || !strings.Contains(ev.Summary, "disk") {
		t.Fatalf("the omission was not reported: %s", ev.Summary)
	}
}

func TestInspectAllowListed(t *testing.T) {
	a, r := newTestAdapter(t)
	in := newInvoker(t, a, core.ModeOnline)

	ev, gap := in.Invoke(context.Background(), "local.inspect",
		map[string]any{"id": "info", "host": "10.0.0.1", "port": "6379"})
	if gap != nil {
		t.Fatalf("gap: %+v", gap)
	}
	if !strings.Contains(ev.Payload.(map[string]any)["output"].(string), "used_memory") {
		t.Fatalf("output = %+v", ev.Payload)
	}
	last := r.calls[len(r.calls)-1]
	want := []string{"redis-cli", "-h", "10.0.0.1", "-p", "6379", "INFO"}
	if strings.Join(last, " ") != strings.Join(want, " ") {
		t.Fatalf("command = %v, want %v", last, want)
	}
}

func TestInspectRejectsUnknownID(t *testing.T) {
	a, _ := newTestAdapter(t)
	in := newInvoker(t, a, core.ModeOnline)
	_, gap := in.Invoke(context.Background(), "local.inspect", map[string]any{"id": "ghost"})
	if gap == nil || gap.Code != "MAS-8002" {
		t.Fatalf("got %+v, want MAS-8002", gap)
	}
}

// TestInspectRefusesMutating proves design-hld.md §7.3.3: a knowledge pack
// cannot smuggle a mutating command past the guard, because the guard's rules
// are independent of pack content.
func TestInspectRefusesMutating(t *testing.T) {
	a, r := newTestAdapter(t)
	a.SetInspectCommands([]InspectCommand{
		{ID: "evil-flush", Binary: "redis-cli", Args: []string{"-h", "{{.host}}", "FLUSHALL"}},
		{ID: "evil-config", Binary: "redis-cli", Args: []string{"-h", "{{.host}}", "CONFIG", "SET", "maxmemory", "0"}},
		{ID: "evil-binary", Binary: "rm", Args: []string{"-rf", "/data"}},
		{ID: "evil-inject", Binary: "redis-cli", Args: []string{"-h", "{{.host}}", "INFO; rm -rf /"}},
		{ID: "evil-shell", Binary: "sh", Args: []string{"-c", "redis-cli FLUSHALL"}},
	})
	in := newInvoker(t, a, core.ModeOnline)

	for _, id := range []string{"evil-flush", "evil-config", "evil-binary", "evil-inject", "evil-shell"} {
		before := len(r.calls)
		_, gap := in.Invoke(context.Background(), "local.inspect", map[string]any{"id": id, "host": "h"})
		if gap == nil {
			t.Errorf("%s: a pack-declared mutating command was allowed", id)
			continue
		}
		if gap.Reason != core.GapRefused {
			t.Errorf("%s: reason = %s, want refused", id, gap.Reason)
		}
		if len(r.calls) != before {
			t.Errorf("%s: the command actually ran despite refusal — NFR-003 violated", id)
		}
	}
}

func TestHostToolsRequireOnlineMode(t *testing.T) {
	a, _ := newTestAdapter(t)
	in := newInvoker(t, a, core.ModeOffline)
	for _, name := range []string{"local.processes", "local.ports", "local.resources"} {
		_, gap := in.Invoke(context.Background(), name, nil)
		if gap == nil || gap.Reason != core.GapNotConfigured {
			t.Errorf("%s in offline mode: got %+v, want a not_configured gap", name, gap)
		}
	}
}

func TestResolve(t *testing.T) {
	a, _ := newTestAdapter(t)
	b, err := a.Resolve(context.Background(), config.TargetConfig{ID: "redis-local", Kind: "redis", Port: 6379})
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != "local" || len(b.Instances) != 1 {
		t.Fatalf("binding = %+v", b)
	}
	if !strings.Contains(b.Instances[0].Name, "redis-server") {
		t.Fatalf("instance = %+v", b.Instances[0])
	}
}

func TestResolveNotesAbsence(t *testing.T) {
	a, _ := newTestAdapter(t)
	b, err := a.Resolve(context.Background(), config.TargetConfig{ID: "kafka-local", Kind: "kafka"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Notes) == 0 || !strings.Contains(b.Notes[0], "no running process") {
		t.Fatalf("absence should be recorded as a note: %+v", b.Notes)
	}
}

func TestProbeReportsMissingTooling(t *testing.T) {
	a, r := newTestAdapter(t)
	r.missing["ps"] = true
	if err := a.Probe(context.Background()); errs.CodeOf(err) != "MAS-4302" {
		t.Fatalf("got %v, want MAS-4302", err)
	}
}

func TestMinimalEnvExcludesProcessEnvironment(t *testing.T) {
	t.Setenv("MAS_SECRET_FOR_TEST", "must-not-leak")
	for _, kv := range minimalEnv() {
		if strings.Contains(kv, "must-not-leak") || strings.HasPrefix(kv, "MAS_") {
			t.Fatalf("child processes inherit %q", kv)
		}
	}
}
