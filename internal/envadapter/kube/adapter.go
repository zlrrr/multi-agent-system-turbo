package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func init() {
	envadapter.Register("kubernetes", func(name string, cfg config.EnvConfig) (envadapter.Adapter, error) {
		c, err := NewClient(cfg)
		if err != nil {
			return nil, err
		}
		return &Adapter{
			name: name, client: c, defaultNS: cfg.Namespace,
			execEnabled: cfg.ExecEnabled(),
		}, nil
	})
}

// Adapter binds targets to a Kubernetes cluster.
type Adapter struct {
	name      string
	client    *Client
	defaultNS string

	// Exec state. instances is what the target resolved to, and exec is bound
	// to it: the tool takes an instance name and looks it up here, so no
	// argument can reach a pod outside the run's scope.
	inspects    map[string]InspectCommand
	instances   []core.Instance
	instanceNS  string
	execEnabled bool

	execOnce sync.Once
	exec     *ExecClient
}

// NewAdapter builds an adapter around an existing client, which is how tests
// inject a stub API server.
func NewAdapter(name string, c *Client, defaultNS string) *Adapter {
	return &Adapter{name: name, client: c, defaultNS: defaultNS, execEnabled: true}
}

// SetExecEnabled applies the environment's narrowing switch. It can only turn
// exec off: there is no path by which anything at runtime widens the guard.
func (a *Adapter) SetExecEnabled(enabled bool) { a.execEnabled = enabled }

// execClient builds the exec client once, from the read client's credentials.
func (a *Adapter) execClient() *ExecClient {
	a.execOnce.Do(func() { a.exec = NewExecClient(a.client) })
	return a.exec
}

// Name reports the environment name.
func (a *Adapter) Name() string { return a.name }

// Client exposes the underlying read-only client.
func (a *Adapter) Client() *Client { return a.client }

// Probe verifies connectivity, for `mas doctor`.
func (a *Adapter) Probe(ctx context.Context) error { return a.client.Probe(ctx) }

// Resolve locates the pods backing a target and derives its version from the
// container image tag when the configuration does not state one.
func (a *Adapter) Resolve(ctx context.Context, t config.TargetConfig) (envadapter.Binding, error) {
	ns := firstNonEmpty(t.Namespace, a.defaultNS, a.client.Namespace())
	b := envadapter.Binding{Kind: "kubernetes", Namespace: ns, Labels: t.Labels, Version: t.Version}

	pods, err := a.client.ListPods(ctx, ns, t.Selector)
	if err != nil {
		// Resolution failing is not fatal: offline analysis over telemetry is
		// still worthwhile, so the caller records a note and continues.
		b.Notes = append(b.Notes, "could not list pods: "+err.Error())
		return b, err
	}
	for _, p := range pods {
		b.Instances = append(b.Instances, core.Instance{
			Name: p.Name, Address: p.PodIP, Node: p.Node,
			Status: fmt.Sprintf("%s (ready=%v, restarts=%d)", p.Phase, p.Ready(), p.Restarts()),
			Labels: p.Labels,
		})
	}
	if b.Version == "" {
		b.Version = versionFromPods(pods)
	}
	if len(pods) == 0 {
		b.Notes = append(b.Notes,
			fmt.Sprintf("no pods matched selector %q in namespace %q", t.Selector, ns))
	}
	return b, nil
}

// versionFromPods reads the image tag, which is the version actually running —
// more trustworthy than a configured value that may have drifted.
func versionFromPods(pods []Pod) string {
	for _, p := range pods {
		for _, c := range p.Containers {
			if i := strings.LastIndex(c.Image, ":"); i > 0 {
				tag := c.Image[i+1:]
				if tag != "" && tag != "latest" && !strings.HasPrefix(tag, "sha256") {
					return tag
				}
			}
		}
	}
	return ""
}

// Tools returns the cluster-domain capabilities.
//
// The exec tool is absent entirely when the environment disabled it, rather
// than present and refusing: a capability that is not registered cannot be
// called however a prompt is phrased.
func (a *Adapter) Tools() []tool.Tool {
	out := []tool.Tool{
		&podsTool{a: a}, &podLogsTool{a: a}, &eventsTool{a: a},
		&nodesTool{a: a}, &workloadsTool{a: a},
	}
	if a.execEnabled {
		out = append(out, &execTool{a: a})
	}
	return out
}

