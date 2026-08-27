package safety

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Class declares whether an effect reads or writes.
type Class string

const (
	ClassReadOnly Class = "read_only"
	ClassMutating Class = "mutating"
)

// HTTPEffect describes an intended HTTP request against a target environment.
type HTTPEffect struct {
	Method string
	URL    string
}

// CommandEffect describes an intended process execution. Args is an argument
// vector: the guard refuses anything that would need a shell to interpret, and
// no code path in this repository ever invokes one.
type CommandEffect struct {
	Binary string
	Args   []string
}

// ExecEffect describes running a command inside a container in a Kubernetes pod.
//
// It is one effect with two constraints rather than two effects. Modelling it as
// an HTTP call plus a command call would leave the composition to the caller,
// and a caller that authorised only the transport would still compile and still
// run — the omission would be invisible until someone read the code
// (design-hld.md §3).
type ExecEffect struct {
	Namespace string
	Pod       string
	Container string
	Binary    string
	Args      []string
}

// FileEffect describes an intended file read.
type FileEffect struct {
	Path string
}

// Call is one intended effect, declared by a tool before it happens.
type Call struct {
	Tool    string
	Class   Class
	HTTP    *HTTPEffect
	Command *CommandEffect
	Exec    *ExecEffect
	File    *FileEffect
	Bytes   int
	Timeout time.Duration
}

// CommandRule is the allow-list entry for one binary. Absence of a rule means
// the binary may not be executed: the guard is deny-by-default (Art. IV.2).
type CommandRule struct {
	Binary          string
	Description     string
	FlagsWithValue  []string            // flags that consume the following argument
	AllowedVerbs    []string            // if set, the first positional argument must be one of these
	DeniedSequences [][]string          // consecutive positional sequences that are refused
	ValueAllowList  map[string][]string // flag → regexes its value must match
}

// PathRule is the allow-list entry for one HTTP read endpoint.
type PathRule struct {
	Method      string
	Pattern     string
	Description string
}

// Guard authorises every effect a tool intends against a target environment.
// It is the single choke point required by Constitution Art. IV.1 and is
// immutable once built: nothing at runtime can widen it.
//
// Scope note: the guard governs effects against *target environments* — the
// metrics and log backends, the Kubernetes API, the host, and source
// repositories. Calls to the configured LLM provider are not target effects and
// are governed by the provider's own budget and redaction path instead.
type Guard struct {
	commands   map[string]compiledCommandRule
	paths      []compiledPathRule
	deniedArgs []*regexp.Regexp
	maxBytes   int
	maxTimeout time.Duration
}

type compiledCommandRule struct {
	rule            CommandRule
	flagsWithValue  map[string]bool
	allowedVerbs    map[string]bool
	deniedSequences [][]string
	valueAllowList  map[string][]*regexp.Regexp
}

type compiledPathRule struct {
	method  string
	pattern *regexp.Regexp
	desc    string
}

// mutatingPositionals are refused as a positional argument to any allow-listed
// binary, whatever the binary's own rule says. This list is independent of
// knowledge-pack content, so a pack cannot smuggle a mutating command past the
// guard (HLD §7.3.3).
var mutatingPositionals = []string{
	// generic
	"delete", "del", "remove", "rm", "rmdir", "destroy", "drop", "truncate",
	"purge", "wipe", "erase", "unlink", "move", "mv", "rename", "copy", "cp",
	"create", "add", "insert", "update", "upsert", "put", "post", "patch",
	"write", "modify", "edit", "alter", "apply", "replace", "set", "unset",
	"install", "uninstall", "upgrade", "downgrade", "rollback", "migrate",
	"restart", "reboot", "shutdown", "stop", "start", "kill", "terminate",
	"scale", "cordon", "drain", "taint", "evict", "failover", "promote",
	"demote", "rebalance", "reassign", "reset", "flush", "compact", "expire",
	"chmod", "chown", "chgrp", "mkfs", "dd", "truncate-log",
	// redis
	"flushall", "flushdb", "bgsave", "bgrewriteaof", "swapdb", "replicaof",
	"slaveof", "debug", "eval", "evalsha", "script", "restore", "setex",
	"setnx", "getset", "append", "incr", "incrby", "decr", "decrby", "lpush",
	"rpush", "lpop", "rpop", "sadd", "srem", "zadd", "zrem", "hset", "hdel",
	"xadd", "xtrim", "pfadd", "persist", "pexpire", "sort", "keys", "monitor",
	"subscribe", "psubscribe", "sync", "psync", "swapdb",
	// mongodb / sql-ish
	"dropdatabase", "dropindex", "createindex", "compactdb", "shutdownserver",
	"truncatetable", "grant", "revoke",
	// version control
	"push", "commit", "merge", "rebase", "cherry-pick", "revert", "tag",
	"gc", "prune", "clean", "checkout", "switch", "restore-file",
}

