package safety

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func newTestGuard(t *testing.T) *Guard {
	t.Helper()
	g, err := NewGuard(config.Default().Safety)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return g
}

func readOnlyCmd(bin string, args ...string) Call {
	return Call{Tool: "test", Class: ClassReadOnly, Command: &CommandEffect{Binary: bin, Args: args}}
}

func readOnlyHTTP(method, u string) Call {
	return Call{Tool: "test", Class: ClassReadOnly, HTTP: &HTTPEffect{Method: method, URL: u}}
}

// TestGuardAllowsLegitimateReads keeps the guard honest in the other direction:
// a guard that refuses everything would pass the adversarial suite while making
// the product useless.
func TestGuardAllowsLegitimateReads(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()
	allowed := []Call{
		readOnlyCmd("redis-cli", "-h", "10.0.0.1", "-p", "6379", "INFO"),
		readOnlyCmd("redis-cli", "-h", "h", "CONFIG", "GET", "maxmemory"),
		readOnlyCmd("redis-cli", "-h", "h", "CLIENT", "LIST"),
		readOnlyCmd("redis-cli", "-h", "h", "SLOWLOG", "GET", "10"),
		readOnlyCmd("redis-cli", "-h", "h", "LATENCY", "LATEST"),
		readOnlyCmd("redis-cli", "-h", "h", "MEMORY", "DOCTOR"),
		readOnlyCmd("redis-cli", "-h", "h", "CLUSTER", "INFO"),
		readOnlyCmd("mongosh", "--host", "h", "--eval", "db.serverStatus()"),
		readOnlyCmd("mongosh", "--host", "h", "--eval", "rs.status()"),
		readOnlyCmd("kafka-topics.sh", "--bootstrap-server", "b:9092", "--describe", "--topic", "orders"),
		readOnlyCmd("kafka-consumer-groups.sh", "--bootstrap-server", "b:9092", "--describe", "--group", "g1"),
		readOnlyCmd("ps", "-eo", "pid,comm,rss"),
		readOnlyCmd("ss", "-lntp"),
		readOnlyCmd("git", "clone", "--depth", "1", "https://example.com/redis.git", "/cache/redis"),
		readOnlyCmd("git", "-C", "/cache/redis", "log", "-1"),
		readOnlyCmd("git", "ls-remote", "https://example.com/redis.git"),
		readOnlyHTTP("GET", "http://prom:9090/api/v1/query?query=up"),
		readOnlyHTTP("POST", "http://prom:9090/api/v1/query_range"),
		readOnlyHTTP("GET", "http://prom:9090/api/v1/label/job/values"),
		readOnlyHTTP("GET", "http://loki:3100/loki/api/v1/query_range?query=%7Bjob%3D%22redis%22%7D"),
		readOnlyHTTP("GET", "https://k8s:6443/api/v1/namespaces/mw/pods"),
		readOnlyHTTP("GET", "https://k8s:6443/api/v1/namespaces/mw/pods/redis-0/log"),
		readOnlyHTTP("GET", "https://k8s:6443/apis/apps/v1/namespaces/mw/statefulsets"),
		readOnlyHTTP("GET", "https://k8s:6443/api/v1/nodes"),
		readOnlyHTTP("GET", "https://k8s:6443/version"),
		{Tool: "t", Class: ClassReadOnly, File: &FileEffect{Path: "/etc/redis/redis.conf"}},
	}
	for _, c := range allowed {
		if err := g.Authorize(ctx, c); err != nil {
			t.Errorf("legitimate read refused: %s → %v", describe(c), err)
		}
	}
}

