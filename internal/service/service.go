// Package service is the application layer: it admits a request, prepares the
// world an analysis needs, runs the two-phase pipeline, and assembles the report.
//
// Governs: specs/001-mvp-core/design-lld.md §2.16, design-hld.md §5.1
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/loki"
	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/promql"
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter/local"
	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/rules"
	"github.com/zlrrr/multi-agent-system-turbo/internal/safety"
	"github.com/zlrrr/multi-agent-system-turbo/internal/source"
	"github.com/zlrrr/multi-agent-system-turbo/internal/store"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/internal/version"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"

	// Adapters register themselves.
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter/kube"
	_ "github.com/zlrrr/multi-agent-system-turbo/internal/llm/anthropic"
	_ "github.com/zlrrr/multi-agent-system-turbo/internal/llm/mock"
	_ "github.com/zlrrr/multi-agent-system-turbo/internal/llm/openai"
)

// Service runs diagnoses.
type Service struct {
	cfg      *config.Config
	guard    *safety.Guard
	redactor *safety.Redactor
	library  *knowledge.Library
	store    store.RunStore
	metrics  *obs.Metrics
	logger   *slogLogger
	now      func() time.Time
}

// slogLogger is a tiny alias so the struct field reads clearly.
type slogLogger = loggerType

// Options configures a Service.
type Options struct {
	Config   *config.Config
	Guard    *safety.Guard
	Redactor *safety.Redactor
	Library  *knowledge.Library
	Store    store.RunStore
	Metrics  *obs.Metrics
	Logger   *loggerType
}

// New builds a Service, constructing whatever the caller did not supply.
func New(opts Options) (*Service, error) {
	if opts.Config == nil {
		return nil, errs.New("MAS-9001", "service requires a configuration")
	}
	s := &Service{cfg: opts.Config, now: time.Now}

	s.redactor = opts.Redactor
	if s.redactor == nil {
		s.redactor = safety.NewRedactor(opts.Config.Log.Redact, nil)
	}
	s.guard = opts.Guard
	if s.guard == nil {
		g, err := safety.NewGuard(opts.Config.Safety)
		if err != nil {
			return nil, err
		}
		s.guard = g
	}
	s.library = opts.Library
	if s.library == nil {
		lib, err := knowledge.LoadDefault(opts.Config.Knowledge.PackDirs)
		if err != nil {
			return nil, err
		}
		s.library = lib
	}
	s.store = opts.Store
	if s.store == nil {
		st, err := store.OpenConfig(opts.Config.Store)
		if err != nil {
			return nil, err
		}
		s.store = st
	}
	s.metrics = opts.Metrics
	if s.metrics == nil {
		s.metrics = obs.Default()
	}
	s.logger = opts.Logger
	if s.logger == nil {
		s.logger = obs.Setup(opts.Config.Log, s.redactor, nil)
	}
	s.metrics.SetGauge("mas_packs_loaded", float64(s.library.Len()), nil)
	s.metrics.SetGauge("mas_build_info", 1, map[string]string{
		"version": version.Get().Version, "commit": version.Get().Commit,
	})
	return s, nil
}

// Config exposes the effective configuration.
func (s *Service) Config() *config.Config { return s.cfg }

// Library exposes the loaded knowledge packs.
func (s *Service) Library() *knowledge.Library { return s.library }

// Store exposes the run store.
func (s *Service) Store() store.RunStore { return s.store }

// Guard exposes the safety guard, for `mas doctor`.
func (s *Service) Guard() *safety.Guard { return s.guard }

// Metrics exposes the self-metrics registry.
func (s *Service) Metrics() *obs.Metrics { return s.metrics }

// Close releases the store.
func (s *Service) Close() error { return s.store.Close() }

