package service

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/loki"
	"github.com/zlrrr/multi-agent-system-turbo/internal/collector/promql"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter/kube"
	"github.com/zlrrr/multi-agent-system-turbo/internal/envadapter/local"
	"github.com/zlrrr/multi-agent-system-turbo/internal/llm"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/source"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

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