// TestGuardAdversarial is the FR-006 / NFR-003 suite: nothing that could mutate
// a target may pass, however it is phrased.
func TestGuardAdversarial(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()

	cases := []struct {
		name string
		call Call
		want string
	}{
		{"declared mutating", Call{Tool: "t", Class: ClassMutating, Command: &CommandEffect{Binary: "ps"}}, "MAS-8001"},
		{"redis flushall", readOnlyCmd("redis-cli", "-h", "h", "FLUSHALL"), "MAS-8001"},
		{"redis flushall lowercase", readOnlyCmd("redis-cli", "-h", "h", "flushall"), "MAS-8001"},
		{"redis flushall mixed case", readOnlyCmd("redis-cli", "-h", "h", "FlUsHaLl"), "MAS-8001"},
		{"redis flushdb", readOnlyCmd("redis-cli", "flushdb"), "MAS-8001"},
		{"redis del", readOnlyCmd("redis-cli", "-h", "h", "DEL", "key"), "MAS-8001"},
		{"redis set", readOnlyCmd("redis-cli", "-h", "h", "SET", "k", "v"), "MAS-8001"},
		{"redis config set", readOnlyCmd("redis-cli", "-h", "h", "CONFIG", "SET", "maxmemory", "0"), "MAS-8001"},
		{"redis config rewrite", readOnlyCmd("redis-cli", "-h", "h", "CONFIG", "REWRITE"), "MAS-8001"},
		{"redis client kill", readOnlyCmd("redis-cli", "-h", "h", "CLIENT", "KILL", "ID", "3"), "MAS-8001"},
		{"redis cluster failover", readOnlyCmd("redis-cli", "-h", "h", "CLUSTER", "FAILOVER"), "MAS-8001"},
		{"redis shutdown", readOnlyCmd("redis-cli", "shutdown"), "MAS-8001"},
		{"redis eval", readOnlyCmd("redis-cli", "-h", "h", "EVAL", "return 1", "0"), "MAS-8001"},
		{"redis debug", readOnlyCmd("redis-cli", "-h", "h", "DEBUG", "SEGFAULT"), "MAS-8001"},
		{"redis monitor", readOnlyCmd("redis-cli", "-h", "h", "MONITOR"), "MAS-8001"},
		{"redis keys scan", readOnlyCmd("redis-cli", "-h", "h", "KEYS", "*"), "MAS-8001"},
		{"redis unknown verb", readOnlyCmd("redis-cli", "-h", "h", "WEIRDCMD"), "MAS-8002"},
		{"redis no verb", readOnlyCmd("redis-cli", "-h", "h"), "MAS-8002"},
		{"redis eval flag", readOnlyCmd("redis-cli", "--eval", "/tmp/x.lua"), "MAS-8001"},
		{"mongo eval mutation", readOnlyCmd("mongosh", "--host", "h", "--eval", "db.dropDatabase()"), "MAS-8005"},
		{"mongo eval insert", readOnlyCmd("mongosh", "--host", "h", "--eval", "db.c.insertOne({})"), "MAS-8005"},
		{"mongo eval shutdown", readOnlyCmd("mongosh", "--eval", "db.adminCommand({shutdown:1})"), "MAS-8005"},
		{"kafka delete topic", readOnlyCmd("kafka-topics.sh", "--bootstrap-server", "b", "--delete", "--topic", "t"), "MAS-8001"},
		{"kafka create topic", readOnlyCmd("kafka-topics.sh", "--bootstrap-server", "b", "--create", "--topic", "t"), "MAS-8001"},
		{"kafka alter", readOnlyCmd("kafka-topics.sh", "--bootstrap-server", "b", "--alter", "--topic", "t"), "MAS-8001"},
		{"kafka reset offsets", readOnlyCmd("kafka-consumer-groups.sh", "--bootstrap-server", "b", "--reset-offsets"), "MAS-8002"},
		{"pulsar topic delete", readOnlyCmd("pulsar-admin", "topics", "delete", "t"), "MAS-8001"},
		{"unlisted binary kubectl", readOnlyCmd("kubectl", "get", "pods"), "MAS-8002"},
		{"unlisted binary rm", readOnlyCmd("rm", "-rf", "/"), "MAS-8002"},
		{"unlisted binary sh", readOnlyCmd("sh", "-c", "echo hi"), "MAS-8002"},
		{"unlisted binary systemctl", readOnlyCmd("systemctl", "restart", "redis"), "MAS-8002"},
		{"git push", readOnlyCmd("git", "push", "origin", "main"), "MAS-8001"},
		{"git clean", readOnlyCmd("git", "clean", "-fdx"), "MAS-8001"},
		{"git global config", readOnlyCmd("git", "config", "--global", "user.name", "x"), "MAS-8001"},
		{"shell metachar semicolon", readOnlyCmd("redis-cli", "INFO; rm -rf /"), "MAS-8005"},
		{"shell metachar pipe", readOnlyCmd("ps", "aux | tee /etc/passwd"), "MAS-8005"},
		{"shell metachar backtick", readOnlyCmd("ps", "`whoami`"), "MAS-8005"},
		{"command substitution", readOnlyCmd("ps", "$(id)"), "MAS-8005"},
		{"redirect", readOnlyCmd("ps", "aux > /etc/passwd"), "MAS-8005"},
		{"newline injection", readOnlyCmd("ps", "aux\nrm -rf /"), "MAS-8005"},
		{"arg traversal", readOnlyCmd("git", "-C", "../../etc", "log"), "MAS-8005"},
		{"binary traversal", readOnlyCmd("../../bin/sh", "-c", "x"), "MAS-8005"},
		{"absolute path to denied binary", readOnlyCmd("/bin/rm", "-rf", "/"), "MAS-8002"},
		{"absolute path to allowed binary", readOnlyCmd("/usr/bin/ps", "aux"), ""},
		{"http PUT", readOnlyHTTP("PUT", "https://k8s:6443/api/v1/namespaces/mw/pods/redis-0"), "MAS-8001"},
		{"http DELETE", readOnlyHTTP("DELETE", "https://k8s:6443/api/v1/namespaces/mw/pods/redis-0"), "MAS-8001"},
		{"http PATCH", readOnlyHTTP("PATCH", "https://k8s:6443/api/v1/namespaces/mw/pods/redis-0"), "MAS-8001"},
		{"http POST to k8s", readOnlyHTTP("POST", "https://k8s:6443/api/v1/namespaces/mw/pods"), "MAS-8003"},
		{"http POST to pod exec", readOnlyHTTP("POST", "https://k8s:6443/api/v1/namespaces/mw/pods/redis-0/exec"), "MAS-8003"},
		{"http GET pod exec", readOnlyHTTP("GET", "https://k8s:6443/api/v1/namespaces/mw/pods/redis-0/exec"), "MAS-8003"},
		{"http GET secrets", readOnlyHTTP("GET", "https://k8s:6443/api/v1/namespaces/mw/secrets"), "MAS-8003"},
		{"http GET serviceaccount tokens", readOnlyHTTP("GET", "https://k8s:6443/api/v1/namespaces/mw/serviceaccounts"), "MAS-8003"},
		{"http prometheus admin delete", readOnlyHTTP("POST", "http://prom:9090/api/v1/admin/tsdb/delete_series"), "MAS-8003"},
		{"http loki push", readOnlyHTTP("POST", "http://loki:3100/loki/api/v1/push"), "MAS-8003"},
		{"http path traversal", readOnlyHTTP("GET", "http://prom:9090/api/v1/../../admin"), "MAS-8005"},
		{"http unknown path", readOnlyHTTP("GET", "http://prom:9090/admin/shutdown"), "MAS-8003"},
		{"file shadow", readOnlyFile("/etc/shadow"), "MAS-8003"},
		{"file ssh key", readOnlyFile("/root/.ssh/id_rsa"), "MAS-8003"},
		{"file traversal", readOnlyFile("/var/log/../../etc/shadow"), "MAS-8005"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := g.Authorize(ctx, tc.call)
			got := errs.CodeOf(err)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected allow, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected refusal %s, call was ALLOWED: %s", tc.want, describe(tc.call))
			}
			if got != tc.want {
				t.Fatalf("got %s (%v), want %s", got, err, tc.want)
			}
		})
	}
}