// dangerousArgPatterns are refused anywhere in an argument vector.
var dangerousArgPatterns = []string{
	"[;&|`$><\\n\\r]", // shell metacharacters and redirection
	`\$\(`,            // command substitution
	`\.\.[\\/]`,       // path traversal
	`\x00`,            // NUL byte
	`(?i)^--?(exec|eval-file|shell|command)$`,
	`(?i)\bsh\s+-c\b`,
	`(?i)\bbash\s+-c\b`,
}

// DefaultCommandRules is the read-only command allow-list. Every entry is an
// inspection command that reports state without changing it. Extending this list
// is a specification change, never a runtime decision (Art. IV.2).
func DefaultCommandRules() []CommandRule {
	return []CommandRule{
		{
			Binary: "redis-cli", Description: "Redis read-only inspection",
			FlagsWithValue: []string{"-h", "-p", "-n", "-t", "--user", "-u", "--tls", "--cacert", "--cert", "--key"},
			AllowedVerbs: []string{
				"info", "ping", "dbsize", "lastsave", "role", "time", "command",
				"client", "config", "cluster", "slowlog", "latency", "memory",
				"acl", "object", "type", "ttl", "pttl", "exists", "llen",
				"scard", "zcard", "hlen", "strlen", "xinfo", "lpos", "randomkey",
			},
			DeniedSequences: [][]string{
				{"config", "set"}, {"config", "rewrite"}, {"config", "resetstat"},
				{"client", "kill"}, {"client", "pause"}, {"client", "unpause"},
				{"cluster", "reset"}, {"cluster", "forget"}, {"cluster", "failover"},
				{"cluster", "setslot"}, {"cluster", "addslots"}, {"cluster", "delslots"},
				{"cluster", "meet"}, {"cluster", "replicate"},
				{"acl", "setuser"}, {"acl", "deluser"}, {"acl", "load"}, {"acl", "save"},
				{"slowlog", "reset"}, {"latency", "reset"}, {"memory", "purge"},
			},
		},
		{
			Binary: "mongosh", Description: "MongoDB read-only inspection",
			FlagsWithValue: []string{"--host", "--port", "--username", "-u", "--authenticationDatabase", "--eval", "--tls", "--tlsCAFile"},
			ValueAllowList: map[string][]string{
				"--eval": {
					`^db\.serverStatus\(\)$`,
					`^db\.stats\(\)$`,
					`^db\.currentOp\(\)$`,
					`^db\.serverCmdLineOpts\(\)$`,
					`^db\.hostInfo\(\)$`,
					`^db\.getProfilingStatus\(\)$`,
					`^db\.adminCommand\(\{\s*(serverStatus|replSetGetStatus|getCmdLineOpts|hostInfo|connPoolStats|top|listDatabases)\s*:\s*1\s*\}\)$`,
					`^rs\.status\(\)$`,
					`^rs\.conf\(\)$`,
					`^sh\.status\(\)$`,
					`^db\.getSiblingDB\(['"][A-Za-z0-9_\-]+['"]\)\.stats\(\)$`,
				},
			},
		},
		{
			Binary: "kafka-topics.sh", Description: "Kafka topic inspection",
			FlagsWithValue: []string{"--bootstrap-server", "--topic", "--command-config"},
			AllowedVerbs:   []string{"--list", "--describe"},
		},
		{
			Binary: "kafka-consumer-groups.sh", Description: "Kafka consumer-group inspection",
			FlagsWithValue: []string{"--bootstrap-server", "--group", "--command-config"},
			AllowedVerbs:   []string{"--list", "--describe"},
		},
		{
			Binary: "kafka-broker-api-versions.sh", Description: "Kafka broker version inspection",
			FlagsWithValue: []string{"--bootstrap-server", "--command-config"},
		},
		{
			Binary: "pulsar-admin", Description: "Pulsar read-only inspection",
			FlagsWithValue: []string{"--admin-url", "--auth-params"},
			AllowedVerbs:   []string{"brokers", "topics", "namespaces", "tenants", "clusters", "bookies"},
			DeniedSequences: [][]string{
				{"topics", "delete"}, {"topics", "unload"}, {"namespaces", "delete"},
				{"clusters", "delete"}, {"brokers", "shutdown"},
			},
		},
		{Binary: "ps", Description: "process listing", AllowedVerbs: nil},
		{Binary: "ss", Description: "socket listing"},
		{Binary: "netstat", Description: "socket listing (legacy)"},
		{Binary: "df", Description: "filesystem usage"},
		{Binary: "free", Description: "memory usage"},
		{Binary: "uptime", Description: "host uptime"},
		{Binary: "uname", Description: "kernel identification"},
		{Binary: "nproc", Description: "CPU count"},
		{
			Binary: "git", Description: "source acquisition into the local cache",
			FlagsWithValue: []string{"-C", "--git-dir", "--work-tree", "--depth", "--branch", "-c"},
			AllowedVerbs: []string{
				"clone", "fetch", "ls-remote", "log", "show", "grep",
				"rev-parse", "cat-file", "describe", "config", "status", "ls-files",
			},
			DeniedSequences: [][]string{
				{"config", "--global"}, {"config", "--system"},
			},
		},
	}
}

