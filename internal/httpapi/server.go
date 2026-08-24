// Package httpapi is the HTTP surface: the diagnosis API, health endpoints and
// Prometheus self-metrics.
//
// Governs: specs/001-mvp-core/design-lld.md §2.16
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/internal/orchestrator"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
	"github.com/zlrrr/multi-agent-system-turbo/internal/version"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Server exposes a Service over HTTP.
type Server struct {
	svc     *service.Service
	mux     *http.ServeMux
	started time.Time

	mu      sync.Mutex
	running map[string]bool
}

// New builds a server around a Service.
func New(svc *service.Service) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux(), started: time.Now(), running: map[string]bool{}}
	s.routes()
	return s
}

// Handler returns the router, which is what tests exercise.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/api/v1/diagnoses", s.handleDiagnoses)
	s.mux.HandleFunc("/api/v1/diagnoses/", s.handleDiagnosis)
	s.mux.HandleFunc("/api/v1/targets", s.handleTargets)
	s.mux.HandleFunc("/api/v1/topologies", s.handleTopologies)
	s.mux.HandleFunc("/api/v1/packs", s.handlePacks)
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/readyz", s.handleReadyz)
	s.mux.HandleFunc("/metrics", s.handleMetrics)
	s.mux.HandleFunc("/", s.handleIndex)
}

// errorBody is the wire form of a failure: always a code, so a client can react
// programmatically rather than by matching on prose.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Remedy  string `json:"remedy,omitempty"`
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	lang := s.svc.Config().Run.Language
	body := errorBody{Code: errs.CodeOf(err), Message: err.Error()}
	if e, ok := errs.AsError(err); ok {
		body.Code, body.Message, body.Remedy = e.Code(), e.Message(lang), e.Remedy(lang)
	}
	if body.Code == "" {
		body.Code = "MAS-7003"
	}
	obs.Log(r.Context()).Warn("request failed",
		"path", r.URL.Path, "status", status, "code", body.Code)
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// statusFor maps an error domain onto an HTTP status. A safety refusal is 403:
// the request was understood and deliberately denied.
func statusFor(err error) int {
	code := errs.CodeOf(err)
	switch errs.Domain(code) {
	case "config":
		if code == "MAS-1005" {
			return http.StatusNotFound
		}
		return http.StatusBadRequest
	case "safety":
		return http.StatusForbidden
	case "storage":
		if code == "MAS-6001" {
			return http.StatusNotFound
		}
		return http.StatusInternalServerError
	case "orchestration":
		if code == "MAS-3001" {
			return http.StatusBadRequest
		}
		return http.StatusInternalServerError
	case "llm", "collector":
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// diagnoseRequestBody is the public request shape. It uses a duration string
// rather than timestamps for the common case, because that is what an operator
// reaching for an API during an incident actually has.
type diagnoseRequestBody struct {
	Target   string            `json:"target"`
	Symptom  string            `json:"symptom"`
	Since    string            `json:"since,omitempty"`
	From     *time.Time        `json:"from,omitempty"`
	To       *time.Time        `json:"to,omitempty"`
	Mode     string            `json:"mode,omitempty"`
	Topology string            `json:"topology,omitempty"`
	Language string            `json:"language,omitempty"`
	Options  map[string]string `json:"options,omitempty"`
}

func (b diagnoseRequestBody) toCore() (core.DiagnoseRequest, error) {
	req := core.DiagnoseRequest{
		Target: b.Target, Symptom: b.Symptom, Mode: core.Mode(b.Mode),
		Topology: b.Topology, Language: b.Language, Options: b.Options,
	}
	switch {
	case b.From != nil && b.To != nil:
		req.Window = core.Window{From: *b.From, To: *b.To}
	case b.From != nil || b.To != nil:
		return req, errs.New("MAS-7001", "from and to must be given together")
	case b.Since != "":
		d, err := time.ParseDuration(b.Since)
		if err != nil || d <= 0 {
			return req, errs.New("MAS-7001", "since must be a positive duration such as 1h")
		}
		now := time.Now().UTC()
		req.Window = core.Window{From: now.Add(-d), To: now}
	}
	return req, nil
}

func (s *Server) handleDiagnoses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createDiagnosis(w, r)
	case http.MethodGet:
		s.listDiagnoses(w, r)
	default:
		s.writeError(w, r, http.StatusMethodNotAllowed, errs.New("MAS-7002", r.Method, r.URL.Path))
	}
}

func (s *Server) createDiagnosis(w http.ResponseWriter, r *http.Request) {
	var body diagnoseRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		s.writeError(w, r, http.StatusBadRequest, errs.Wrap(err, "MAS-7001", "the body is not valid JSON"))
		return
	}
	req, err := body.toCore()
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err)
		return
	}
	// Admission runs before anything is created, so a malformed request never
	// leaves a half-formed run behind.
	admitted, err := s.svc.Admit(req)
	if err != nil {
		s.writeError(w, r, statusFor(err), err)
		return
	}

	if r.URL.Query().Get("wait") == "true" {
		rep, err := s.svc.Diagnose(r.Context(), admitted)
		if err != nil {
			s.writeError(w, r, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, rep)
		return
	}

	runID := service.NewRunID(time.Now())
	s.mu.Lock()
	s.running[runID] = true
	s.mu.Unlock()

	go func() {
		// A detached run must not be cancelled when the client's request ends.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()),
			admitted.Budget.MaxWall+2*time.Minute)
		defer cancel()
		rep, err := s.svc.Diagnose(ctx, admitted)
		s.mu.Lock()
		delete(s.running, runID)
		s.mu.Unlock()
		if err != nil {
			obs.Log(ctx).Error("background diagnosis failed", "code", errs.CodeOf(err), "error", err.Error())
			return
		}
		obs.Log(ctx).Info("background diagnosis complete", "run_id", rep.RunID)
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":  "accepted",
		"message": "the diagnosis is running; poll GET /api/v1/diagnoses to find it, or use ?wait=true",
		"request": admitted,
	})
}

