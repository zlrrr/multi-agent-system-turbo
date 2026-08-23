package local

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func init() {
	envadapter.Register("local", func(name string, cfg config.EnvConfig) (envadapter.Adapter, error) {
		return &Adapter{name: name, runner: ExecRunner{}}, nil
	})
}

// InspectCommand is a middleware inspection command declared by a knowledge
// pack. The guard re-validates it at call time, so a pack cannot introduce a
// mutating command (design-hld.md §7.3.3).
type InspectCommand struct {
	ID          string
	Binary      string
	Args        []string
	Description string
}

// Adapter inspects the local host.
type Adapter struct {
	name     string
	runner   Runner
	inspects map[string]InspectCommand
}

// NewAdapter builds an adapter with an explicit runner, which is how tests
// substitute a stub.
func NewAdapter(name string, r Runner) *Adapter {
	if r == nil {
		r = ExecRunner{}
	}
	return &Adapter{name: name, runner: r, inspects: map[string]InspectCommand{}}
}

// Name reports the environment name.
func (a *Adapter) Name() string { return a.name }

// SetInspectCommands installs the commands a knowledge pack declares for the
// middleware under diagnosis.
func (a *Adapter) SetInspectCommands(cmds []InspectCommand) {
	if a.inspects == nil {
		a.inspects = map[string]InspectCommand{}
	}
	for _, c := range cmds {
		a.inspects[c.ID] = c
	}
}

// InspectIDs lists the available inspection commands.
func (a *Adapter) InspectIDs() []string {
	out := make([]string, 0, len(a.inspects))
	for id := range a.inspects {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Probe verifies the host can be inspected at all.
func (a *Adapter) Probe(ctx context.Context) error {
	if !Supported() {
		return errs.New("MAS-4303", "host inspection", "this platform")
	}
	if _, err := a.runner.LookPath("ps"); err != nil {
		return err
	}
	return nil
}

// Resolve locates the processes backing a target.
func (a *Adapter) Resolve(ctx context.Context, t config.TargetConfig) (envadapter.Binding, error) {
	b := envadapter.Binding{Kind: "local", Labels: t.Labels, Version: t.Version}
	procs, err := a.processes(ctx, t.Kind)
	if err != nil {
		b.Notes = append(b.Notes, "could not list processes: "+err.Error())
		return b, err
	}
	for _, p := range procs {
		b.Instances = append(b.Instances, core.Instance{
			Name:   fmt.Sprintf("%s[%d]", p.Command, p.PID),
			Status: fmt.Sprintf("rss=%dKiB cpu=%.1f%%", p.RSSKiB, p.CPUPercent),
		})
	}
	for _, h := range t.Hosts {
		b.Instances = append(b.Instances, core.Instance{Name: h, Address: h})
	}
	if len(procs) == 0 {
		b.Notes = append(b.Notes, fmt.Sprintf("no running process matched %q on this host", t.Kind))
	}
	return b, nil
}

// Process is one running process.
type Process struct {
	PID        int     `json:"pid"`
	Command    string  `json:"command"`
	CPUPercent float64 `json:"cpu_percent"`
	MemPercent float64 `json:"mem_percent"`
	RSSKiB     int     `json:"rss_kib"`
	Args       string  `json:"args,omitempty"`
}

func (a *Adapter) processes(ctx context.Context, match string) ([]Process, error) {
	out, err := a.runner.Run(ctx, "ps", []string{"-eo", "pid,pcpu,pmem,rss,comm,args"})
	if err != nil {
		return nil, err
	}
	return parseProcesses(out, match), nil
}

func parseProcesses(out, match string) []Process {
	var procs []Process
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[1], 64)
		mem, _ := strconv.ParseFloat(fields[2], 64)
		rss, _ := strconv.Atoi(fields[3])
		p := Process{PID: pid, CPUPercent: cpu, MemPercent: mem, RSSKiB: rss, Command: fields[4]}
		if len(fields) > 5 {
			p.Args = strings.Join(fields[5:], " ")
		}
		if match != "" && !strings.Contains(strings.ToLower(p.Command+" "+p.Args), strings.ToLower(match)) {
			continue
		}
		procs = append(procs, p)
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].RSSKiB > procs[j].RSSKiB })
	return procs
}