// clusterTool carries what every cluster tool shares. All of them require online
// mode: they read the live environment rather than a telemetry snapshot.
type clusterTool struct{ a *Adapter }

func (clusterTool) Domain() tool.Domain     { return tool.DomainCluster }
func (clusterTool) Safety() safety.Class    { return safety.ClassReadOnly }
func (clusterTool) RequiredMode() core.Mode { return core.ModeOnline }
func (c clusterTool) plan(path string) safety.Call {
	return safety.Call{
		Class:   safety.ClassReadOnly,
		HTTP:    &safety.HTTPEffect{Method: "GET", URL: c.a.client.URLFor(path)},
		Timeout: c.a.client.Timeout(),
	}
}

type podsTool struct {
	clusterTool
	a *Adapter
}

func (t *podsTool) Name() string { return "kube.pods" }
func (t *podsTool) Description() string {
	return "List pods in a namespace with phase, readiness, restart counts, node placement and container state. " +
		"Use to see whether the middleware is crash-looping, pending, OOM-killed, or spread across unhealthy nodes."
}
func (t *podsTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"namespace": {Type: tool.TypeString, Description: "Namespace; defaults to the target's namespace"},
		"selector":  {Type: tool.TypeString, Description: "Label selector, e.g. app=redis"},
	})
}
func (t *podsTool) Plan(args map[string]any) (safety.Call, error) {
	return clusterTool{a: t.a}.plan(t.a.client.PodsPath(tool.Str(args, "namespace", ""))), nil
}
func (t *podsTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	ns := tool.Str(args, "namespace", "")
	pods, err := t.a.client.ListPods(ctx, ns, tool.Str(args, "selector", ""))
	if err != nil {
		return core.Evidence{}, err
	}
	unhealthy, restarts := 0, 0
	for _, p := range pods {
		if !p.Ready() || p.Phase != "Running" {
			unhealthy++
		}
		restarts += p.Restarts()
	}
	return core.Evidence{
		Kind: core.EvidenceKubeObject, Source: "kube:" + t.a.name,
		Query:   fmt.Sprintf("pods in %s selector=%q", t.a.client.ns(ns), tool.Str(args, "selector", "")),
		Payload: map[string]any{"pods": pods, "count": len(pods)},
		Summary: fmt.Sprintf("%d pods, %d not ready, %d total container restarts", len(pods), unhealthy, restarts),
	}, nil
}

type podLogsTool struct {
	clusterTool
	a *Adapter
}

func (t *podLogsTool) Name() string { return "kube.logs" }
func (t *podLogsTool) Description() string {
	return "Read a pod's container log directly from the API server, with timestamps. " +
		"Use when logs are not shipped to Loki, or to read the previous container's log after a crash (previous=true)."
}
func (t *podLogsTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"pod":       {Type: tool.TypeString, Description: "Pod name"},
		"namespace": {Type: tool.TypeString, Description: "Namespace; defaults to the target's namespace"},
		"container": {Type: tool.TypeString, Description: "Container name; defaults to the first container"},
		"tail":      {Type: tool.TypeInteger, Description: "Lines to read from the end", Default: 200, Minimum: tool.Float(1), Maximum: tool.Float(5000)},
		"previous":  {Type: tool.TypeBoolean, Description: "Read the previous container instance's log", Default: false},
		"since_seconds": {Type: tool.TypeInteger, Description: "Only lines newer than this many seconds",
			Minimum: tool.Float(1), Maximum: tool.Float(604800)},
	}, "pod")
}
func (t *podLogsTool) Plan(args map[string]any) (safety.Call, error) {
	path := t.a.client.PodLogPath(tool.Str(args, "namespace", ""), tool.Str(args, "pod", ""))
	return clusterTool{a: t.a}.plan(path), nil
}
func (t *podLogsTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	pod := tool.Str(args, "pod", "")
	ns := tool.Str(args, "namespace", "")
	opts := LogOptions{
		Container:    tool.Str(args, "container", ""),
		TailLines:    tool.Int(args, "tail", 200),
		Previous:     tool.Bool(args, "previous", false),
		SinceSeconds: tool.Int(args, "since_seconds", 0),
		LimitBytes:   1 << 20,
	}
	body, err := t.a.client.PodLogs(ctx, ns, pod, opts)
	if err != nil {
		return core.Evidence{}, err
	}
	lines := splitNonEmpty(body)
	truncated := len(body) >= opts.LimitBytes
	return core.Evidence{
		Kind: core.EvidenceLogLines, Source: "kube:" + t.a.name,
		Query:     fmt.Sprintf("logs %s/%s container=%s previous=%v", t.a.client.ns(ns), pod, opts.Container, opts.Previous),
		Payload:   map[string]any{"lines": lines, "count": len(lines)},
		Summary:   fmt.Sprintf("%d log lines from %s%s", len(lines), pod, previousSuffix(opts.Previous)),
		Truncated: truncated,
	}, nil
}