// DefaultPathRules is the read-only HTTP allow-list. POST appears only for the
// query endpoints whose semantics are read-only but whose payloads outgrow a
// URL (HLD §7.3 check 2).
func DefaultPathRules() []PathRule {
	return []PathRule{
		// Prometheus-compatible
		{"GET", `^/api/v1/(query|query_range|series|labels|metadata|targets|rules|alerts|status/[a-z-]+)$`, "Prometheus read API"},
		{"GET", `^/api/v1/label/[^/]+/values$`, "Prometheus label values"},
		{"POST", `^/api/v1/(query|query_range|series)$`, "Prometheus query via POST body"},
		{"GET", `^/-/(healthy|ready)$`, "Prometheus health"},
		// Loki-compatible
		{"GET", `^/loki/api/v1/(query|query_range|labels|series|index/stats)$`, "Loki read API"},
		{"GET", `^/loki/api/v1/label/[^/]+/values$`, "Loki label values"},
		{"POST", `^/loki/api/v1/(query|query_range)$`, "Loki query via POST body"},
		{"GET", `^/(ready|metrics)$`, "Loki health"},
		// Kubernetes read-only
		{"GET", `^/(version|api|apis|healthz|readyz|livez)$`, "Kubernetes discovery"},
		{"GET", `^/api/v1/nodes(/[^/]+)?$`, "Kubernetes nodes"},
		{"GET", `^/api/v1/(events|pods|services|endpoints)$`, "Kubernetes cluster-wide list"},
		{"GET", `^/api/v1/namespaces(/[^/]+)?$`, "Kubernetes namespaces"},
		{"GET", `^/api/v1/namespaces/[^/]+/pods(/[^/]+)?$`, "Kubernetes pods"},
		{"GET", `^/api/v1/namespaces/[^/]+/pods/[^/]+/log$`, "Kubernetes pod logs"},
		{"GET", `^/api/v1/namespaces/[^/]+/(events|services|endpoints|configmaps|persistentvolumeclaims|replicationcontrollers)(/[^/]+)?$`, "Kubernetes namespaced reads"},
		{"GET", `^/apis/apps/v1/namespaces/[^/]+/(deployments|statefulsets|daemonsets|replicasets)(/[^/]+)?$`, "Kubernetes workloads"},
		{"GET", `^/apis/discovery\.k8s\.io/v1/namespaces/[^/]+/endpointslices(/[^/]+)?$`, "Kubernetes endpoint slices"},
		{"GET", `^/apis/metrics\.k8s\.io/v1beta1/(nodes|pods)(/[^/]+)?$`, "Kubernetes resource metrics"},
		{"GET", `^/apis/metrics\.k8s\.io/v1beta1/namespaces/[^/]+/pods(/[^/]+)?$`, "Kubernetes pod metrics"},
	}
}