// Socket is one listening socket.
type Socket struct {
	Protocol string `json:"protocol"`
	Local    string `json:"local"`
	Process  string `json:"process,omitempty"`
}

func parseSockets(out string) []Socket {
	var socks []Socket
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 5 {
			continue
		}
		if !strings.Contains(strings.ToUpper(line), "LISTEN") {
			continue
		}
		s := Socket{Protocol: fields[0], Local: fields[4]}
		if len(fields) > 6 {
			s.Process = fields[len(fields)-1]
		}
		socks = append(socks, s)
	}
	sort.Slice(socks, func(i, j int) bool { return socks[i].Local < socks[j].Local })
	return socks
}

// Tools returns the host-domain capabilities.
func (a *Adapter) Tools() []tool.Tool {
	return []tool.Tool{
		&processesTool{a: a}, &portsTool{a: a}, &resourcesTool{a: a}, &inspectTool{a: a},
	}
}

type hostTool struct{ a *Adapter }

func (hostTool) Domain() tool.Domain     { return tool.DomainHost }
func (hostTool) Safety() safety.Class    { return safety.ClassReadOnly }
func (hostTool) RequiredMode() core.Mode { return core.ModeOnline }

func planCommand(binary string, args []string) safety.Call {
	return safety.Call{
		Class:   safety.ClassReadOnly,
		Command: &safety.CommandEffect{Binary: binary, Args: args},
		Timeout: DefaultTimeout,
	}
}

type processesTool struct {
	hostTool
	a *Adapter
}

func (t *processesTool) Name() string { return "local.processes" }
func (t *processesTool) Description() string {
	return "List processes on this host with CPU, memory and resident-set size, optionally filtered by name. " +
		"Use when the middleware runs as a plain binary rather than in Kubernetes, to confirm it is running and how much memory it holds."
}
func (t *processesTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"match": {Type: tool.TypeString, Description: "Case-insensitive substring to match against the command and its arguments"},
	})
}
func (t *processesTool) Plan(map[string]any) (safety.Call, error) {
	return planCommand("ps", []string{"-eo", "pid,pcpu,pmem,rss,comm,args"}), nil
}
func (t *processesTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	match := tool.Str(args, "match", "")
	procs, err := t.a.processes(ctx, match)
	if err != nil {
		return core.Evidence{}, err
	}
	summary := fmt.Sprintf("%d processes match %q", len(procs), match)
	if len(procs) > 0 {
		summary += fmt.Sprintf("; largest is %s (pid %d) at %d KiB RSS",
			procs[0].Command, procs[0].PID, procs[0].RSSKiB)
	}
	return core.Evidence{
		Kind: core.EvidenceHostState, Source: "local:" + t.a.name,
		Query: "ps match=" + match, Payload: map[string]any{"processes": procs, "count": len(procs)},
		Summary: summary,
	}, nil
}

type portsTool struct {
	hostTool
	a *Adapter
}

func (t *portsTool) Name() string { return "local.ports" }
func (t *portsTool) Description() string {
	return "List listening TCP sockets on this host and the processes holding them. " +
		"Use to confirm the middleware is accepting connections on the expected port, or to find a port conflict."
}
func (t *portsTool) ArgsSchema() tool.Schema { return tool.NewSchema(map[string]tool.Property{}) }
func (t *portsTool) Plan(map[string]any) (safety.Call, error) {
	return planCommand("ss", []string{"-lntp"}), nil
}
func (t *portsTool) Invoke(ctx context.Context, _ map[string]any) (core.Evidence, error) {
	out, err := t.a.runner.Run(ctx, "ss", []string{"-lntp"})
	if err != nil {
		return core.Evidence{}, err
	}
	socks := parseSockets(out)
	return core.Evidence{
		Kind: core.EvidenceHostState, Source: "local:" + t.a.name, Query: "ss -lntp",
		Payload: map[string]any{"sockets": socks, "count": len(socks)},
		Summary: fmt.Sprintf("%d listening sockets", len(socks)),
	}, nil
}

type resourcesTool struct {
	hostTool
	a *Adapter
}