func readOnlyFile(path string) Call {
	return Call{Tool: "test", Class: ClassReadOnly, File: &FileEffect{Path: path}}
}

func TestGuardFileReadRefusals(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()
	for _, p := range []string{
		"/etc/shadow", "/etc/gshadow", "/etc/sudoers", "/root/.ssh/id_rsa",
		"/home/u/.ssh/authorized_keys", "/home/u/.netrc", "/proc/self/environ",
		"/var/lib/../../etc/shadow",
	} {
		err := g.Authorize(ctx, Call{Tool: "t", Class: ClassReadOnly, File: &FileEffect{Path: p}})
		if err == nil {
			t.Errorf("sensitive file read allowed: %s", p)
		}
	}
}

func TestGuardCeilings(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()
	maxBytes, maxTimeout := g.Limits()

	over := readOnlyCmd("ps", "aux")
	over.Bytes = maxBytes + 1
	if code := errs.CodeOf(g.Authorize(ctx, over)); code != "MAS-8010" {
		t.Errorf("oversized response: got %s, want MAS-8010", code)
	}

	slow := readOnlyCmd("ps", "aux")
	slow.Timeout = maxTimeout + time.Second
	if code := errs.CodeOf(g.Authorize(ctx, slow)); code != "MAS-8010" {
		t.Errorf("excessive timeout: got %s, want MAS-8010", code)
	}

	ok := readOnlyCmd("ps", "aux")
	ok.Bytes, ok.Timeout = maxBytes, maxTimeout
	if err := g.Authorize(ctx, ok); err != nil {
		t.Errorf("call at exactly the ceiling refused: %v", err)
	}
}