func (s *Server) listDiagnoses(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := s.svc.Runs(r.Context(), limit)
	if err != nil {
		s.writeError(w, r, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "count": len(runs)})
}

func (s *Server) handleDiagnosis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, http.StatusMethodNotAllowed, errs.New("MAS-7002", r.Method, r.URL.Path))
		return
	}
	id := r.URL.Path[len("/api/v1/diagnoses/"):]
	if id == "" {
		s.writeError(w, r, http.StatusNotFound, errs.New("MAS-7404", "a run id is required"))
		return
	}
	rec, err := s.svc.Run(r.Context(), id)
	if err != nil {
		s.writeError(w, r, statusFor(err), err)
		return
	}
	if r.URL.Query().Get("steps") == "true" {
		writeJSON(w, http.StatusOK, rec)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": rec.ID, "status": rec.Status, "started_at": rec.StartedAt,
		"finished_at": rec.FinishedAt, "usage": rec.Usage, "versions": rec.Versions,
		"report": rec.Report,
	})
}

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, http.StatusMethodNotAllowed, errs.New("MAS-7002", r.Method, r.URL.Path))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": s.svc.Config().Targets})
}

func (s *Server) handleTopologies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, http.StatusMethodNotAllowed, errs.New("MAS-7002", r.Method, r.URL.Path))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"topologies": orchestrator.Names(), "descriptions": orchestrator.Descriptions(),
		"default": s.svc.Config().Run.DefaultTopology,
	})
}

func (s *Server) handlePacks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, http.StatusMethodNotAllowed, errs.New("MAS-7002", r.Method, r.URL.Path))
		return
	}
	type packInfo struct {
		ID           string `json:"id"`
		Middleware   string `json:"middleware"`
		Version      string `json:"version"`
		VersionRange string `json:"version_range,omitempty"`
		Signals      int    `json:"signals"`
		LogPatterns  int    `json:"log_patterns"`
		FailureModes int    `json:"failure_modes"`
		Playbooks    int    `json:"playbooks"`
	}
	out := []packInfo{}
	for _, p := range s.svc.Library().All() {
		out = append(out, packInfo{
			ID: p.ID(), Middleware: p.Metadata.Middleware, Version: p.Metadata.Version,
			VersionRange: p.Metadata.VersionRange, Signals: len(p.Signals),
			LogPatterns: len(p.LogPatterns), FailureModes: len(p.FailureModes), Playbooks: len(p.Playbooks),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"packs": out, "count": len(out)})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": version.Get().Version,
		"uptime_seconds": int(time.Since(s.started).Seconds()),
	})
}

// handleReadyz reports readiness to serve, which is narrower than liveness: the
// process is only ready once its knowledge and store are usable.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.svc.Library().Len() == 0 {
		s.writeError(w, r, http.StatusServiceUnavailable, errs.New("MAS-5003", "(no packs loaded)"))
		return
	}
	if _, err := s.svc.Runs(r.Context(), 1); err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	s.mu.Lock()
	inflight := len(s.running)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ready", "packs": s.svc.Library().Len(), "in_flight": inflight,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, http.StatusMethodNotAllowed, errs.New("MAS-7002", r.Method, r.URL.Path))
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if err := s.svc.Metrics().WriteProm(w); err != nil {
		obs.Log(r.Context()).Warn("metrics exposition failed", "error", err.Error())
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.writeError(w, r, http.StatusNotFound, errs.New("MAS-7404", r.URL.Path))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "mas-turbo", "version": version.Get().Version,
		"description": "Read-only diagnostic multi-agent system for open-source middleware",
		"endpoints": []string{
			"POST /api/v1/diagnoses", "GET /api/v1/diagnoses", "GET /api/v1/diagnoses/{id}",
			"GET /api/v1/targets", "GET /api/v1/topologies", "GET /api/v1/packs",
			"GET /healthz", "GET /readyz", "GET /metrics",
		},
	})
}

// Serve runs the HTTP server until the context is cancelled or the process is
// signalled, then shuts down gracefully so an in-flight diagnosis can finish.
func Serve(ctx context.Context, svc *service.Service) error {
	cfg := svc.Config().Server
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           New(svc).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.ReadTimeout.D(),
		WriteTimeout:      cfg.WriteTimeout.D(),
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- errs.Wrap(err, "MAS-7005", cfg.Addr, err.Error())
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return errs.Wrap(err, "MAS-7005", cfg.Addr, err.Error())
		}
		return nil
	}
}