// NewGuard compiles the allow-lists together with the configuration's
// narrowing-only additions.
func NewGuard(cfg config.SafetyConfig) (*Guard, error) {
	g := &Guard{
		commands:   map[string]compiledCommandRule{},
		maxBytes:   cfg.MaxResponseBytes,
		maxTimeout: cfg.MaxTimeout.D(),
	}
	if g.maxBytes <= 0 {
		g.maxBytes = 8 << 20
	}
	if g.maxTimeout <= 0 {
		g.maxTimeout = 120 * time.Second
	}

	denied := map[string]bool{}
	for _, b := range cfg.ExtraDeniedBinaries {
		denied[strings.ToLower(strings.TrimSpace(b))] = true
	}
	for _, r := range DefaultCommandRules() {
		if denied[strings.ToLower(r.Binary)] {
			continue // configuration may only narrow
		}
		c, err := compileCommandRule(r)
		if err != nil {
			return nil, errs.Wrap(err, "MAS-9001", "command rule "+r.Binary+" does not compile")
		}
		g.commands[r.Binary] = c
	}

	for _, p := range DefaultPathRules() {
		re, err := regexp.Compile(p.Pattern)
		if err != nil {
			return nil, errs.Wrap(err, "MAS-9001", "path rule "+p.Pattern+" does not compile")
		}
		g.paths = append(g.paths, compiledPathRule{method: p.Method, pattern: re, desc: p.Description})
	}

	for _, p := range append(append([]string{}, dangerousArgPatterns...), cfg.ExtraDeniedArgs...) {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, errs.Wrap(err, "MAS-9001", "denied-argument pattern "+p+" does not compile")
		}
		g.deniedArgs = append(g.deniedArgs, re)
	}
	return g, nil
}

func compileCommandRule(r CommandRule) (compiledCommandRule, error) {
	c := compiledCommandRule{
		rule:            r,
		flagsWithValue:  map[string]bool{},
		allowedVerbs:    map[string]bool{},
		deniedSequences: r.DeniedSequences,
		valueAllowList:  map[string][]*regexp.Regexp{},
	}
	for _, f := range r.FlagsWithValue {
		c.flagsWithValue[f] = true
	}
	for _, v := range r.AllowedVerbs {
		c.allowedVerbs[strings.ToLower(v)] = true
	}
	for flag, pats := range r.ValueAllowList {
		for _, p := range pats {
			re, err := regexp.Compile(p)
			if err != nil {
				return c, err
			}
			c.valueAllowList[flag] = append(c.valueAllowList[flag], re)
		}
	}
	return c, nil
}

// Authorize decides whether a call may proceed. It performs no I/O and has no
// state, so its behaviour is entirely determined by its inputs and is exhaustively
// testable (LLD §2.5 invariant).
//
// A nil return means the call is allowed. Any other return is a coded refusal.
func (g *Guard) Authorize(_ context.Context, c Call) error {
	// 1 · declared class
	if c.Class != ClassReadOnly {
		return errs.New("MAS-8001", describe(c)).With("tool", c.Tool)
	}

	// 2 · ceilings, checked before any effect-specific work
	if g.maxBytes > 0 && c.Bytes > g.maxBytes {
		return errs.New("MAS-8010",
			fmt.Sprintf("response cap of %d bytes", c.Bytes),
			fmt.Sprintf("max_response_bytes=%d", g.maxBytes)).With("tool", c.Tool)
	}
	if g.maxTimeout > 0 && c.Timeout > g.maxTimeout {
		return errs.New("MAS-8010",
			fmt.Sprintf("timeout of %s", c.Timeout),
			fmt.Sprintf("max_timeout=%s", g.maxTimeout)).With("tool", c.Tool)
	}

	// 3 · exactly one effect must be declared
	declared := 0
	for _, present := range []bool{c.HTTP != nil, c.Command != nil, c.Exec != nil, c.File != nil} {
		if present {
			declared++
		}
	}
	if declared != 1 {
		return errs.New("MAS-8005", "effect",
			fmt.Sprintf("a call must declare exactly one effect, got %d", declared)).With("tool", c.Tool)
	}

	switch {
	case c.HTTP != nil:
		return g.authorizeHTTP(c)
	case c.Command != nil:
		return g.authorizeCommand(c)
	case c.Exec != nil:
		return g.authorizeExec(c)
	default:
		return g.authorizeFile(c)
	}
}

func (g *Guard) authorizeHTTP(c Call) error {
	method := strings.ToUpper(c.HTTP.Method)
	if method != "GET" && method != "POST" {
		return errs.New("MAS-8001", method+" "+c.HTTP.URL).With("tool", c.Tool)
	}
	u, err := url.Parse(c.HTTP.URL)
	if err != nil {
		return errs.Wrap(err, "MAS-8005", c.HTTP.URL, "is not a parsable URL").With("tool", c.Tool)
	}
	if strings.Contains(u.Path, "..") {
		return errs.New("MAS-8005", u.Path, "contains path traversal").With("tool", c.Tool)
	}
	path := u.EscapedPath()
	for _, r := range g.paths {
		if r.method == method && r.pattern.MatchString(path) {
			return nil
		}
	}
	return errs.New("MAS-8003", method, path).With("tool", c.Tool)
}

