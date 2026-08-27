package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/eval"
	"github.com/zlrrr/multi-agent-system-turbo/internal/httpapi"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/report"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func newDiagnoseCmd(e *env) *cobra.Command {
	var (
		target, symptom, since, from, to string
		mode, topology, format, output   string
		forceAgents                      bool
	)
	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Analyse a runtime problem in a configured middleware target",
		Long: `Runs a diagnosis: deterministic playbook checks first, then — only where those
are inconclusive — a multi-agent investigation over the same evidence.

Every recommendation in the report is advisory. MAS-Turbo performs no action
against the target.`,
		Example: `  mas diagnose --target redis-prod --symptom "p99 latency spike" --since 1h
  mas diagnose --target kafka-prod --symptom "consumer lag growing" --topology single
  mas diagnose --target redis-prod --symptom "OOM errors" --mode online --format json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			req := core.DiagnoseRequest{
				Target: target, Symptom: symptom,
				Mode: core.Mode(mode), Topology: topology, Language: e.g.language,
			}
			if req.Window, err = parseWindow(since, from, to); err != nil {
				return err
			}
			if forceAgents {
				req.Options = map[string]string{"force_agents": "true"}
			}

			rep, err := svc.Diagnose(cmd.Context(), req)
			if err != nil {
				return err
			}
			return emitReport(e, rep, format, output, svc.Config().Run.Language)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&target, "target", "t", "", "target id from the configuration (required)")
	f.StringVarP(&symptom, "symptom", "s", "", "what you observed, in your own words (required)")
	f.StringVar(&since, "since", "", "look back this far, e.g. 30m, 1h, 24h (default: run.default_window)")
	f.StringVar(&from, "from", "", "window start as RFC3339")
	f.StringVar(&to, "to", "", "window end as RFC3339")
	f.StringVar(&mode, "mode", "", "offline (telemetry only) or online (also read the live environment)")
	f.StringVar(&topology, "topology", "", "agent topology: run `mas topologies` to list")
	f.StringVarP(&format, "format", "f", "markdown", "output format: markdown, json or text")
	f.StringVarP(&output, "output", "o", "", "write the report to a file instead of stdout")
	f.BoolVar(&forceAgents, "force-agents", false, "run the agent phase even when a deterministic check is conclusive")
	_ = cmd.MarkFlagRequired("target")
	_ = cmd.MarkFlagRequired("symptom")
	return cmd
}

func parseWindow(since, from, to string) (core.Window, error) {
	var w core.Window
	if from != "" || to != "" {
		if from == "" || to == "" {
			return w, errs.New("MAS-1010", "--from and --to must be given together")
		}
		f, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return w, errs.New("MAS-1010", "--from must be RFC3339, got "+from)
		}
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return w, errs.New("MAS-1010", "--to must be RFC3339, got "+to)
		}
		w.From, w.To = f, t
		return w, w.Validate()
	}
	if since != "" {
		d, err := time.ParseDuration(since)
		if err != nil {
			return w, errs.New("MAS-1010", "--since must be a duration such as 1h, got "+since)
		}
		if d <= 0 {
			return w, errs.New("MAS-1010", "--since must be positive")
		}
		now := time.Now().UTC()
		w.From, w.To = now.Add(-d), now
	}
	return w, nil // an empty window is filled in by admission
}

func emitReport(e *env, rep *core.Report, format, output, lang string) error {
	var (
		body []byte
		err  error
	)
	switch strings.ToLower(format) {
	case "json":
		body, err = report.JSON(rep)
	case "text", "txt":
		body, err = report.Text(rep, lang)
	case "markdown", "md", "":
		body, err = report.Markdown(rep, lang)
	default:
		return errs.New("MAS-1007", fmt.Sprintf("format %q is not one of markdown, json, text", format))
	}
	if err != nil {
		return err
	}
	if output == "" {
		_, err = e.out.Write(body)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil && filepath.Dir(output) != "." {
		return errs.Wrap(err, "MAS-6002", output, err.Error())
	}
	if err := os.WriteFile(output, body, 0o640); err != nil {
		return errs.Wrap(err, "MAS-6002", output, err.Error())
	}
	fmt.Fprintf(e.out, "report written to %s (run %s)\n", output, rep.RunID)
	return nil
}

func newServeCmd(e *env) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API",
		Long: `Serves the diagnosis API, health endpoints and Prometheus self-metrics.