func TestGuardRequiresExactlyOneEffect(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()
	none := Call{Tool: "t", Class: ClassReadOnly}
	if code := errs.CodeOf(g.Authorize(ctx, none)); code != "MAS-8005" {
		t.Errorf("no effect: got %s, want MAS-8005", code)
	}
	both := Call{Tool: "t", Class: ClassReadOnly,
		HTTP:    &HTTPEffect{Method: "GET", URL: "http://prom:9090/api/v1/query"},
		Command: &CommandEffect{Binary: "ps"}}
	if code := errs.CodeOf(g.Authorize(ctx, both)); code != "MAS-8005" {
		t.Errorf("two effects: got %s, want MAS-8005", code)
	}
}

// TestGuardCannotBeWidened proves Art. IV.2: configuration may narrow the guard
// but never widen it.
func TestGuardCannotBeWidened(t *testing.T) {
	cfg := config.Default().Safety
	cfg.ExtraDeniedBinaries = []string{"redis-cli"}
	cfg.ExtraDeniedArgs = []string{`(?i)^INFO$`}
	g, err := NewGuard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if code := errs.CodeOf(g.Authorize(ctx, readOnlyCmd("redis-cli", "INFO"))); code != "MAS-8002" {
		t.Errorf("narrowing a binary did not take effect: %s", code)
	}
	if code := errs.CodeOf(g.Authorize(ctx, readOnlyCmd("ps", "INFO"))); code != "MAS-8005" {
		t.Errorf("narrowing an argument did not take effect: %s", code)
	}
	// There is no configuration field capable of adding a binary or a path.
	for _, name := range []string{"AllowedBinaries", "ExtraAllowedBinaries", "AllowedPaths", "Disable", "Enabled"} {
		if fieldExists(cfg, name) {
			t.Fatalf("config.SafetyConfig exposes %q, which could widen the guard", name)
		}
	}
}

func fieldExists(v any, name string) bool {
	return strings.Contains(fmt.Sprintf("%#v", v), name+":")
}

func TestSplitArgsSkipsFlagValues(t *testing.T) {
	pos, flags := splitArgs([]string{"-h", "host", "-p", "6379", "INFO", "--eval=db.stats()"},
		map[string]bool{"-h": true, "-p": true})
	if len(pos) != 1 || pos[0] != "INFO" {
		t.Fatalf("positionals = %v, want [INFO]", pos)
	}
	if flags["--eval"] != "db.stats()" {
		t.Fatalf("flag values = %v", flags)
	}
}

