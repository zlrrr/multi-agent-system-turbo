package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/loki"
	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/promql"
	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter/kube"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter/local"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/source"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// probeRunStore reports where runs are kept and, for an object store, whether
// the bucket answers. A store that is unreachable is not discovered until a run
// tries to save, which is the worst moment to learn about it.
func (s *Service) probeRunStore(ctx context.Context,
	add func(string, CheckStatus, string, error, time.Time), t0 time.Time) {

	switch s.cfg.Store.Type {
	case "memory":
		add("run store", CheckWarn,
			"in memory: nothing is persisted, and every run is lost when the process exits", nil, t0)
		return
	case "s3":
	default:
		add("run store", CheckOK,
			fmt.Sprintf("filesystem at %s; private to this machine", orUnset(s.cfg.Store.Dir)), nil, t0)
		return
	}

	cfg := s.cfg.Store.S3
	where := fmt.Sprintf("%s bucket %s", cfg.Endpoint, cfg.Bucket)
	probe, ok := s.store.(interface{ Probe(context.Context) error })
	if !ok {
		add("run store", CheckWarn, where+"; reachability could not be checked", nil, t0)
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := probe.Probe(probeCtx)
	cancel()
	if err != nil {
		add("run store", CheckFail, where+" — "+err.Error(), err, t0)
		return
	}
	add("run store", CheckOK, where+" reachable; shared by every replica", nil, t0)
}

// apiExposureStatus grades the API's configuration.
//
// A configuration `httpapi.Admit` would refuse is a **warning** here, not a
// failure, and the split is deliberate. `mas doctor` describes a configuration;
// `mas serve` acts on one. Most runs of this tool never open a listener at all,
// and grading an unused surface as a failure would make `mas doctor` red for
// every CLI-only operator — which is how a red build stops meaning anything.
//
// The refusal still happens, at the moment it matters, from Admit. The detail
// below says so in as many words, so nobody reads the warning as optional.
func apiExposureStatus(cfg config.ServerConfig) CheckStatus {
	if serverIsLoopback(cfg.Addr) {
		return CheckOK
	}
	switch {
	case len(cfg.Auth.Tokens) == 0:
		return CheckWarn
	case !cfg.TLS.Enabled() && !cfg.TLS.TerminatedByProxy:
		return CheckWarn
	default:
		return CheckOK
	}
}

// describeExposure says what protects the API, naming principals and scopes but
// never a token, a digest or a length.
func describeExposure(cfg config.ServerConfig) string {
	reach := "reachable off-host"
	if serverIsLoopback(cfg.Addr) {
		reach = "loopback only"
	}

	who := "no authentication configured"
	if n := len(cfg.Auth.Tokens); n > 0 {
		names := make([]string, 0, n)
		for _, t := range cfg.Auth.Tokens {
			names = append(names, fmt.Sprintf("%s[%s]", t.Name, strings.Join(t.Scopes, "+")))
		}
		who = fmt.Sprintf("%d credential(s): %s", n, strings.Join(names, ", "))
	}

	wire := "plaintext"
	switch {
	case cfg.TLS.Enabled():
		wire = "TLS served here"
	case cfg.TLS.TerminatedByProxy:
		wire = "TLS declared terminated by a proxy"
	}

	out := fmt.Sprintf("%s (%s); %s; %s", orUnset(cfg.Addr), reach, who, wire)
	if apiExposureStatus(cfg) != CheckOK {
		out += " — `mas serve` will refuse to start with this"
	}
	return out
}

// serverIsLoopback mirrors httpapi.Admit's address test. It is written here
// rather than imported because internal/httpapi imports internal/service and
// the reverse would be a cycle; a test asserts the two agree.
func serverIsLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

func orUnset(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unset)"
	}
	return s
}

// CheckStatus grades one self-check.
type CheckStatus string

const (
	CheckOK   CheckStatus = "ok"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
	CheckSkip CheckStatus = "skip"
)

// CheckResult is one line of `mas doctor` output.
type CheckResult struct {
	Name    string        `json:"name"`
	Status  CheckStatus   `json:"status"`
	Detail  string        `json:"detail"`
	Code    string        `json:"code,omitempty"`
	Remedy  string        `json:"remedy,omitempty"`
	Elapsed time.Duration `json:"elapsed"`
}