func (t *resourcesTool) Name() string { return "local.resources" }
func (t *resourcesTool) Description() string {
	return "Report host memory, filesystem usage and load average. " +
		"Use to establish whether the host itself is out of memory or disk before blaming the middleware."
}
func (t *resourcesTool) ArgsSchema() tool.Schema { return tool.NewSchema(map[string]tool.Property{}) }
func (t *resourcesTool) Plan(map[string]any) (safety.Call, error) {
	return planCommand("free", []string{"-m"}), nil
}
func (t *resourcesTool) Invoke(ctx context.Context, _ map[string]any) (core.Evidence, error) {
	payload := map[string]any{}
	var missing []string
	for name, spec := range map[string][]string{
		"memory": {"free", "-m"},
		"disk":   {"df", "-h"},
		"load":   {"uptime"},
	} {
		out, err := t.a.runner.Run(ctx, spec[0], spec[1:])
		if err != nil {
			missing = append(missing, name)
			continue
		}
		payload[name] = strings.TrimSpace(out)
	}
	sort.Strings(missing)
	if len(payload) == 0 {
		return core.Evidence{}, errs.New("MAS-4301", "free/df/uptime", "no host resource command succeeded")
	}
	summary := fmt.Sprintf("host resources collected: %d of 3 checks", len(payload))
	if len(missing) > 0 {
		summary += " (missing: " + strings.Join(missing, ", ") + ")"
	}
	return core.Evidence{
		Kind: core.EvidenceHostState, Source: "local:" + t.a.name, Query: "free -m; df -h; uptime",
		Payload: payload, Summary: summary, Truncated: len(missing) > 0,
	}, nil
}

type inspectTool struct {
	hostTool
	a *Adapter
}

func (t *inspectTool) Name() string { return "local.inspect" }
func (t *inspectTool) Description() string {
	return "Run a middleware-specific read-only inspection command declared by the knowledge pack — for example " +
		"Redis INFO or Kafka consumer-group describe. Only pack-declared commands that also pass the read-only " +
		"allow-list can run; anything that would change state is refused."
}
func (t *inspectTool) ArgsSchema() tool.Schema {
	return tool.NewSchema(map[string]tool.Property{
		"id":   {Type: tool.TypeString, Description: "Inspection command id from the knowledge pack, e.g. info"},
		"host": {Type: tool.TypeString, Description: "Host to inspect; substituted into the command template"},
		"port": {Type: tool.TypeString, Description: "Port to inspect; substituted into the command template"},
	}, "id")
}

func (t *inspectTool) resolve(args map[string]any) (InspectCommand, error) {
	id := tool.Str(args, "id", "")
	cmd, ok := t.a.inspects[id]
	if !ok {
		return InspectCommand{}, errs.New("MAS-8002", "inspection command "+id)
	}
	subs := map[string]string{
		"{{.host}}": tool.Str(args, "host", "127.0.0.1"),
		"{{.port}}": tool.Str(args, "port", ""),
	}
	out := InspectCommand{ID: cmd.ID, Binary: cmd.Binary, Description: cmd.Description}
	for _, a := range cmd.Args {
		for k, v := range subs {
			a = strings.ReplaceAll(a, k, v)
		}
		if a == "" {
			continue
		}
		out.Args = append(out.Args, a)
	}
	return out, nil
}

func (t *inspectTool) Plan(args map[string]any) (safety.Call, error) {
	cmd, err := t.resolve(args)
	if err != nil {
		return safety.Call{}, err
	}
	return planCommand(cmd.Binary, cmd.Args), nil
}

func (t *inspectTool) Invoke(ctx context.Context, args map[string]any) (core.Evidence, error) {
	cmd, err := t.resolve(args)
	if err != nil {
		return core.Evidence{}, err
	}
	out, err := t.a.runner.Run(ctx, cmd.Binary, cmd.Args)
	if err != nil {
		return core.Evidence{}, err
	}
	lines := strings.Count(out, "\n")
	return core.Evidence{
		Kind: core.EvidenceCommandOutput, Source: "local:" + t.a.name,
		Query:   cmd.Binary + " " + strings.Join(cmd.Args, " "),
		Payload: map[string]any{"output": out, "command_id": cmd.ID},
		Summary: fmt.Sprintf("%s → %d lines of output", cmd.ID, lines),
	}, nil
}