func TestAllowListIntrospection(t *testing.T) {
	g := newTestGuard(t)
	cmds := g.AllowedCommands()
	if len(cmds) < 10 {
		t.Fatalf("only %d command rules; the allow-list looks incomplete", len(cmds))
	}
	for i := 1; i < len(cmds); i++ {
		if cmds[i-1].Binary >= cmds[i].Binary {
			t.Fatalf("AllowedCommands is not sorted at %d", i)
		}
	}
	for _, c := range cmds {
		if c.Description == "" {
			t.Errorf("command rule %s has no description; it appears in the user manual", c.Binary)
		}
	}
	if len(g.AllowedPaths()) < 10 {
		t.Fatal("path allow-list looks incomplete")
	}
}

func execCall(binary string, args ...string) Call {
	return Call{
		Tool: "kube.exec", Class: ClassReadOnly,
		Exec: &ExecEffect{
			Namespace: "middleware", Pod: "redis-0", Container: "redis",
			Binary: binary, Args: args,
		},
	}
}

// TestGuardAuthorisesExecAsOneEffect is FR-002. An exec must be one effect the
// guard checks twice, not two effects a caller composes: a caller that
// authorised only the transport would otherwise compile and run.
func TestGuardAuthorisesExecAsOneEffect(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()

	if err := g.Authorize(ctx, execCall("redis-cli", "-h", "10.0.0.1", "INFO", "all")); err != nil {
		t.Fatalf("an allow-listed read-only command in a pod was refused: %v", err)
	}

	// Declaring exec alongside another effect must be refused, or the
	// "exactly one effect" invariant would not hold for the new kind.
	both := execCall("redis-cli", "INFO")
	both.HTTP = &HTTPEffect{Method: "GET", URL: "https://k8s/api/v1/namespaces/x/pods/y/exec"}
	if err := g.Authorize(ctx, both); errs.CodeOf(err) != "MAS-8005" {
		t.Errorf("two effects in one call: got %v (%s), want MAS-8005", err, errs.CodeOf(err))
	}
}

// TestExecRefusesUnlistedBinary is FR-003. The refusal must come from the
// command allow-list, not from anything about Kubernetes: exec changes where
// vetted commands run, never which commands are vetted.
func TestExecRefusesUnlistedBinary(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()

	for _, binary := range []string{"kubectl", "sh", "bash", "curl", "python3", "rm"} {
		err := g.Authorize(ctx, execCall(binary, "--help"))
		if code := errs.CodeOf(err); code != "MAS-8002" {
			t.Errorf("exec %s: got %v (%s), want MAS-8002 — deny-by-default must not depend on the transport",
				binary, err, code)
		}
	}
}

// TestExecRefusesMutatingCommand is FR-004: an identical transport must not make
// a mutating command acceptable.
func TestExecRefusesMutatingCommand(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()

	for _, tc := range []struct {
		binary string
		args   []string
	}{
		{"redis-cli", []string{"FLUSHALL"}},
		{"redis-cli", []string{"CONFIG", "SET", "maxmemory", "0"}},
		{"redis-cli", []string{"--eval", "/tmp/x.lua"}},
		{"kafka-topics.sh", []string{"--delete", "--topic", "orders"}},
		{"mongosh", []string{"--eval", "db.dropDatabase()"}},
	} {
		err := g.Authorize(ctx, execCall(tc.binary, tc.args...))
		if err == nil {
			t.Errorf("exec %s %v was authorised", tc.binary, tc.args)
			continue
		}
		if code := errs.CodeOf(err); !strings.HasPrefix(code, "MAS-80") {
			t.Errorf("exec %s %v: got %s, want a guard refusal", tc.binary, tc.args, code)
		}
	}

	// A mutating class is refused before any effect is examined.
	mutating := execCall("redis-cli", "INFO")
	mutating.Class = ClassMutating
	if err := g.Authorize(ctx, mutating); errs.CodeOf(err) != "MAS-8001" {
		t.Errorf("a mutating exec: got %v, want MAS-8001", err)
	}
}

