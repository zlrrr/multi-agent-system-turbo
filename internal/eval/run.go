package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Options control one evaluation run.
type Options struct {
	Topology string
	Language string
	Mode     core.Mode
	LLM      config.LLMConfig

	// MaxConcurrency bounds how many cases run at once. Cases are independent,
	// and the corpus has to stay inside CI's minute (NFR-001).
	MaxConcurrency int

	// Models is the model axis. Empty means the single model in LLM, which is
	// what every run before feature 008 did. G7.3 asks for a model/topology
	// matrix, and only the topology axis existed.
	Models []string
}

func (o Options) withDefaults() Options {
	if o.Topology == "" {
		o.Topology = "supervisor"
	}
	if o.Language == "" {
		o.Language = "en"
	}
	if o.Mode == "" {
		o.Mode = core.ModeOnline
	}
	if o.LLM.Provider == "" {
		o.LLM = config.LLMConfig{Provider: "mock", Model: "mock-1", MaxTokens: 1024}
	}
	if o.MaxConcurrency <= 0 {
		o.MaxConcurrency = 4
	}
	return o
}

// Runner evaluates cases against the real pipeline.
type Runner struct {
	lib *knowledge.Library
}

// NewRunner builds a runner over a knowledge library.
func NewRunner(lib *knowledge.Library) *Runner { return &Runner{lib: lib} }

// Run evaluates one case under one topology.
//
// It builds a real service pointed at the case's stub servers and calls
// Diagnose — the same entry point `mas diagnose` uses. Nothing in the pipeline
// is substituted, which is the whole reason the harness lives outside the
// system rather than inside it (design-hld.md §1).
func (r *Runner) Run(ctx context.Context, c *Case, o Options) Outcome {
	o = o.withDefaults()
	started := time.Now()

	st := newStubs(c)
	defer st.close()

	cfg := caseConfig(c, st, o)
	redactor := safety.NewRedactor(cfg.Log.Redact, nil)
	svc, err := service.New(service.Options{
		Config:   cfg,
		Redactor: redactor,
		Logger:   obs.Setup(cfg.Log, redactor, discard{}),
	})
	if err != nil {
		return Outcome{Case: c.ID(), Topology: o.Topology, Model: o.LLM.Model,
			Err: err, Duration: time.Since(started)}
	}

	report, err := svc.Diagnose(ctx, core.DiagnoseRequest{
		Target:   c.ID(),
		Symptom:  c.Symptom.In(o.Language),
		Topology: o.Topology,
		Mode:     o.Mode,
		Language: o.Language,
	})
	if err != nil {
		return Outcome{Case: c.ID(), Topology: o.Topology, Model: o.LLM.Model,
			Err: err, Duration: time.Since(started)}
	}

	out := Score(c, report)
	out.Topology = o.Topology
	// From the options this job ran with, never from shared config: a shared
	// read would attribute every cell's cost to whichever model was configured
	// last (specs/008-regression-baselines/plan.md RSK-4).
	out.Model = o.LLM.Model
	out.Duration = time.Since(started)
	prom, loki := st.hits()
	out.TelemetryHits = prom + loki
	return out
}

// AllTopologies is every registered topology, which is what `--matrix` runs and
// what the shipped baseline covers.
func AllTopologies() []string { return orchestrator.Names() }

// Matrix evaluates every case against every topology.
func (r *Runner) Matrix(ctx context.Context, cases []*Case, topologies []string, o Options) Summary {
	o = o.withDefaults()
	if len(topologies) == 0 {
		topologies = []string{o.Topology}
	}

	models := o.Models
	if len(models) == 0 {
		models = []string{o.LLM.Model}
	}

	type job struct {
		c        *Case
		topology string
		model    string
	}
	var jobs []job
	for _, c := range cases {
		for _, t := range topologies {
			for _, m := range models {
				jobs = append(jobs, job{c: c, topology: t, model: m})
			}
		}
	}

	results := make([]Outcome, len(jobs))
	sem := make(chan struct{}, o.MaxConcurrency)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			opts := o
			opts.Topology = j.topology
			opts.LLM.Model = j.model
			results[i] = r.Run(ctx, j.c, opts)
		}(i, j)
	}
	wg.Wait()

	// Results are ordered by case, topology then model rather than by
	// completion, so two runs of the same matrix render identically (FR-008).
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Case != results[j].Case {
			return results[i].Case < results[j].Case
		}
		if results[i].Topology != results[j].Topology {
			return results[i].Topology < results[j].Topology
		}
		return results[i].Model < results[j].Model
	})

	return Summary{
		Outcomes:   results,
		Topologies: append([]string(nil), topologies...),
		Cases:      len(cases),
		Provider:   o.LLM.Provider,
		Language:   o.Language,
	}
}

// caseConfig builds the configuration a case's run uses: real collectors
// pointed at the stub servers, an in-memory store, and no source acquisition.
func caseConfig(c *Case, st *stubs, o Options) *config.Config {
	cfg := config.Default()
	cfg.Store = config.StoreConfig{Type: "memory"}
	cfg.Log.Level = "error"
	cfg.Source.Enabled = false
	cfg.LLM = o.LLM
	cfg.Run.DefaultTopology = o.Topology
	cfg.Run.DefaultMode = string(o.Mode)
	cfg.Run.Language = o.Language

	// The deterministic phase must always run to completion: short-circuiting
	// on a confident rule is right in production and would make the corpus
	// measure only the rules under some cases and the whole pipeline under
	// others, which is not a comparison.
	cfg.Run.DeterministicShortCircuit = 0

	cfg.Telemetry.Metrics = []config.MetricsSource{{
		Name: "case", Type: "prometheus", URL: st.prom.URL,
		Timeout: config.Duration(10 * time.Second), MaxSamples: 1000,
	}}
	cfg.Telemetry.Logs = []config.LogsSource{{
		Name: "case", Type: "loki", URL: st.loki.URL,
		Timeout: config.Duration(10 * time.Second), MaxLines: 500,
	}}
	cfg.Targets = []config.TargetConfig{{
		ID: c.ID(), Kind: c.Metadata.Middleware, Version: c.Metadata.Version,
		Labels:        map[string]string{"job": "case"},
		MetricsSource: "case", LogsSource: "case",
	}}
	return cfg
}

// discard swallows the logger's output: an evaluation run's own logs would
// drown the table it produces.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Regression reports whether a summary should fail a build, and why.
func (s Summary) Regression() error {
	misses, falses := 0, 0
	for _, o := range s.Outcomes {
		if len(o.Missing) > 0 || o.Err != nil {
			misses++
		}
		if len(o.False) > 0 {
			falses++
		}
	}
	if misses == 0 && falses == 0 {
		return nil
	}
	return errs.New("MAS-9103", misses, falses)
}

// describeTopologies renders the topology list for a heading.
func describeTopologies(t []string) string {
	if len(t) == 0 {
		return "(none)"
	}
	return strings.Join(t, ", ")
}

var _ = fmt.Sprintf