// Admit validates a request and fills in configured defaults. It is separated
// from Diagnose so the HTTP layer can reject a bad request before creating a run
// record for it (FR-001).
func (s *Service) Admit(req core.DiagnoseRequest) (core.DiagnoseRequest, error) {
	if strings.TrimSpace(req.Target) == "" {
		return req, errs.New("MAS-1007", "target must be set")
	}
	if strings.TrimSpace(req.Symptom) == "" {
		return req, errs.New("MAS-1007", "symptom must be set; describe what you observed")
	}
	if _, err := s.cfg.Target(req.Target); err != nil {
		return req, err
	}

	if req.Window.To.IsZero() {
		req.Window.To = s.now().UTC()
	}
	if req.Window.From.IsZero() {
		d := s.cfg.Run.DefaultWindow.D()
		if d <= 0 {
			d = time.Hour
		}
		req.Window.From = req.Window.To.Add(-d)
	}
	if err := req.Window.Validate(); err != nil {
		return req, err
	}

	if req.Mode == "" {
		req.Mode = core.Mode(s.cfg.Run.DefaultMode)
	}
	if req.Mode != core.ModeOffline && req.Mode != core.ModeOnline {
		return req, errs.New("MAS-1011", string(req.Mode))
	}

	if req.Topology == "" {
		req.Topology = s.cfg.Run.DefaultTopology
	}
	if _, err := orchestrator.Open(req.Topology); err != nil {
		return req, err
	}

	if req.Language == "" {
		req.Language = s.cfg.Run.Language
	}
	if req.Language != "en" && req.Language != "zh" {
		return req, errs.New("MAS-1007", fmt.Sprintf("language %q is not one of en, zh", req.Language))
	}

	if req.Budget.MaxSteps == 0 {
		req.Budget.MaxSteps = s.cfg.Run.Budget.MaxSteps
	}
	if req.Budget.MaxToolCalls == 0 {
		req.Budget.MaxToolCalls = s.cfg.Run.Budget.MaxToolCalls
	}
	if req.Budget.MaxTokens == 0 {
		req.Budget.MaxTokens = s.cfg.Run.Budget.MaxTokens
	}
	if req.Budget.MaxWall == 0 {
		req.Budget.MaxWall = s.cfg.Run.Budget.MaxWall.D()
	}
	return req, nil
}

