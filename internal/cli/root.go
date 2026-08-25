// Package cli is the command-line surface.
//
// Governs: specs/001-mvp-core/design-lld.md §2.16
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
	"github.com/zlrrr/multi-agent-system-turbo/internal/version"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// globals carries the flags every subcommand honours.
type globals struct {
	configPath string
	logLevel   string
	logFormat  string
	language   string
	storeDir   string
	provider   string
	model      string
}

// env bundles what a command needs, so tests can substitute streams.
type env struct {
	out, errOut io.Writer
	g           *globals
}

func (e *env) load() (*service.Service, error) {
	overrides := map[string]string{}
	if e.g.logLevel != "" {
		overrides["log.level"] = e.g.logLevel
	}
	if e.g.logFormat != "" {
		overrides["log.format"] = e.g.logFormat
	}
	if e.g.language != "" {
		overrides["run.language"] = e.g.language
	}
	if e.g.storeDir != "" {
		overrides["store.dir"] = e.g.storeDir
	}
	if e.g.provider != "" {
		overrides["llm.provider"] = e.g.provider
	}
	if e.g.model != "" {
		overrides["llm.model"] = e.g.model
	}

	cfg, err := config.Load(e.configPaths(), nil, overrides)
	if err != nil {
		return nil, err
	}
	redactor := safety.NewRedactor(cfg.Log.Redact, nil)
	logger := obs.Setup(cfg.Log, redactor, e.errOut)
	obs.SetFallbackLogger(logger)
	return service.New(service.Options{Config: cfg, Redactor: redactor, Logger: logger})
}

// configPaths is where configuration is read from: the --config flag, then
// MAS_CONFIG, then the default search path.
func (e *env) configPaths() []string {
	if e.g.configPath != "" {
		return []string{e.g.configPath}
	}
	if p := os.Getenv("MAS_CONFIG"); p != "" {
		return []string{p}
	}
	return nil
}

// lang resolves the operator's language for the commands that only list things:
// the --lang flag if given, otherwise whatever the configuration says.
//
// A broken configuration must not stop it. Looking up an error code or reading
// what a topology does is exactly what an operator does *while* their config is
// broken, so a load failure falls back to English rather than failing the
// command; `mas doctor` is where a bad config is reported.
func (e *env) lang() string {
	if e.g.language != "" {
		return e.g.language
	}
	if cfg, err := config.Load(e.configPaths(), nil, nil); err == nil &&
		strings.TrimSpace(cfg.Run.Language) != "" {
		return cfg.Run.Language
	}
	return "en"
}

// Execute builds and runs the CLI. It returns the process exit code rather than
// calling os.Exit, so the whole surface is testable.
func Execute(args []string, stdout, stderr io.Writer) int {
	g := &globals{}
	e := &env{out: stdout, errOut: stderr, g: g}

	root := &cobra.Command{
		Use:   "mas",
		Short: "Diagnose open-source middleware with a read-only multi-agent system",
		Long: `MAS-Turbo analyses runtime problems in open-source middleware — Redis, Kafka,
MongoDB, Pulsar, Milvus, OceanBase and others — by correlating metrics, logs,
live cluster state and upstream source code.

It is read-only by construction. It cannot restart, reconfigure or write to
anything it inspects: every recommendation is for a human operator to carry out.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&g.configPath, "config", "c", "", "path to mas.yaml (default: ./mas.yaml, /etc/mas/mas.yaml, or $MAS_CONFIG)")
	pf.StringVar(&g.logLevel, "log-level", "", "log level: debug, info, warn, error")
	pf.StringVar(&g.logFormat, "log-format", "", "log format: json or text")
	pf.StringVar(&g.language, "lang", "", "report language: en or zh")
	pf.StringVar(&g.storeDir, "store-dir", "", "directory for run records")
	pf.StringVar(&g.provider, "provider", "", "LLM provider: mock, anthropic or openai")
	pf.StringVar(&g.model, "model", "", "model name for the configured provider")

	root.AddCommand(
		newDiagnoseCmd(e),
		newServeCmd(e),
		newDoctorCmd(e),
		newReplayCmd(e),
		newRunsCmd(e),
		newTargetsCmd(e),
		newTopologiesCmd(e),
		newModelsCmd(e),
		newPacksCmd(e),
		newToolsCmd(e),
		newErrCodesCmd(e),
		newConfigCmd(e),
		newVersionCmd(e),
	)

	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.Execute(); err != nil {
		printError(stderr, err, g.language)
		return exitCode(err)
	}
	return 0
}

// printError renders an error the way an operator needs it: the code, the
// message in their language, and what to do about it.
func printError(w io.Writer, err error, lang string) {
	if lang != "zh" {
		lang = "en"
	}
	if e, ok := errs.AsError(err); ok {
		fmt.Fprintf(w, "\n%s  %s\n", e.Code(), e.Message(lang))
		if remedy := e.Remedy(lang); remedy != "" {
			fmt.Fprintf(w, "        %s\n", remedy)
		}
		return
	}
	fmt.Fprintf(w, "\nerror: %v\n", err)
}

// exitCode maps an error domain to a distinct exit status, so a script can react
// to a safety refusal differently from a configuration mistake.
func exitCode(err error) int {
	switch errs.Domain(errs.CodeOf(err)) {
	case "config":
		return 2
	case "safety":
		return 3
	case "collector":
		return 4
	case "llm", "orchestration":
		return 5
	case "storage":
		return 6
	default:
		return 1
	}
}

func newVersionCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			if asJSON {
				return writeJSON(e.out, info)
			}
			fmt.Fprintln(e.out, info.String())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}