// Doctor validates the configuration and probes every configured endpoint.
//
// It reports every check rather than stopping at the first failure: an operator
// setting the tool up wants the whole list, not one problem at a time (FR-018).
func (s *Service) Doctor(ctx context.Context) []CheckResult {
	var out []CheckResult
	add := func(name string, status CheckStatus, detail string, err error, started time.Time) {
		r := CheckResult{Name: name, Status: status, Detail: detail, Elapsed: time.Since(started)}
		if err != nil {
			r.Code = errs.CodeOf(err)
			if e, ok := errs.AsError(err); ok {
				r.Remedy = e.Remedy(s.cfg.Run.Language)
			}
		}
		out = append(out, r)
	}

	t0 := time.Now()
	add("configuration", CheckOK, fmt.Sprintf("valid; %d target(s), %d environment(s)",
		len(s.cfg.Targets), len(s.cfg.Envs)), nil, t0)

	t0 = time.Now()
	add("api exposure", apiExposureStatus(s.cfg.Server), describeExposure(s.cfg.Server), nil, t0)

	t0 = time.Now()
	s.probeRunStore(ctx, add, t0)

	t0 = time.Now()
	if s.library.Len() == 0 {
		add("knowledge packs", CheckFail, "no packs loaded", nil, t0)
	} else {
		detail := fmt.Sprintf("%d pack(s) covering %v", s.library.Len(), s.library.Middlewares())
		status := CheckOK
		if probs := s.library.Problems(); len(probs) > 0 {
			status = CheckWarn
			detail += fmt.Sprintf("; %d pack(s) failed to load: %v", len(probs), probs[0])
		}
		add("knowledge packs", status, detail, nil, t0)
	}

	t0 = time.Now()
	add("topologies", CheckOK, fmt.Sprintf("available: %v", orchestrator.Names()), nil, t0)

	t0 = time.Now()
	maxBytes, maxTimeout := s.guard.Limits()
	add("safety guard", CheckOK, fmt.Sprintf(
		"read-only enforced; %d allow-listed command(s), %d allow-listed read path(s), "+
			"max response %d bytes, max timeout %s",
		len(s.guard.AllowedCommands()), len(s.guard.AllowedPaths()), maxBytes, maxTimeout), nil, t0)

	hc := &http.Client{Timeout: 10 * time.Second}
	for _, ms := range s.cfg.Telemetry.Metrics {
		t0 = time.Now()
		c := promql.New(ms, hc)
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.Health(probeCtx)
		cancel()
		if err != nil {
			add("metrics: "+ms.Name, CheckFail, ms.URL+" — "+err.Error(), err, t0)
		} else {
			add("metrics: "+ms.Name, CheckOK, ms.URL+" reachable", nil, t0)
		}
	}
	if len(s.cfg.Telemetry.Metrics) == 0 {
		add("metrics", CheckWarn, "no metrics source configured; metric evidence is unavailable", nil, time.Now())
	}

	for _, ls := range s.cfg.Telemetry.Logs {
		t0 = time.Now()
		c := loki.New(ls, hc)
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.Health(probeCtx)
		cancel()
		if err != nil {
			add("logs: "+ls.Name, CheckFail, ls.URL+" — "+err.Error(), err, t0)
		} else {
			add("logs: "+ls.Name, CheckOK, ls.URL+" reachable", nil, t0)
		}
	}
	if len(s.cfg.Telemetry.Logs) == 0 {
		add("logs", CheckWarn, "no log source configured; log evidence is unavailable", nil, time.Now())
	}

	names := make([]string, 0, len(s.cfg.Envs))
	for name := range s.cfg.Envs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t0 = time.Now()
		adapter, err := envadapter.Open(name, s.cfg.Envs[name])
		if err != nil {
			add("environment: "+name, CheckWarn, err.Error(), err, t0)
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = adapter.Probe(probeCtx)
		cancel()
		if err != nil {
			add("environment: "+name, CheckWarn, err.Error(), err, t0)
		} else {
			add("environment: "+name, CheckOK,
				fmt.Sprintf("%s reachable, %d tool(s)", s.cfg.Envs[name].Type, len(adapter.Tools())), nil, t0)
		}

		// In-container execution is reported separately, because "no exec tool"
		// has two very different causes — a policy decision and a missing
		// capability — and an operator who cannot tell them apart will go
		// looking for the wrong problem.
		if ka, isKube := adapter.(*kube.Adapter); isKube {
			t0 = time.Now()
			ka.SetExecEnabled(s.cfg.Envs[name].ExecEnabled())
			if ok, reason := ka.ExecAvailable(); ok {
				add("environment: "+name+" · exec", CheckOK,
					"in-container execution is available; the guard's read-only allow-list "+
						"still decides every command", nil, t0)
			} else {
				add("environment: "+name+" · exec", CheckSkip, reason.Error(), nil, t0)
			}
		}
	}

	t0 = time.Now()
	if !s.cfg.Source.Enabled {
		add("source acquisition", CheckSkip, "disabled in configuration", nil, t0)
	} else {
		f := source.New(s.cfg.Source, local.ExecRunner{})
		if err := f.Available(); err != nil {
			add("source acquisition", CheckWarn, err.Error(), err, t0)
		} else {
			add("source acquisition", CheckOK, fmt.Sprintf(
				"git available; %d repo(s), %d mirror(s), cache %s",
				len(s.cfg.Source.Repos), len(s.cfg.Source.Mirrors), s.cfg.Source.CacheDir), nil, t0)
		}
	}

	// Pricing is reported separately from the provider: an unpriced model is not
	// a failure, it is a stated unknown, and an operator who sees "cost: $0.00"
	// nowhere in their report deserves to know why.
	t0 = time.Now()
	if router, rerr := llm.NewRouter(s.cfg.LLM); rerr == nil {
		pricing := llm.Pricing(s.cfg.LLM.Pricing)
		var unpriced []string
		for _, m := range router.Models() {
			if !pricing.Priced(m) {
				unpriced = append(unpriced, m)
			}
		}
		_ = router.Close()
		switch {
		case len(router.Models()) == 0:
			add("model pricing", CheckSkip, "no model is configured", nil, t0)
		case len(unpriced) == 0:
			add("model pricing", CheckOK,
				fmt.Sprintf("every model this run would use is priced (%s)",
					strings.Join(router.Models(), ", ")), nil, t0)
		default:
			add("model pricing", CheckSkip, fmt.Sprintf(
				"not priced: %s — reports will state the cost as unknown rather than as zero; "+
					"set llm.pricing to get a figure", strings.Join(unpriced, ", ")), nil, t0)
		}
	}

	t0 = time.Now()
	provider, err := llm.Open(s.cfg.LLM)
	switch {
	case err != nil:
		add("llm provider", CheckFail, err.Error(), err, t0)
	case s.cfg.LLM.Provider == "mock":
		_ = provider.Close()
		add("llm provider", CheckWarn,
			"the mock provider is configured; analysis will use a scripted transcript, not a real model", nil, t0)
	default:
		_ = provider.Close()
		detail := fmt.Sprintf("%s configured with model %s", s.cfg.LLM.Provider, s.cfg.LLM.Model)
		status := CheckOK
		if s.cfg.LLM.APIKey.IsZero() && s.cfg.LLM.BaseURL == "" {
			status = CheckWarn
			detail += "; no API key is set"
		}
		add("llm provider", status, detail, nil, t0)
	}

	t0 = time.Now()
	testID := "doctor-probe"
	testRec := &core.RunRecord{ID: testID, Status: core.RunRunning, StartedAt: time.Now().UTC()}
	if err := s.store.Create(ctx, testRec); err != nil {
		add("run store", CheckFail, err.Error(), err, t0)
	} else if _, err := s.store.Get(ctx, testID); err != nil {
		add("run store", CheckFail, "written but not readable: "+err.Error(), err, t0)
	} else {
		add("run store", CheckOK, fmt.Sprintf("%s store is writable and readable", s.cfg.Store.Type), nil, t0)
	}

	for i, t := range s.cfg.Targets {
		t0 = time.Now()
		_, perr := s.library.For(core.MiddlewareKind(t.Kind), t.Version)
		name := fmt.Sprintf("target: %s", t.ID)
		if perr != nil {
			add(name, CheckWarn,
				fmt.Sprintf("no knowledge pack for %s; only generic checks apply", t.Kind), perr, t0)
			continue
		}
		add(name, CheckOK, fmt.Sprintf("%s in %s, knowledge pack available", t.Kind, orNone(t.Env)), nil, t0)
		_ = i
	}
	if len(s.cfg.Targets) == 0 {
		add("targets", CheckWarn, "no targets configured; `mas diagnose` has nothing to point at", nil, time.Now())
	}

	return out
}

func orNone(s string) string {
	if s == "" {
		return "(no environment)"
	}
	return s
}

// DoctorOK reports whether any check failed outright.
func DoctorOK(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == CheckFail {
			return false
		}
	}
	return true
}