Endpoints:
  POST /api/v1/diagnoses            create a diagnosis (?wait=true to block)
  GET  /api/v1/diagnoses            list runs
  GET  /api/v1/diagnoses/{id}       fetch a run and its report
  GET  /api/v1/targets              configured targets
  GET  /api/v1/topologies           available topologies
  GET  /api/v1/packs                loaded knowledge packs
  GET  /ui/                         read-only web console
  GET  /healthz  /readyz  /metrics`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()
			if addr != "" {
				svc.Config().Server.Addr = addr
			}
			// Admission before the announcement, not after: a configuration
			// that will be refused used to print "listening on …" and then
			// refuse, which reads as a crash rather than as the deliberate
			// stop it is. Serve checks this again — it is the gate, and this
			// call is only about what the operator is told.
			if err := httpapi.Admit(svc.Config().Server); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "listening on %s\n", svc.Config().Server.Addr)
			if svc.Config().Server.UI.On() {
				fmt.Fprintf(e.out, "web console at %s/ui/\n", svc.Config().Server.Addr)
			}
			return httpapi.Serve(cmd.Context(), svc)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "listen address, e.g. :8080")
	return cmd
}

func newDoctorCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate the configuration and probe every configured endpoint",
		Long: `Checks the configuration, the knowledge packs, the safety guard, every telemetry
source, every environment, the model provider and the run store — and reports all
of them, not just the first problem.