// NewRunID mints a run identifier that sorts chronologically and is unique.
func NewRunID(t time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "run-" + t.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// Diagnose executes the two-phase pipeline and returns the report.
func (s *Service) Diagnose(ctx context.Context, req core.DiagnoseRequest) (*core.Report, error) {
	started := s.now()

	admitted, err := s.Admit(req)
	if err != nil {
		return nil, err
	}
	req = admitted

	runID := NewRunID(started)
	rc := &obs.RunContext{RunID: runID, Logger: s.logger, Metrics: s.metrics}
	ctx = obs.WithRun(ctx, rc)
	log := obs.Log(ctx)

	targetCfg, _ := s.cfg.Target(req.Target)
	target := core.Target{
		ID: targetCfg.ID, Kind: core.MiddlewareKind(targetCfg.Kind), Version: targetCfg.Version,
		Labels: targetCfg.Labels, Selector: targetCfg.Selector,
		Env: core.EnvBinding{Name: targetCfg.Env, Namespace: targetCfg.Namespace, Selector: targetCfg.Selector},
	}
	if envCfg, ok := s.cfg.Envs[targetCfg.Env]; ok {
		target.Env.Type = envCfg.Type
		if target.Env.Namespace == "" {
			target.Env.Namespace = envCfg.Namespace
		}
	}

	rec := &core.RunRecord{
		ID: runID, Status: core.RunRunning, Request: req, Target: target,
		Principal: req.Principal,
		StartedAt: started.UTC(),
		Versions: map[string]string{
			"binary": version.Get().Version, "topology": req.Topology,
			"provider": s.cfg.LLM.Provider, "model": s.cfg.LLM.Model,
			"packs": fmt.Sprintf("%d", s.library.Len()),
		},
	}
	if err := s.store.Create(ctx, rec); err != nil {
		return nil, err
	}
	s.metrics.IncCounter("mas_runs_total", map[string]string{
		"topology": req.Topology, "mode": string(req.Mode),
	})
	log.Info("diagnosis started",
		"target", req.Target, "kind", targetCfg.Kind, "symptom", req.Symptom,
		"mode", req.Mode, "topology", req.Topology,
		"window_from", req.Window.From, "window_to", req.Window.To)

	report, err := s.run(ctx, rec, req, target, targetCfg)
	if err != nil {
		_ = s.store.Fail(ctx, runID, errs.CodeOf(err), err.Error())
		s.metrics.IncCounter("mas_runs_completed_total", map[string]string{"status": "failed"})
		log.Error("diagnosis failed", "code", errs.CodeOf(err), "error", err.Error())
		return nil, err
	}

	report.Usage.WallMillis = time.Since(started).Milliseconds()
	if err := s.store.Finish(ctx, runID, report, report.Usage); err != nil {
		// The analysis is done and the operator gets it: losing the answer
		// because we could not file it away would be the wrong trade in the
		// middle of an incident. But it is said out loud rather than left in a
		// log nobody is reading — a record that quietly did not save is only
		// discovered by the person who needed it
		// (specs/010-object-run-store/design-hld.md §4).
		log.Warn("run record could not be finalised", "code", errs.CodeOf(err), "error", err.Error())
		report.Notes = append(report.Notes, storeFailureNote(errs.CodeOf(err), runID, req.Language))
	}
	s.metrics.IncCounter("mas_runs_completed_total", map[string]string{"status": "completed"})
	s.metrics.Observe("mas_run_duration_seconds", time.Since(started).Seconds(),
		map[string]string{"topology": req.Topology})
	log.Info("diagnosis complete",
		"hypotheses", len(report.Hypotheses), "findings", len(report.Findings),
		"gaps", len(report.Gaps), "llm_calls", report.Usage.LLMCalls,
		"tool_calls", report.Usage.ToolCalls, "duration_ms", report.Usage.WallMillis)
	return report, nil
}

// stepSink adapts the run store to the tool layer's step recorder.
//
// Concurrent investigators append through it simultaneously, so the counter is
// atomic; the stores behind it serialise their own writes.
type stepSink struct {
	store store.RunStore
	runID string
	calls atomic.Int64
}

func (s *stepSink) AppendStep(ctx context.Context, step core.Step) {
	s.calls.Add(1)
	if err := s.store.Append(ctx, s.runID, step); err != nil {
		obs.Log(ctx).Warn("step not recorded", "code", errs.CodeOf(err), "error", err.Error())
	}
}

// Calls reports how many steps were recorded.
func (s *stepSink) Calls() int { return int(s.calls.Load()) }

func (s *Service) run(ctx context.Context, rec *core.RunRecord, req core.DiagnoseRequest,
	target core.Target, targetCfg config.TargetConfig) (*core.Report, error) {

	log := obs.Log(ctx)
	sink := &stepSink{store: s.store, runID: rec.ID}

	// ── prepare the world ────────────────────────────────────────────────────
	registry := tool.NewRegistry()
	var prepGaps []core.Gap

	binding, adapterGaps := s.registerEnvTools(ctx, registry, targetCfg, req.Mode)
	prepGaps = append(prepGaps, adapterGaps...)
	if binding != nil {
		target.Instances = binding.Instances
		if target.Version == "" {
			target.Version = binding.Version
		}
		for _, n := range binding.Notes {
			log.Info("environment note", "note", n)
		}
	}

	prepGaps = append(prepGaps, s.registerTelemetryTools(ctx, registry, targetCfg)...)
	prepGaps = append(prepGaps, s.registerSourceTools(registry, target)...)

	pack, packErr := s.library.For(target.Kind, target.Version)
	if packErr == nil {
		// Version scoping is resolved once, here, and what comes back is the
		// pack the rest of the run holds. Filtering at each lookup instead
		// would make this something five call sites have to remember, and a
		// caller that forgot would silently get unscoped behaviour
		// (specs/007-version-scoped-rules/plan.md §1).
		var scopeGaps []core.Gap
		pack, scopeGaps = pack.Resolve(target.Version)
		prepGaps = append(prepGaps, scopeGaps...)
		for _, g := range scopeGaps {
			log.Info("version scoping", "code", g.Code, "detail", g.Detail)
		}
	}
	if packErr != nil {
		prepGaps = append(prepGaps, core.Gap{
			Intent: "load knowledge for " + string(target.Kind), Reason: core.GapNotConfigured,
			Code: errs.CodeOf(packErr), Detail: packErr.Error(),
			Impact: "only generic checks are available for this middleware",
		})
		log.Warn("no knowledge pack", "kind", target.Kind, "code", errs.CodeOf(packErr))
	}

	// A run with no evidence source at all cannot produce anything worth
	// reading, so it is refused at admission rather than yielding an empty report.
	if len(registry.List()) == 0 {
		return nil, errs.New("MAS-1007",
			"no evidence source is reachable for this target: configure telemetry.metrics, telemetry.logs, "+
				"or an environment, then run `mas doctor`")
	}

	invoker, err := tool.NewInvoker(registry, tool.InvokerOptions{
		Guard: s.guard, Sink: sink, Redactor: s.redactor, Mode: req.Mode,
		Timeout: s.cfg.Safety.MaxTimeout.D(), MaxToolCalls: req.Budget.MaxToolCalls,
	})
	if err != nil {
		return nil, err
	}

	ctx = promql.WithWindow(ctx, req.Window)
	ctx = loki.WithWindow(ctx, req.Window)

	// ── phase 1: deterministic ───────────────────────────────────────────────
	engine := rules.New(invoker, s.library)
	deterministic := engine.RunAll(ctx, pack, req.Symptom, rules.Input{
		Target: target, Selector: promSelector(targetCfg), Language: req.Language,
		MaxSteps: req.Budget.MaxSteps,
	})

	report := &core.Report{
		Schema: core.ReportSchema, RunID: rec.ID, GeneratedAt: s.now().UTC(),
		Target: target, Request: req, Topology: req.Topology,
		Findings: deterministic.Findings, ChecksPassed: deterministic.ChecksPassed,
		Evidence: deterministic.Evidence,
		Gaps:     append(prepGaps, deterministic.Gaps...),
	}

	// ── phase 2: agentic, unless the deterministic layer already settled it ──
	threshold := s.cfg.Run.DeterministicShortCircuit
	shortCircuit := threshold > 0 && deterministic.TopConfidence() >= threshold &&
		req.Options["force_agents"] != "true"

	if shortCircuit {
		log.Info("deterministic short circuit",
			"top_confidence", deterministic.TopConfidence(), "threshold", threshold, "llm_calls", 0)
		s.finishDeterministic(report, pack, deterministic, req.Language)
		report.Usage.ToolCalls = sink.Calls()
		report.SortFindings()
		report.SortHypotheses()
		return report, report.Validate()
	}

	// The router opens every provider the run routes to, so a bad credential on
	// a per-role provider is refused here rather than discovered mid-run, after
	// the roles that did work have already spent their tokens.
	router, err := llm.NewRouter(s.cfg.LLM)
	if err != nil {
		// A model outage must not lose the deterministic work already done.
		log.Warn("model provider unavailable; reporting deterministic findings only",
			"code", errs.CodeOf(err), "error", err.Error())
		report.Gaps = append(report.Gaps, core.Gap{
			Intent: "agentic analysis", Reason: core.GapUnavailable,
			Code: errs.CodeOf(err), Detail: err.Error(),
			Impact: "only the deterministic checks contributed to this report",
		})
		s.finishDeterministic(report, pack, deterministic, req.Language)
		report.Usage.ToolCalls = sink.Calls()
		report.SortFindings()
		report.SortHypotheses()
		return report, report.Validate()
	}
	defer func() { _ = router.Close() }()

	st := agent.NewState()
	st.Run = rec
	st.Request = req
	st.Target = target
	st.Pack = pack
	st.Prior = deterministic.Findings
	st.Passed = deterministic.ChecksPassed
	st.Tools = invoker
	st.Sink = sink
	st.Provider = router.Default().Provider
	st.Router = router
	st.LLMConfig = s.cfg.LLM
	st.Language = req.Language
	st.MaxConcurrency = s.cfg.Run.MaxConcurrency
	st.Budget = agent.Budget{
		MaxSteps: req.Budget.MaxSteps, MaxToolCalls: req.Budget.MaxToolCalls,
		MaxTokens: req.Budget.MaxTokens, MaxWall: req.Budget.MaxWall,
	}
	for _, ev := range deterministic.Evidence {
		st.AddEvidence(ev)
	}
	for _, g := range report.Gaps {
		st.AddGap(g)
	}
	st.Start()

	topology, err := orchestrator.Open(req.Topology)
	if err != nil {
		return nil, err
	}
	if err := topology.Run(ctx, st); err != nil {
		return nil, errs.Wrap(err, "MAS-3002", req.Topology, err.Error())
	}

	report.Summary = st.Summary()
	report.Hypotheses = st.Hypotheses()
	report.Recommendations = st.Recommendations()
	report.Evidence = st.Evidence()
	report.Gaps = st.Gaps()
	report.Notes = st.Notes()
	report.Usage = st.Usage()
	report.Usage.ToolCalls = sink.Calls()

	// Cost and the per-role breakdown come from the ledger the router wrote to,
	// not from the state's running totals: the ledger is what saw the model
	// names, and cost can only be priced against those.
	report.Usage.Cost = router.Ledger().Cost()
	report.RoleUsage = router.Ledger().ByRole()
	report.Routing = effectiveRouting(router)

	truncated, reason := st.Truncated()
	report.Truncated = truncated || deterministic.Truncated
	if reason != "" {
		report.Notes = append(report.Notes, "Run truncated: "+reason)
	}

	// Pack recommendations for concluded failure modes are appended after the
	// agents' own, so vetted domain advice is never lost to a terse model reply.
	report.Conclusions = uniqueSorted(deterministic.Conclusions)
	s.appendPackRecommendations(report, pack, deterministic.Conclusions, req.Language)
	if strings.TrimSpace(report.Summary) == "" {
		report.Summary = fallbackSummary(report, req.Language)
	}

	report.SortFindings()
	report.SortHypotheses()
	return report, report.Validate()
}

// finishDeterministic turns rule findings into report hypotheses when no agent
// phase runs, so a short-circuited run still reads like a diagnosis.
func (s *Service) finishDeterministic(report *core.Report, pack *knowledge.Pack,
	out rules.Output, lang string) {

	for _, f := range out.Findings {
		if f.Confidence < 0.5 {
			continue
		}
		report.Hypotheses = append(report.Hypotheses, core.Hypothesis{
			ID:         "h-" + strings.TrimPrefix(f.ID, "f-"),
			Statement:  f.Statement,
			Status:     core.HypothesisSupported,
			Confidence: f.Confidence,
			Supporting: f.Evidence,
			Rationale:  deterministicRationale(f, lang),
		})
	}
	report.Conclusions = uniqueSorted(out.Conclusions)
	s.appendPackRecommendations(report, pack, out.Conclusions, lang)
	report.Summary = fallbackSummary(report, lang)
}

// storeFailureNote tells the reader that this report exists but was not
// persisted, so nobody looks for it later and concludes it was never run.
func storeFailureNote(code, runID, lang string) string {
	if lang == "zh" {
		return "本报告未能存入运行存储（" + code + "，运行 id " + runID +
			"）。分析本身完好，但此次运行不会出现在 `mas runs` 中，也无法被重放。"
	}
	return "This report could not be written to the run store (" + code +
		", run id " + runID + "). The analysis is intact, but this run will not " +
		"appear in `mas runs` and cannot be replayed."
}

func deterministicRationale(f core.Finding, lang string) string {
	if lang == "zh" {
		return "由确定性检查 " + f.Origin + " 得出，未使用模型推理。"
	}
	return "Established by the deterministic check " + f.Origin + ", with no model reasoning involved."
}

// appendPackRecommendations adds the vetted advice for each concluded failure
// mode, skipping any the agents already said in substance.
// effectiveRouting renders which provider and model each role used, so a
// comparison between two runs can be repeated exactly rather than approximately
// (FR-012).
func effectiveRouting(router *llm.Router) map[string]string {
	out := map[string]string{}
	for role, rt := range router.Routes() {
		out[role] = rt.Name + "/" + rt.Model
	}
	return out
}

// uniqueSorted deduplicates and orders ids so the report's verdict is stable
// between runs of the same case.
func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Service) appendPackRecommendations(report *core.Report, pack *knowledge.Pack,
	conclusions []string, lang string) {

	if pack == nil {
		return
	}
	seen := map[string]bool{}
	for _, r := range report.Recommendations {
		seen[normalise(r.Statement)] = true
	}
	added := map[string]bool{}
	for _, id := range conclusions {
		if added[id] {
			continue
		}
		added[id] = true
		mode, ok := pack.FailureMode(id)
		if !ok {
			continue
		}
		for _, adv := range mode.Recommendations {
			text := adv.Statement.In(lang)
			if text == "" || seen[normalise(text)] {
				continue
			}
			seen[normalise(text)] = true
			report.Recommendations = append(report.Recommendations,
				core.NewRecommendation(text, core.Risk(adv.Risk), adv.Rationale.In(lang)))
		}
	}
}

