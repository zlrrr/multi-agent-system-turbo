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