func previousSuffix(prev bool) string {
	if prev {
		return " (previous container instance)"
	}
	return ""
}

type eventsTool struct {
	clusterTool
	a *Adapter
}

func (t *eventsTool) Name() string { return "kube.events" }
func (t *eventsTool) Description() string {
	return "List Kubernetes events in a namespace, oldest first. Use to find OOMKilled, FailedScheduling, " +
		"Unhealthy probe failures, image pull errors and volume problems around the time of the symptom."
}
func (t *eventsTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"namespace": {Type: tool.TypeString, Description: "Namespace; defaults to the target's namespace"},
		"type":      {Type: tool.TypeString, Description: "Filter by event type", Enum: []string{"Normal", "Warning"}},
		"object":    {Type: tool.TypeString, Description: "Filter to one object name, e.g. redis-0"},
	})
}
func (t *eventsTool) Plan(args map[string]any) (safety.Call, error) {
	return clusterTool{a: t.a}.plan(t.a.client.EventsPath(tool.Str(args, "namespace", ""))), nil
}
func (t *eventsTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	ns := tool.Str(args, "namespace", "")
	field := ""
	if obj := tool.Str(args, "object", ""); obj != "" {
		field = "involvedObject.name=" + obj
	}
	events, err := t.a.client.ListEvents(ctx, ns, field)
	if err != nil {
		return core.Evidence{}, err
	}
	if want := tool.Str(args, "type", ""); want != "" {
		filtered := events[:0]
		for _, e := range events {
			if e.Type == want {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}
	warnings := 0
	for _, e := range events {
		if e.Type == "Warning" {
			warnings++
		}
	}
	summary := fmt.Sprintf("%d events (%d warnings)", len(events), warnings)
	if len(events) > 0 {
		last := events[len(events)-1]
		summary += fmt.Sprintf("; latest %s %s on %s: %s", last.Type, last.Reason, last.Object, truncate(last.Message, 120))
	}
	return core.Evidence{
		Kind: core.EvidenceKubeObject, Source: "kube:" + t.a.name,
		Query:   fmt.Sprintf("events in %s %s", t.a.client.ns(ns), field),
		Payload: map[string]any{"events": events, "count": len(events), "warnings": warnings},
		Summary: summary,
	}, nil
}

type nodesTool struct {
	clusterTool
	a *Adapter
}

func (t *nodesTool) Name() string { return "kube.nodes" }
func (t *nodesTool) Description() string {
	return "List cluster nodes with readiness, pressure conditions, capacity and kubelet version. " +
		"Use when a middleware problem may be caused by the node underneath it — memory pressure, disk pressure or cordoning."
}
func (t *nodesTool) ArgsSchema() tool.Schema { return tool.NewSchema(map[string]tool.Property{}) }
func (t *nodesTool) Plan(map[string]any) (safety.Call, error) {
	return clusterTool{a: t.a}.plan("/api/v1/nodes"), nil
}
func (t *nodesTool) Invoke(ctx context.Context, _ map[string]any) (core.Evidence, error) {
	nodes, err := t.a.client.ListNodes(ctx)
	if err != nil {
		return core.Evidence{}, err
	}
	notReady, pressured := 0, 0
	for _, n := range nodes {
		if n.Ready != "True" {
			notReady++
		}
		for _, cond := range []string{"MemoryPressure", "DiskPressure", "PIDPressure"} {
			if n.Conditions[cond] == "True" {
				pressured++
				break
			}
		}
	}
	return core.Evidence{
		Kind: core.EvidenceKubeObject, Source: "kube:" + t.a.name, Query: "nodes",
		Payload: map[string]any{"nodes": nodes, "count": len(nodes)},
		Summary: fmt.Sprintf("%d nodes, %d not ready, %d under resource pressure", len(nodes), notReady, pressured),
	}, nil
}

type workloadsTool struct {
	clusterTool
	a *Adapter
}

func (t *workloadsTool) Name() string { return "kube.workloads" }
func (t *workloadsTool) Description() string {
	return "List deployments and statefulsets in a namespace with desired, ready, updated and available replica counts " +
		"and their container images. Use to detect a partial rollout or a version mismatch between replicas."
}
func (t *workloadsTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"namespace": {Type: tool.TypeString, Description: "Namespace; defaults to the target's namespace"},
	})
}
func (t *workloadsTool) Plan(args map[string]any) (safety.Call, error) {
	return clusterTool{a: t.a}.plan(t.a.client.WorkloadsPath(tool.Str(args, "namespace", ""))), nil
}
func (t *workloadsTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	ns := tool.Str(args, "namespace", "")
	ws, err := t.a.client.ListWorkloads(ctx, ns)
	if err != nil {
		return core.Evidence{}, err
	}
	degraded := 0
	for _, w := range ws {
		if w.Ready < w.Replicas {
			degraded++
		}
	}
	return core.Evidence{
		Kind: core.EvidenceKubeObject, Source: "kube:" + t.a.name,
		Query:   "workloads in " + t.a.client.ns(ns),
		Payload: map[string]any{"workloads": ws, "count": len(ws)},
		Summary: fmt.Sprintf("%d workloads, %d with fewer ready replicas than desired", len(ws), degraded),
	}, nil
}