func normalise(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func fallbackSummary(report *core.Report, lang string) string {
	switch {
	case len(report.Hypotheses) > 0:
		if lang == "zh" {
			return fmt.Sprintf("确定性检查得出 %d 条发现；置信度最高的解释是：%s",
				len(report.Findings), report.Hypotheses[0].Statement)
		}
		return fmt.Sprintf("The deterministic checks produced %d finding(s); the leading explanation is: %s",
			len(report.Findings), report.Hypotheses[0].Statement)
	case len(report.Gaps) > 0:
		if lang == "zh" {
			return fmt.Sprintf("在该时间窗内未发现异常，但有 %d 项证据未能采集，因此本结论并不完整。",
				len(report.Gaps))
		}
		return fmt.Sprintf("No fault was identified in this window, but %d piece(s) of evidence could not be "+
			"collected, so this conclusion is incomplete.", len(report.Gaps))
	default:
		if lang == "zh" {
			return "在该时间窗内，所有检查均正常，未发现异常。"
		}
		return "Every check passed in this window; no fault was identified."
	}
}

func promSelector(t config.TargetConfig) string {
	if len(t.Labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(t.Labels))
	for k := range t.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, t.Labels[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// registerTelemetryTools installs the metric and log collectors and reports what
// this run will not be able to see.
//
// Each configured source is probed once. A source that cannot answer is
// unobtainable evidence, and the report has to say so whether or not this run's
// control flow happened to ask it anything: discovered only on use, "the logs
// were unavailable" becomes a fact about which agent ran rather than about the
// deployment, and the same incident under a different topology would quietly
// omit it (specs/001-mvp-core/design-lld.md amendment 1.0.6).
//
// The tools are registered either way. A source that is down at admission may
// recover mid-run, and refusing to install the tool would turn a transient
// outage into a whole run without metrics.
func (s *Service) registerTelemetryTools(ctx context.Context, registry *tool.Registry,
	t config.TargetConfig) []core.Gap {

	var gaps []core.Gap
	hc := &http.Client{}

	if ms, err := s.cfg.MetricsSourceFor(t.MetricsSource); err == nil {
		client := promql.New(ms, hc)
		registry.MustRegister(promql.Tools(client)...)
		if probeErr := client.Health(ctx); probeErr != nil {
			gaps = append(gaps, core.Gap{
				Intent: "metrics source " + client.Name(), Reason: core.GapUnavailable,
				Code: errs.CodeOf(probeErr), Detail: probeErr.Error(),
				Impact: "no metric evidence could be read for this run; every metric check below is unperformed rather than passed",
			})
		}
	} else {
		gaps = append(gaps, core.Gap{
			Intent: "metrics source", Reason: core.GapNotConfigured, Code: errs.CodeOf(err),
			Detail: err.Error(), Impact: "no metric evidence is available for this run",
		})
	}

	if ls, err := s.cfg.LogsSourceFor(t.LogsSource); err == nil {
		client := loki.New(ls, hc)
		registry.MustRegister(loki.Tools(client)...)
		if probeErr := client.Health(ctx); probeErr != nil {
			gaps = append(gaps, core.Gap{
				Intent: "log source " + client.Name(), Reason: core.GapUnavailable,
				Code: errs.CodeOf(probeErr), Detail: probeErr.Error(),
				Impact: "no log evidence could be read for this run; failure modes that only show in the log are neither confirmed nor ruled out",
			})
		}
	} else {
		gaps = append(gaps, core.Gap{
			Intent: "log source", Reason: core.GapNotConfigured, Code: errs.CodeOf(err),
			Detail: err.Error(), Impact: "no log evidence is available for this run",
		})
	}
	return gaps
}

func (s *Service) registerEnvTools(ctx context.Context, registry *tool.Registry,
	t config.TargetConfig, mode core.Mode) (*envadapter.Binding, []core.Gap) {

	if t.Env == "" {
		return nil, nil
	}
	envCfg, ok := s.cfg.Envs[t.Env]
	if !ok {
		return nil, []core.Gap{{
			Intent: "environment " + t.Env, Reason: core.GapNotConfigured, Code: "MAS-1008",
			Detail: "the target references an environment that is not declared",
			Impact: "no live environment evidence is available",
		}}
	}
	adapter, err := envadapter.Open(t.Env, envCfg)
	if err != nil {
		return nil, []core.Gap{{
			Intent: "environment " + t.Env, Reason: core.GapNotConfigured, Code: errs.CodeOf(err),
			Detail: err.Error(),
			Impact: "no live environment evidence is available; telemetry analysis continues",
		}}
	}

	// A pack's inspection commands are installed on whichever adapter can run
	// them. The two adapters take the same commands and reach them differently:
	// the host runs them locally, the cluster runs them inside the pod. Neither
	// widens what may run — the guard's allow-list decides that in both cases.
	pack, packErr := s.library.For(core.MiddlewareKind(t.Kind), t.Version)
	switch a := adapter.(type) {
	case *local.Adapter:
		if packErr == nil {
			var cmds []local.InspectCommand
			for _, in := range pack.InspectCommands() {
				cmds = append(cmds, local.InspectCommand{
					ID: in.ID, Binary: in.Binary, Args: in.Args, Description: in.Description.In("en"),
				})
			}
			a.SetInspectCommands(cmds)
		}
	case *kube.Adapter:
		a.SetExecEnabled(envCfg.ExecEnabled())
		if packErr == nil {
			var cmds []kube.InspectCommand
			for _, in := range pack.InspectCommands() {
				cmds = append(cmds, kube.InspectCommand{
					ID: in.ID, Binary: in.Binary, Args: in.Args, Description: in.Description.In("en"),
				})
			}
			a.SetInspectCommands(cmds)
		}
	}

	registry.MustRegister(adapter.Tools()...)

	// Resolution reads the live environment, so it only happens online.
	if mode != core.ModeOnline {
		return nil, nil
	}
	binding, err := adapter.Resolve(ctx, t)
	if err != nil {
		return &binding, []core.Gap{{
			Intent: "resolve target in " + t.Env, Reason: core.GapUnavailable, Code: errs.CodeOf(err),
			Detail: err.Error(),
			Impact: "instance identities are unknown; evidence is analysed without them",
		}}
	}
	// Exec is bound to what the target resolved to, so this is what stops a run
	// from reaching a pod outside its own scope (004 design-lld.md §5).
	if ka, isKube := adapter.(*kube.Adapter); isKube {
		ka.SetInstances(binding.Namespace, binding.Instances)
	}
	return &binding, nil
}

func (s *Service) registerSourceTools(registry *tool.Registry, target core.Target) []core.Gap {
	if !s.cfg.Source.Enabled {
		return nil
	}
	fetcher := source.New(s.cfg.Source, local.ExecRunner{})
	if err := fetcher.Available(); err != nil {
		return []core.Gap{{
			Intent: "source acquisition", Reason: core.GapUnsupported, Code: errs.CodeOf(err),
			Detail: err.Error(), Impact: "code-level analysis is unavailable for this run",
		}}
	}
	registry.MustRegister(source.Tools(fetcher, target.Kind, target.Version)...)
	return nil
}