func (g *Guard) authorizeCommand(c Call) error {
	bin := c.Command.Binary
	// A binary given as a path is matched on its base name, and a path that
	// escapes upward is refused outright.
	if strings.Contains(bin, "..") {
		return errs.New("MAS-8005", bin, "binary path contains traversal").With("tool", c.Tool)
	}
	base := bin
	if i := strings.LastIndexAny(bin, `/\`); i >= 0 {
		base = bin[i+1:]
	}
	rule, ok := g.commands[base]
	if !ok {
		return errs.New("MAS-8002", base).With("tool", c.Tool)
	}

	for _, a := range c.Command.Args {
		for _, re := range g.deniedArgs {
			if re.MatchString(a) {
				return errs.New("MAS-8005", a, "matches a denied-argument pattern").With("tool", c.Tool)
			}
		}
	}

	positionals, flagValues := splitArgs(c.Command.Args, rule.flagsWithValue)

	for flag, res := range rule.valueAllowList {
		v, present := flagValues[flag]
		if !present {
			continue
		}
		matched := false
		for _, re := range res {
			if re.MatchString(v) {
				matched = true
				break
			}
		}
		if !matched {
			return errs.New("MAS-8005", flag+"="+v, "value is not on the read-only allow-list").With("tool", c.Tool)
		}
	}

	// Leading dashes are stripped before the mutating-verb comparison, so a
	// mutating action expressed as a flag (`kafka-topics.sh --delete`) is
	// refused for what it does rather than merely for being unrecognised.
	for _, p := range positionals {
		low := strings.ToLower(strings.TrimLeft(p, "-"))
		for _, m := range mutatingPositionals {
			if low == m {
				return errs.New("MAS-8001", base+" "+p).With("tool", c.Tool)
			}
		}
	}

	for _, seq := range rule.deniedSequences {
		if containsSequence(positionals, seq) {
			return errs.New("MAS-8001", base+" "+strings.Join(seq, " ")).With("tool", c.Tool)
		}
	}

	if len(rule.allowedVerbs) > 0 {
		if len(positionals) == 0 {
			return errs.New("MAS-8002", base+" (no subcommand given)").With("tool", c.Tool)
		}
		if !rule.allowedVerbs[strings.ToLower(positionals[0])] {
			return errs.New("MAS-8002", base+" "+positionals[0]).With("tool", c.Tool)
		}
	}
	return nil
}

func (g *Guard) authorizeFile(c Call) error {
	p := c.File.Path
	if strings.Contains(p, "..") {
		return errs.New("MAS-8005", p, "contains path traversal").With("tool", c.Tool)
	}
	if strings.ContainsRune(p, 0) {
		return errs.New("MAS-8005", p, "contains a NUL byte").With("tool", c.Tool)
	}
	for _, forbidden := range []string{"/etc/shadow", "/etc/gshadow", "/proc/self/environ", "/root/.ssh", "/etc/sudoers"} {
		if strings.HasPrefix(p, forbidden) {
			return errs.New("MAS-8003", "READ", p).With("tool", c.Tool)
		}
	}
	if strings.Contains(p, "/.ssh/") || strings.HasSuffix(p, "/.netrc") || strings.HasSuffix(p, "id_rsa") {
		return errs.New("MAS-8003", "READ", p).With("tool", c.Tool)
	}
	return nil
}

// splitArgs separates positional arguments from flags. A flag known to consume
// a value takes the next argument with it, so `redis-cli -h host INFO` yields the
// positional "INFO" rather than "host".
func splitArgs(args []string, flagsWithValue map[string]bool) (positionals []string, flagValues map[string]string) {
	flagValues = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if eq := strings.IndexByte(a, '='); eq > 0 {
				flagValues[a[:eq]] = a[eq+1:]
				continue
			}
			if flagsWithValue[a] && i+1 < len(args) {
				flagValues[a] = args[i+1]
				i++
				continue
			}
			positionals = append(positionals, a) // a valueless flag can itself be a verb
			continue
		}
		positionals = append(positionals, a)
	}
	return positionals, flagValues
}

func containsSequence(positionals, seq []string) bool {
	if len(seq) == 0 || len(positionals) < len(seq) {
		return false
	}
	for i := 0; i+len(seq) <= len(positionals); i++ {
		match := true
		for j, want := range seq {
			if !strings.EqualFold(positionals[i+j], want) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// authorizeExec applies both constraints an exec is subject to.
//
// The command check reuses authorizeCommand verbatim rather than reimplementing
// it: the whole claim of this feature is that exec changes *where* vetted
// commands run and never *which* commands are vetted, and two copies of the
// allow-list logic would eventually make that claim false.
func (g *Guard) authorizeExec(c Call) error {
	e := c.Exec

	// 1 · path components, before anything is built from them. The path rule
	//     below is a regex over a URL assembled from these three fields; a
	//     component containing a slash or a traversal would produce a URL that
	//     still matches the rule while addressing something else entirely.
	for _, part := range []struct{ what, value string }{
		{"namespace", e.Namespace}, {"pod", e.Pod}, {"container", e.Container},
	} {
		if part.what == "container" && part.value == "" {
			continue // the pod's default container
		}
		if !validPathComponent(part.value) {
			return errs.New("MAS-8005", part.what,
				fmt.Sprintf("%q is not a valid Kubernetes name", part.value)).With("tool", c.Tool)
		}
	}

	// 2 · the command, through the same allow-list as any local command.
	if err := g.authorizeCommand(Call{
		Tool:  c.Tool,
		Class: c.Class,
		Command: &CommandEffect{
			Binary: e.Binary,
			Args:   e.Args,
		},
	}); err != nil {
		return err
	}

	// 3 · the endpoint the effect implies.
	//
	// This is checked against execPath rather than through the general HTTP
	// allow-list, and deliberately so: the exec subresource stays *absent* from
	// DefaultPathRules, so a tool that declared a plain HTTPEffect against it
	// is still refused. The endpoint is reachable only through the effect that
	// also checks the command — which is the whole point of making exec one
	// effect with two constraints.
	path := "/api/v1/namespaces/" + e.Namespace + "/pods/" + e.Pod + "/exec"
	if !execPath.MatchString(path) {
		return errs.New("MAS-8003", "GET "+path).With("tool", c.Tool)
	}
	return nil
}

// execPath is the one endpoint an ExecEffect may address. It is not part of
// DefaultPathRules: keeping it out is what stops a bare HTTP call from reaching
// exec without the command allow-list being consulted.
var execPath = regexp.MustCompile(`^/api/v1/namespaces/[^/]+/pods/[^/]+/exec$`)

// validPathComponent accepts the shape Kubernetes itself accepts for a name:
// lowercase alphanumerics, '-' and '.', bounded in length. Anything else could
// change which endpoint the assembled URL addresses.
var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func validPathComponent(v string) bool {
	if v == "" || len(v) > 253 || strings.Contains(v, "..") {
		return false
	}
	return dns1123.MatchString(v)
}

func describe(c Call) string {
	switch {
	case c.HTTP != nil:
		return strings.ToUpper(c.HTTP.Method) + " " + c.HTTP.URL
	case c.Command != nil:
		return strings.TrimSpace(c.Command.Binary + " " + strings.Join(c.Command.Args, " "))
	case c.Exec != nil:
		return "exec in " + c.Exec.Namespace + "/" + c.Exec.Pod + ": " +
			strings.TrimSpace(c.Exec.Binary+" "+strings.Join(c.Exec.Args, " "))
	case c.File != nil:
		return "read " + c.File.Path
	default:
		return "tool " + c.Tool
	}
}

// AllowedCommands reports the effective command allow-list, for `mas doctor`
// and the user manual.
func (g *Guard) AllowedCommands() []CommandRule {
	out := make([]CommandRule, 0, len(g.commands))
	for _, c := range g.commands {
		out = append(out, c.rule)
	}
	sortRules(out)
	return out
}

// AllowedPaths reports the effective HTTP allow-list.
func (g *Guard) AllowedPaths() []PathRule {
	out := make([]PathRule, 0, len(g.paths))
	for _, p := range g.paths {
		out = append(out, PathRule{Method: p.method, Pattern: p.pattern.String(), Description: p.desc})
	}
	return out
}

// Limits reports the effective ceilings.
func (g *Guard) Limits() (maxBytes int, maxTimeout time.Duration) { return g.maxBytes, g.maxTimeout }

func sortRules(r []CommandRule) {
	for i := 1; i < len(r); i++ {
		for j := i; j > 0 && r[j].Binary < r[j-1].Binary; j-- {
			r[j], r[j-1] = r[j-1], r[j]
		}
	}
}