func splitNonEmpty(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// InspectCommand is a middleware inspection command a knowledge pack declares.
// It mirrors the local adapter's type rather than sharing one, because the two
// adapters are independent by design: a change to how one runs commands must
// not silently change the other.
type InspectCommand struct {
	ID          string
	Binary      string
	Args        []string
	Container   string
	Description string
}

// SetInspectCommands installs the commands the run's knowledge pack declares.
func (a *Adapter) SetInspectCommands(cmds []InspectCommand) {
	if a.inspects == nil {
		a.inspects = map[string]InspectCommand{}
	}
	for _, c := range cmds {
		a.inspects[c.ID] = c
	}
}

// InspectIDs lists the available inspection commands, sorted.
func (a *Adapter) InspectIDs() []string {
	out := make([]string, 0, len(a.inspects))
	for id := range a.inspects {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SetInstances records which pods the target resolved to. Exec is bound to this
// set: an agent that could name any pod could read any pod, so the tool takes an
// instance name and resolves it here rather than accepting a pod name
// (design-lld.md §5).
func (a *Adapter) SetInstances(ns string, instances []core.Instance) {
	a.instanceNS = ns
	a.instances = instances
}

// ExecAvailable reports whether exec can be offered, and why not when it cannot.
// `mas doctor` renders both.
func (a *Adapter) ExecAvailable() (bool, error) {
	if !a.execEnabled {
		return false, errs.New("MAS-4210", a.name)
	}
	return true, nil
}

// execTool runs one pack-declared inspection command inside a pod.
//
// It takes an instance name and a command id — never a pod name, and never an
// argument vector from the model. A model that has read a hostile log line can
// therefore ask only for a command the pack already declared, in a pod the
// target already resolved to.
type execTool struct {
	clusterTool
	a *Adapter
}

func (t *execTool) Name() string { return "kube.exec" }

func (t *execTool) Description() string {
	return "Run a middleware-specific read-only inspection command declared by the knowledge pack " +
		"inside the pod that runs it — for example Redis INFO or MongoDB rs.status(). " +
		"Only pack-declared commands that also pass the read-only allow-list can run, and only in " +
		"a pod this target resolved to; anything that would change state is refused."
}

func (t *execTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"id": {Type: tool.TypeString,
			Description: "Inspection command id from the knowledge pack, e.g. server-info"},
		"instance": {Type: tool.TypeString,
			Description: "Instance name from this target's resolved instances; defaults to the first"},
	}, "id")
}

// resolve turns the arguments into a concrete command and pod, or refuses.
func (t *execTool) resolve(args map[string]any) (InspectCommand, core.Instance, error) {
	id := tool.Str(args, "id", "")
	cmd, ok := t.a.inspects[id]
	if !ok {
		return InspectCommand{}, core.Instance{}, errs.New("MAS-8002", "inspection command "+id)
	}

	if len(t.a.instances) == 0 {
		return InspectCommand{}, core.Instance{}, errs.New("MAS-4211", "(none resolved)", t.a.name)
	}
	want := tool.Str(args, "instance", "")
	instance := t.a.instances[0]
	if want != "" {
		found := false
		for _, in := range t.a.instances {
			if in.Name == want {
				instance, found = in, true
				break
			}
		}
		if !found {
			// The refusal names the target rather than listing pods: an agent
			// that guessed a pod name learns nothing from being told it guessed
			// wrong.
			return InspectCommand{}, core.Instance{}, errs.New("MAS-4211", want, t.a.name)
		}
	}

	out := InspectCommand{ID: cmd.ID, Binary: cmd.Binary, Container: cmd.Container,
		Description: cmd.Description}
	out.Args = substituteInContainer(cmd.Args)
	return out, instance, nil
}

// substituteInContainer rewrites a pack's argument template for execution
// *inside* the container the middleware runs in.
//
// The host is always the loopback address there. The port is not known — an
// instance carries a pod IP, not a port — and that is fine, because a client
// run inside the container reaches its own server on the default port with no
// flag at all. What must not happen is dropping an empty value while keeping the
// flag that introduced it: `redis-cli -h 127.0.0.1 -p INFO all` makes `-p`
// swallow `INFO`, and the guard then sees a first positional of `all`, refuses
// it, and reports something that looks like a bad allow-list rather than a bad
// argument vector. So a flag whose value disappears goes with it.
func substituteInContainer(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.Contains(a, "{{.port}}") {
			// Drop the value and the flag that introduced it.
			if n := len(out); n > 0 && strings.HasPrefix(out[n-1], "-") {
				out = out[:n-1]
			}
			continue
		}
		a = strings.ReplaceAll(a, "{{.host}}", "127.0.0.1")
		if strings.TrimSpace(a) == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (t *execTool) Plan(args map[string]any) (safety.Call, error) {
	cmd, instance, err := t.resolve(args)
	if err != nil {
		return safety.Call{}, err
	}
	return safety.Call{
		Class:   safety.ClassReadOnly,
		Timeout: t.a.client.Timeout(),
		Exec: &safety.ExecEffect{
			Namespace: t.a.instanceNS,
			Pod:       instance.Name,
			Container: cmd.Container,
			Binary:    cmd.Binary,
			Args:      cmd.Args,
		},
	}, nil
}

func (t *execTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	cmd, instance, err := t.resolve(args)
	if err != nil {
		return core.Evidence{}, err
	}

	res, err := t.a.execClient().Run(ctx, ExecRequest{
		Namespace: t.a.instanceNS,
		Pod:       instance.Name,
		Container: cmd.Container,
		Command:   append([]string{cmd.Binary}, cmd.Args...),
		MaxBytes:  execOutputCeiling,
	})
	if err != nil {
		// The output collected before the failure is still worth reporting, but
		// the caller decides that: returning the error lets the invoker record
		// the gap with its code.
		return core.Evidence{}, err
	}

	summary := fmt.Sprintf("%s in %s → %d lines", cmd.ID, instance.Name,
		strings.Count(res.Stdout, "\n"))
	if res.ExitCode != 0 {
		summary += fmt.Sprintf(" (exit %d)", res.ExitCode)
	}
	if res.Truncated {
		summary += " (truncated)"
	}
	return core.Evidence{
		Kind:   core.EvidenceCommandOutput,
		Source: "kube:" + t.a.name,
		Query:  cmd.Binary + " " + strings.Join(cmd.Args, " ") + " in " + instance.Name,
		Payload: map[string]any{
			"output":     res.Stdout,
			"stderr":     res.Stderr,
			"exit_code":  res.ExitCode,
			"command_id": cmd.ID,
			"pod":        instance.Name,
			"namespace":  t.a.instanceNS,
		},
		Summary:   summary,
		Truncated: res.Truncated,
	}, nil
}

// execOutputCeiling bounds one command's output. `INFO all` is a few kilobytes;
// anything far past that is a log file someone pointed a tool at, and it would
// crowd the evidence a report can actually use.
const execOutputCeiling = 256 * 1024