// TestExecPathComponentsCannotEscape is the subtle one. The exec path rule is a
// regex over a URL built from namespace, pod and container. If a component
// could contain a slash or a traversal, the built URL would still match the rule
// while addressing a different endpoint — the check would look like it worked.
func TestExecPathComponentsCannotEscape(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name                      string
		namespace, pod, container string
	}{
		{"slash in pod", "middleware", "redis-0/../../secrets", "redis"},
		{"slash in namespace", "kube-system/pods", "redis-0", "redis"},
		{"traversal in pod", "middleware", "..", "redis"},
		{"traversal in container", "middleware", "redis-0", "../etc"},
		{"empty namespace", "", "redis-0", "redis"},
		{"uppercase pod", "middleware", "Redis-0", "redis"},
		{"query injection", "middleware", "redis-0?command=sh", "redis"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := execCall("redis-cli", "INFO")
			c.Exec.Namespace, c.Exec.Pod, c.Exec.Container = tc.namespace, tc.pod, tc.container
			err := g.Authorize(ctx, c)
			if err == nil {
				t.Fatalf("%s was accepted; the path rule can be escaped", tc.name)
			}
			if code := errs.CodeOf(err); !strings.HasPrefix(code, "MAS-80") {
				t.Errorf("got %s, want a guard refusal", code)
			}
		})
	}

	// An empty container is legitimate: it means the pod's default container.
	ok := execCall("redis-cli", "INFO")
	ok.Exec.Container = ""
	if err := g.Authorize(ctx, ok); err != nil {
		t.Errorf("an unspecified container must mean the default one: %v", err)
	}
}

// TestExecPathRuleMatchesOnlyExec proves the new rule did not widen the read
// surface: the subresources next to exec must stay refused.
func TestExecPathRuleMatchesOnlyExec(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()

	for _, path := range []string{
		"/api/v1/namespaces/middleware/pods/redis-0/attach",
		"/api/v1/namespaces/middleware/pods/redis-0/portforward",
		"/api/v1/namespaces/middleware/pods/redis-0/eviction",
		"/api/v1/namespaces/middleware/pods/redis-0/exec/extra",
	} {
		err := g.Authorize(ctx, Call{
			Tool: "test", Class: ClassReadOnly,
			HTTP: &HTTPEffect{Method: "GET", URL: "https://k8s.local" + path},
		})
		if err == nil {
			t.Errorf("%s was authorised; the exec rule must match exec and nothing else", path)
		}
	}
}

// TestExecEndpointUnreachableThroughPlainHTTP is the property the adversarial
// suite already asserted and this feature had to preserve. The exec subresource
// stays out of the general path allow-list, so a tool that declares an
// HTTPEffect against it is refused — the endpoint is reachable only through the
// effect that also checks the command.
func TestExecEndpointUnreachableThroughPlainHTTP(t *testing.T) {
	g := newTestGuard(t)
	ctx := context.Background()

	err := g.Authorize(ctx, Call{
		Tool: "sneaky", Class: ClassReadOnly,
		HTTP: &HTTPEffect{
			Method: "GET",
			URL:    "https://k8s:6443/api/v1/namespaces/mw/pods/redis-0/exec?command=sh",
		},
	})
	if errs.CodeOf(err) != "MAS-8003" {
		t.Fatalf("got %v (%s), want MAS-8003: exec must not be reachable without the command check",
			err, errs.CodeOf(err))
	}

	for _, rule := range g.AllowedPaths() {
		if strings.Contains(rule.Pattern, "exec") {
			t.Errorf("the exec endpoint appears in the general path allow-list as %q; "+
				"it must be reachable only through an ExecEffect", rule.Pattern)
		}
	}
}