Exit status is non-zero if any check fails outright.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			results := svc.Doctor(cmd.Context())
			if asJSON {
				if err := writeJSON(e.out, results); err != nil {
					return err
				}
			} else {
				w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "STATUS\tCHECK\tDETAIL")
				for _, r := range results {
					detail := r.Detail
					if r.Code != "" {
						detail = r.Code + "  " + detail
					}
					fmt.Fprintf(w, "%s\t%s\t%s\n", statusGlyph(r.Status), r.Name, detail)
				}
				_ = w.Flush()
				for _, r := range results {
					if r.Status == service.CheckFail && r.Remedy != "" {
						fmt.Fprintf(e.out, "\n%s: %s\n", r.Name, r.Remedy)
					}
				}
			}
			if !service.DoctorOK(results) {
				return errs.New("MAS-1003", "doctor", "one or more checks failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func statusGlyph(s service.CheckStatus) string {
	switch s {
	case service.CheckOK:
		return "ok"
	case service.CheckWarn:
		return "warn"
	case service.CheckFail:
		return "FAIL"
	default:
		return "skip"
	}
}

func newReplayCmd(e *env) *cobra.Command {
	var format, output string
	var withSteps bool
	cmd := &cobra.Command{
		Use:   "replay <run-id>",
		Short: "Reproduce a stored run's report without contacting anything",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			if withSteps {
				rec, err := svc.Run(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return writeJSON(e.out, rec)
			}
			rep, err := svc.Replay(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emitReport(e, rep, format, output, svc.Config().Run.Language)
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "markdown", "output format: markdown, json or text")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file instead of stdout")
	cmd.Flags().BoolVar(&withSteps, "steps", false, "print the full run record including every step")
	return cmd
}

func newRunsCmd(e *env) *cobra.Command {
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List stored diagnostic runs, newest first",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			runs, err := svc.Runs(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(e.out, runs)
			}
			if len(runs) == 0 {
				fmt.Fprintln(e.out, "no runs stored yet")
				return nil
			}
			w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "RUN\tSTATUS\tTARGET\tTOPOLOGY\tHYPOTHESES\tSTARTED\tSYMPTOM")
			for _, r := range runs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
					r.ID, r.Status, r.Target, r.Topology, r.Hypotheses,
					r.StartedAt.Format(time.RFC3339), truncate(r.Symptom, 40))
			}
			return w.Flush()
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "maximum runs to list")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func newTargetsCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List configured diagnosis targets",
		RunE: func(_ *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			targets := svc.Config().Targets
			if asJSON {
				return writeJSON(e.out, targets)
			}
			if len(targets) == 0 {
				fmt.Fprintln(e.out, "no targets configured; add a `targets:` entry to mas.yaml")
				return nil
			}
			w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tKIND\tVERSION\tENVIRONMENT\tSELECTOR")
			for _, t := range targets {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Kind, orDash(t.Version), orDash(t.Env), orDash(t.Selector))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func newTopologiesCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "topologies",
		Short: "List the available agent topologies",
		Long: `Topologies are interchangeable: the same request, evidence and tools run through
a different arrangement of agents. Selecting one per run is what makes them
comparable on identical cases.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if asJSON {
				// JSON carries both languages: a caller may not be the operator.
				return writeJSON(e.out, orchestrator.Details())
			}
			descs := orchestrator.Descriptions(e.lang())
			for _, n := range orchestrator.Names() {
				fmt.Fprintf(e.out, "%s\n", n)
				// Each of Summary/Cost/Choose/Avoid is its own line: run
				// together they read as one paragraph, and the whole point of
				// the last two is that an operator can find them at a glance.
				for _, line := range strings.Split(descs[n], "\n") {
					fmt.Fprintf(e.out, "    %s\n", wrap(line, 92, "      "))
				}
				fmt.Fprintln(e.out)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func newPacksCmd(e *env) *cobra.Command {
	var asJSON bool
	var showID string
	var version string
	cmd := &cobra.Command{
		Use:   "packs",
		Short: "List loaded knowledge packs, or print one in detail",
		RunE: func(_ *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			lib := svc.Library()
			if showID != "" {
				for _, p := range lib.All() {
					if p.ID() != showID && p.Metadata.Middleware != showID {
						continue
					}
					// With a version, print what a diagnosis on it would
					// actually use, and what it would skip. Same resolution,
					// same sentences as a report — a preview that reimplemented
					// either would be a second thing to keep in step.
					var gaps []core.Gap
					if version != "" {
						p, gaps = p.Resolve(version)
					}
					if asJSON {
						return writeJSON(e.out, struct {
							Pack    any        `json:"pack"`
							Skipped []core.Gap `json:"skipped,omitempty"`
						}{Pack: p, Skipped: gaps})
					}
					fmt.Fprintln(e.out, p.Summary(svc.Config().Run.Language))
					for _, g := range gaps {
						fmt.Fprintf(e.out, "%s  %s\n", g.Code, g.Detail)
						if g.Impact != "" {
							fmt.Fprintf(e.out, "          %s\n", wrap(g.Impact, 92, "          "))
						}
					}
					return nil
				}
				return errs.New("MAS-5003", showID)
			}
			if asJSON {
				return writeJSON(e.out, lib.All())
			}
			w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PACK\tMIDDLEWARE\tVERSIONS\tSIGNALS\tPATTERNS\tFAILURE MODES\tPLAYBOOKS\tSOURCE")
			for _, p := range lib.All() {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
					p.Metadata.Name, p.Metadata.Middleware, orDash(p.Metadata.VersionRange),
					len(p.Signals), len(p.LogPatterns), len(p.FailureModes), len(p.Playbooks),
					p.Metadata.Source)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			for _, prob := range lib.Problems() {
				fmt.Fprintf(e.errOut, "warning: %v\n", prob)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	cmd.Flags().StringVar(&showID, "show", "", "print one pack in detail, by id or middleware")
	cmd.Flags().StringVar(&version, "version", "",
		"with --show, resolve the pack for this middleware version and report what it skips")
	return cmd
}

func newToolsCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List the read-only capabilities the safety guard permits",
		Long: `Shows the command and HTTP allow-lists the guard enforces.

Nothing outside these lists can be executed or requested. Extending them is a
specification change, not a runtime option.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			g := svc.Guard()
			if asJSON {
				maxBytes, maxTimeout := g.Limits()
				return writeJSON(e.out, map[string]any{
					"commands": g.AllowedCommands(), "paths": g.AllowedPaths(),
					"max_response_bytes": maxBytes, "max_timeout": maxTimeout.String(),
				})
			}
			fmt.Fprintln(e.out, "Allow-listed commands:")
			w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
			for _, c := range g.AllowedCommands() {
				verbs := "(any read-only argument)"
				if len(c.AllowedVerbs) > 0 {
					verbs = strings.Join(c.AllowedVerbs, ", ")
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\n", c.Binary, c.Description, truncate(verbs, 70))
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(e.out, "\nAllow-listed read paths:")
			w2 := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
			for _, p := range g.AllowedPaths() {
				fmt.Fprintf(w2, "  %s\t%s\t%s\n", p.Method, p.Description, p.Pattern)
			}
			if err := w2.Flush(); err != nil {
				return err
			}
			maxBytes, maxTimeout := g.Limits()
			fmt.Fprintf(e.out, "\nCeilings: %d bytes per response, %s per call\n", maxBytes, maxTimeout)
			fmt.Fprintln(e.out, "\nProviders:", strings.Join(llm.Names(), ", "))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func newErrCodesCmd(e *env) *cobra.Command {
	var format, lang, filter string
	cmd := &cobra.Command{
		Use:   "errcodes",
		Short: "Print the MAS-NNNN error-code registry",
		Long: `Every error this tool surfaces carries a stable code with a bilingual message and
a remediation hint. This command prints the whole registry, and generates the
reference documentation.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			all := errs.All()
			if filter != "" {
				var kept []errs.Definition
				for _, d := range all {
					if strings.Contains(strings.ToLower(d.Code+d.Symbol+d.MessageEN), strings.ToLower(filter)) {
						kept = append(kept, d)
					}
				}
				all = kept
			}
			if lang == "" {
				lang = e.lang()
			}
			switch format {
			case "json":
				return writeJSON(e.out, all)
			case "markdown", "md":
				return writeErrCodeMarkdown(e.out, all, lang)
			default:
				w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "CODE\tSEVERITY\tDOMAIN\tSYMBOL\tMESSAGE")
				for _, d := range all {
					msg := d.MessageEN
					if lang == "zh" {
						msg = d.MessageZH
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
						d.Code, d.Severity, errs.Domain(d.Code), d.Symbol, msg)
				}
				return w.Flush()
			}
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "table", "output format: table, markdown or json")
	cmd.Flags().StringVar(&lang, "lang", "", "message language: en or zh")
	cmd.Flags().StringVar(&filter, "filter", "", "only codes matching this substring")
	return cmd
}

var domainTitles = map[string]struct{ en, zh string }{
	"config":        {"Configuration and request", "配置与请求"},
	"llm":           {"LLM providers", "LLM 供应商"},
	"orchestration": {"Agents and orchestration", "Agent 与编排"},
	"collector":     {"Collectors, adapters and source", "采集器、适配器与源码"},
	"knowledge":     {"Knowledge packs and rules", "知识包与规则"},
	"storage":       {"Run storage", "运行存储"},
	"interface":     {"API and CLI", "API 与 CLI"},
	"safety":        {"Safety guard", "安全守卫"},
	"internal":      {"Internal", "内部错误"},
}

var domainOrder = []string{"config", "llm", "orchestration", "collector",
	"knowledge", "storage", "interface", "safety", "internal"}

func writeErrCodeMarkdown(w io.Writer, defs []errs.Definition, lang string) error {
	zh := lang == "zh"
	byDomain := map[string][]errs.Definition{}
	for _, d := range defs {
		dom := errs.Domain(d.Code)
		byDomain[dom] = append(byDomain[dom], d)
	}
	if zh {
		fmt.Fprintln(w, "# 错误码参考")
		fmt.Fprintln(w, "\n> 本文件由 `mas errcodes --format markdown --lang zh` 生成，请勿手工编辑。")
		fmt.Fprintln(w, "> 双语对应文件：[`../en/error-codes.md`](../en/error-codes.md)")
		fmt.Fprintln(w, "\n每个跨越边界的错误都携带一个稳定的 `MAS-NNNN` 错误码。错误码按域分段（宪章第五条）。")
	} else {
		fmt.Fprintln(w, "# Error-code reference")
		fmt.Fprintln(w, "\n> Generated by `mas errcodes --format markdown --lang en`. Do not edit by hand.")
		fmt.Fprintln(w, "> Bilingual pair: [`../zh/error-codes.md`](../zh/error-codes.md)")
		fmt.Fprintln(w, "\nEvery error crossing a boundary carries a stable `MAS-NNNN` code, allocated by domain")
		fmt.Fprintln(w, "(Constitution Article V).")
	}
	for _, dom := range domainOrder {
		list := byDomain[dom]
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Code < list[j].Code })
		title := domainTitles[dom].en
		if zh {
			title = domainTitles[dom].zh
		}
		fmt.Fprintf(w, "\n## %s\n\n", title)
		if zh {
			fmt.Fprintln(w, "| 错误码 | 严重级别 | 符号 | 含义 | 处理建议 |")
		} else {
			fmt.Fprintln(w, "| Code | Severity | Symbol | Meaning | What to do |")
		}
		fmt.Fprintln(w, "|---|---|---|---|---|")
		for _, d := range list {
			msg, remedy := d.MessageEN, d.RemedyEN
			if zh {
				msg, remedy = d.MessageZH, d.RemedyZH
			}
			fmt.Fprintf(w, "| `%s` | %s | `%s` | %s | %s |\n",
				d.Code, d.Severity, d.Symbol, escapePipes(msg), escapePipes(remedy))
		}
	}
	return nil
}

func escapePipes(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func newConfigCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the effective configuration with every secret redacted",
		RunE: func(_ *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()
			body, err := svc.Config().Dump()
			if err != nil {
				return err
			}
			_, err = e.out.Write(body)
			return err
		},
	}
	return cmd
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errs.Wrap(err, "MAS-9002", err.Error())
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// wrap re-flows text to a width, indenting continuation lines.
// wrap breaks text to a display width.
//
// Width is measured in terminal columns, not bytes: a CJK character occupies two
// columns and three bytes, so a byte count wraps Chinese at a third of the
// intended width. CJK also has no spaces, so a break is allowed between two wide
// characters — otherwise a Chinese paragraph is one unbreakable word and does
// not wrap at all. Both matter in a product that ships every string twice.
func wrap(s string, width int, indent string) string {
	toks := tokenize(s)
	if len(toks) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for _, tk := range toks {
		gap := 0
		if tk.spaced && lineLen > 0 {
			gap = 1
		}
		// Chinese typesetting forbids a line that begins with closing
		// punctuation. Allowing one column of overflow keeps 。 or ）with the
		// clause it closes, which is what a reader expects.
		if noBreakBefore(tk.text) {
			b.WriteString(tk.text)
			lineLen += tk.width
			continue
		}
		if lineLen > 0 && lineLen+gap+tk.width > width {
			b.WriteString("\n" + indent)
			lineLen = 0
			gap = 0
		} else if gap == 1 {
			b.WriteString(" ")
			lineLen++
		}
		b.WriteString(tk.text)
		lineLen += tk.width
	}
	return b.String()
}

// token is one unbreakable unit: a run of narrow characters, or a single wide
// one. spaced records whether whitespace preceded it in the source, which is
// what decides whether a space is reinserted when it does not start a line.
type token struct {
	text   string
	width  int
	spaced bool
}

func tokenize(s string) []token {
	var (
		out     []token
		cur     strings.Builder
		curW    int
		pending bool // whitespace seen since the last token
	)
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out = append(out, token{text: cur.String(), width: curW, spaced: pending})
		cur.Reset()
		curW = 0
		pending = false
	}
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
			pending = true
		case isWide(r):
			flush()
			out = append(out, token{text: string(r), width: 2, spaced: pending})
			pending = false
		default:
			cur.WriteRune(r)
			curW++
		}
	}
	flush()
	return out
}

// noBreakBefore reports whether a token may not start a line: CJK closing
// punctuation belongs to the clause it ends.
func noBreakBefore(text string) bool {
	r := []rune(text)
	if len(r) != 1 {
		return false
	}
	return strings.ContainsRune("。，、；：？！）】》」』〉〕｝”’·…—", r[0])
}

// isWide reports whether a rune occupies two terminal columns. The ranges cover
// what this project actually emits — CJK ideographs, kana, Hangul, fullwidth
// forms and CJK punctuation — rather than the whole East Asian Width table,
// which would be a dependency for no gain here.
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals and punctuation
		r >= 0x3041 && r <= 0x33FF, // kana, Hangul compatibility, CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK extension A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD: // CJK extensions B and beyond
		return true
	default:
		return false
	}
}

func newModelsCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Show which provider and model each agent role will use",
		Long: `Effective routing is derived, not configured: this answers "what will actually
happen", which is a different question from what ` + "`mas config`" + ` answers.

It also reports whether each model has a price. A model with no entry under
llm.pricing makes the run's cost unknown — never zero, which an operator would
read as free.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			svc, err := e.load()
			if err != nil {
				return err
			}
			cfg := svc.Config()

			router, err := llm.NewRouter(cfg.LLM)
			if err != nil {
				return err
			}
			defer func() { _ = router.Close() }()

			pricing := llm.Pricing(cfg.LLM.Pricing)
			type row struct {
				Role        string  `json:"role"`
				Provider    string  `json:"provider"`
				Model       string  `json:"model"`
				Temperature float64 `json:"temperature"`
				Priced      bool    `json:"priced"`
			}
			routes := router.Routes()
			roles := make([]string, 0, len(routes))
			for role := range routes {
				roles = append(roles, role)
			}
			sort.Strings(roles)

			rows := make([]row, 0, len(roles))
			for _, role := range roles {
				rt := routes[role]
				rows = append(rows, row{
					Role: role, Provider: rt.Name, Model: rt.Model,
					Temperature: rt.Temperature, Priced: pricing.Priced(rt.Model),
				})
			}
			if asJSON {
				return writeJSON(e.out, rows)
			}

			w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ROLE\tPROVIDER\tMODEL\tTEMP\tPRICED")
			for _, r := range rows {
				priced := "no"
				if r.Priced {
					priced = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%.2f\t%s\n", r.Role, r.Provider, r.Model, r.Temperature, priced)
			}
			if err := w.Flush(); err != nil {
				return err
			}

			var unpriced []string
			for _, r := range rows {
				if !r.Priced && r.Model != "" {
					unpriced = append(unpriced, r.Model)
				}
			}
			if len(unpriced) > 0 {
				fmt.Fprintf(e.out, "\n%s\n", wrap(unpricedNotice(e.lang(), unique(unpriced)), 92, ""))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

// unpricedNotice explains what an unpriced model means for the report, because
// "PRICED no" on its own reads like a missing feature rather than a stated
// unknown.
func unpricedNotice(lang string, models []string) string {
	if lang == "zh" {
		return "以下模型未定价：" + strings.Join(models, "、") +
			"。这些运行的成本会被报告为“未定价”，而不是 0 —— " +
			"0 会被读成“免费”。在 llm.pricing 下为它们设置价格即可得到成本数字。"
	}
	return "Not priced: " + strings.Join(models, ", ") +
		". Runs using them report their cost as unknown rather than as zero, which would " +
		"read as free. Set prices under llm.pricing to get a figure."
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// newEvalCmd runs the diagnostic case corpus.
//
// Its output is four numbers kept apart — concluded, missed, wrongly concluded,
// gaps not declared — and never a single score. A weighted sum would let a
// change that trades a miss for a confident wrong answer look like an
// improvement, and that is exactly the trade a model makes when it is pushed to
// be more decisive (specs/006-eval-harness/design-hld.md §3).
func newEvalCmd(e *env) *cobra.Command {
	var (
		matrix        bool
		asJSON        bool
		caseDirs      []string
		topology      string
		models        []string
		baselinePath  string
		writeBaseline string
	)
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the diagnostic case corpus and report what was concluded, missed and wrongly concluded",
		Long: `Each case carries synthetic telemetry and the failure modes a correct diagnosis
reaches. The whole pipeline runs against it — the same entry point 'mas diagnose'
uses, over real HTTP to stub metric and log servers — so query construction, the
safety guard's verdict, decoding and the rule engine are all exercised rather
than mocked away.

The corpus is synthetic. It measures agreement with its own labels, not accuracy
on real incidents, and the caveats printed under the table say so every time.

The exit status is non-zero when any case missed or reached a conclusion the case
rules out, which is what makes this usable as a CI gate.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := e.loadConfig()
			if err != nil {
				return err
			}
			lib, err := knowledge.LoadDefault(cfg.Knowledge.PackDirs)
			if err != nil {
				return err
			}
			corpus, err := eval.LoadCorpus(lib, caseDirs)
			if err != nil {
				return err
			}

			topologies := []string{topology}
			switch {
			case matrix:
				topologies = orchestrator.Names()
			case topology == "":
				topologies = []string{cfg.Run.DefaultTopology}
			}

			// The baseline is loaded before the run: a comparison against a
			// file that turns out to be unreadable is worth finding out about
			// before spending the corpus, not after.
			var baseline eval.Baseline
			if baselinePath != "" {
				baseline, err = eval.LoadBaseline(baselinePath)
				if err != nil {
					return err
				}
			}

			lang := cfg.Run.Language
			summary := eval.NewRunner(lib).Matrix(cmd.Context(), corpus.Cases(), topologies,
				eval.Options{Language: lang, LLM: cfg.LLM, Models: models})

			if writeBaseline != "" {
				// A person's act, never a run's. A baseline that writes itself
				// records whatever happened and can never fail.
				if err := eval.NewBaseline(summary).Save(writeBaseline); err != nil {
					return err
				}
				fmt.Fprintf(e.errOut, "baseline written to %s\n", writeBaseline)
			}

			if baselinePath == "" {
				render := eval.Render
				if asJSON {
					render = eval.RenderJSON
				}
				if err := render(e.out, summary, lang); err != nil {
					return err
				}
				// Returned rather than printed: the exit status is the gate,
				// and a regression that only appeared in the table would be a
				// gate that lets everything through.
				return summary.Regression()
			}

			delta := eval.Compare(baseline, summary)
			if asJSON {
				if err := eval.RenderComparisonJSON(e.out, summary, delta, lang); err != nil {
					return err
				}
			} else {
				if err := eval.Render(e.out, summary, lang); err != nil {
					return err
				}
				fmt.Fprintln(e.out)
				if err := eval.RenderDelta(e.out, delta, lang); err != nil {
					return err
				}
			}
			// The baseline gate replaces the absolute one: a build that failed
			// for two reasons at once teaches nothing about either.
			return delta.Gate()
		},
	}
	cmd.Flags().BoolVar(&matrix, "matrix", false, "run every topology, not just one")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	cmd.Flags().StringVar(&topology, "topology", "", "run one named topology (default: run.default_topology)")
	cmd.Flags().StringSliceVar(&caseDirs, "cases", nil, "directories of additional cases, alongside the shipped corpus")
	cmd.Flags().StringSliceVar(&models, "models", nil, "run each of these models across every case and topology")
	cmd.Flags().StringVar(&baselinePath, "baseline", "",
		"compare against a recorded baseline and fail only on cells that got worse")
	cmd.Flags().StringVar(&writeBaseline, "write-baseline", "",
		"record this run as the baseline at this path")
	return cmd
}
