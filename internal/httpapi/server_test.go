package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/httpapi"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
	"github.com/zlrrr/multi-agent-system-turbo/internal/store"
)

func stubTelemetry(t *testing.T) (string, string) {
	t.Helper()
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		q := r.Form.Get("query")
		value := "1"
		switch {
		case strings.Contains(q, "redis_memory_used_bytes"):
			value = "990"
		case strings.Contains(q, "redis_memory_max_bytes"):
			value = "1000"
		}
		if strings.Contains(r.URL.Path, "query_range") {
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[
			  {"metric":{"instance":"redis-0"},"values":[[1724400000,%q]]}]}}`, value)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[
		  {"metric":{"instance":"redis-0"},"value":[1724400000,%q]}]}}`, value)
	}))
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	t.Cleanup(func() { prom.Close(); loki.Close() })
	return prom.URL, loki.URL
}

func newServer(t *testing.T) *httpapi.Server {
	t.Helper()
	promURL, lokiURL := stubTelemetry(t)
	cfg := config.Default()
	cfg.Log.Level = "error"
	cfg.Source.Enabled = false
	cfg.Store = config.StoreConfig{Type: "memory"}
	cfg.LLM = config.LLMConfig{Provider: "mock", Model: "mock-1"}
	cfg.Run.DeterministicShortCircuit = 0.85
	cfg.Telemetry.Metrics = []config.MetricsSource{{
		Name: "primary", Type: "prometheus", URL: promURL,
		Timeout: config.Duration(2 * time.Second), MaxSamples: 100,
	}}
	cfg.Telemetry.Logs = []config.LogsSource{{
		Name: "primary", Type: "loki", URL: lokiURL,
		Timeout: config.Duration(2 * time.Second), MaxLines: 100,
	}}
	cfg.Targets = []config.TargetConfig{{
		ID: "redis-prod", Kind: "redis", Version: "7.2.4",
		Labels: map[string]string{"instance": "redis-0"},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Options{Config: cfg, Store: store.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return httpapi.New(svc)
}

func do(t *testing.T, s *httpapi.Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON (%d): %s", w.Code, w.Body.String())
	}
	return out
}

func TestIndex(t *testing.T) {
	w := do(t, newServer(t), http.MethodGet, "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	body := decode(t, w)
	if body["name"] != "mas-turbo" {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["endpoints"]; !ok {
		t.Error("the index does not list its endpoints")
	}
}

func TestHealthzAndReadyz(t *testing.T) {
	s := newServer(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		w := do(t, s, http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s code = %d: %s", path, w.Code, w.Body.String())
		}
		body := decode(t, w)
		if body["status"] == "" {
			t.Errorf("%s body = %v", path, body)
		}
	}
}

func TestMetricsExposition(t *testing.T) {
	s := newServer(t)
	// Run a diagnosis so the registry has something to report.
	do(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", map[string]any{
		"target": "redis-prod", "symptom": "latency", "since": "1h",
	})
	w := do(t, s, http.MethodGet, "/metrics", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"mas_runs_total", "# TYPE", "mas_tool_calls_total"} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition is missing %q", want)
		}
	}
}

func TestCreateDiagnosisWaiting(t *testing.T) {
	s := newServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", map[string]any{
		"target": "redis-prod", "symptom": "p99 latency spike with evictions", "since": "1h",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	body := decode(t, w)
	if body["schema"] != "report/v1" {
		t.Fatalf("schema = %v", body["schema"])
	}
	if body["run_id"] == "" {
		t.Error("no run id")
	}
	if _, ok := body["hypotheses"]; !ok {
		t.Error("the report has no hypotheses field")
	}
}

func TestCreateDiagnosisAsync(t *testing.T) {
	s := newServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/diagnoses", map[string]any{
		"target": "redis-prod", "symptom": "latency", "since": "1h",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	body := decode(t, w)
	if body["status"] != "accepted" {
		t.Fatalf("body = %v", body)
	}
	// The accepted request must echo what was actually admitted, so a caller can
	// see the defaults that were applied.
	req, ok := body["request"].(map[string]any)
	if !ok || req["topology"] == "" {
		t.Fatalf("request echo = %v", body["request"])
	}
}

func TestCreateDiagnosisValidation(t *testing.T) {
	s := newServer(t)
	cases := map[string]struct {
		body   any
		status int
		code   string
	}{
		"no target":        {map[string]any{"symptom": "x"}, http.StatusBadRequest, "MAS-1007"},
		"no symptom":       {map[string]any{"target": "redis-prod"}, http.StatusBadRequest, "MAS-1007"},
		"unknown target":   {map[string]any{"target": "ghost", "symptom": "x"}, http.StatusNotFound, "MAS-1005"},
		"unknown topology": {map[string]any{"target": "redis-prod", "symptom": "x", "topology": "debate"}, http.StatusBadRequest, "MAS-3001"},
		"bad mode":         {map[string]any{"target": "redis-prod", "symptom": "x", "mode": "hybrid"}, http.StatusBadRequest, "MAS-1011"},
		"bad since":        {map[string]any{"target": "redis-prod", "symptom": "x", "since": "soon"}, http.StatusBadRequest, "MAS-7001"},
		"half window":      {map[string]any{"target": "redis-prod", "symptom": "x", "from": "2026-01-01T00:00:00Z"}, http.StatusBadRequest, "MAS-7001"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := do(t, s, http.MethodPost, "/api/v1/diagnoses", tc.body)
			if w.Code != tc.status {
				t.Fatalf("code = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			body := decode(t, w)
			if body["code"] != tc.code {
				t.Fatalf("code = %v, want %s", body["code"], tc.code)
			}
			if body["message"] == "" {
				t.Error("the error carries no message")
			}
		})
	}
}

func TestMalformedBodyIsCoded(t *testing.T) {
	s := newServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/diagnoses", strings.NewReader("{not json"))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
	if decode(t, w)["code"] != "MAS-7001" {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestGetAndListDiagnoses(t *testing.T) {
	s := newServer(t)
	created := do(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", map[string]any{
		"target": "redis-prod", "symptom": "latency", "since": "1h",
	})
	runID, _ := decode(t, created)["run_id"].(string)
	if runID == "" {
		t.Fatal("no run id from the creating call")
	}

	list := do(t, s, http.MethodGet, "/api/v1/diagnoses", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list code = %d", list.Code)
	}
	if n, _ := decode(t, list)["count"].(float64); n < 1 {
		t.Fatalf("count = %v", n)
	}

	got := do(t, s, http.MethodGet, "/api/v1/diagnoses/"+runID, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get code = %d: %s", got.Code, got.Body.String())
	}
	body := decode(t, got)
	if body["id"] != runID || body["status"] != "completed" {
		t.Fatalf("body = %v", body)
	}
	if body["report"] == nil {
		t.Error("the run carries no report")
	}

	withSteps := do(t, s, http.MethodGet, "/api/v1/diagnoses/"+runID+"?steps=true", nil)
	if _, ok := decode(t, withSteps)["steps"]; !ok {
		t.Error("?steps=true did not include the audit trail")
	}
}

func TestGetUnknownDiagnosis(t *testing.T) {
	s := newServer(t)
	w := do(t, s, http.MethodGet, "/api/v1/diagnoses/run-nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d", w.Code)
	}
	if decode(t, w)["code"] != "MAS-6001" {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestListingEndpoints(t *testing.T) {
	s := newServer(t)
	for path, key := range map[string]string{
		"/api/v1/targets":    "targets",
		"/api/v1/topologies": "topologies",
		"/api/v1/packs":      "packs",
	} {
		w := do(t, s, http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s code = %d", path, w.Code)
			continue
		}
		if _, ok := decode(t, w)[key]; !ok {
			t.Errorf("%s response has no %q field: %s", path, key, w.Body.String())
		}
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := newServer(t)
	for _, path := range []string{"/api/v1/targets", "/api/v1/topologies", "/api/v1/packs", "/metrics"} {
		w := do(t, s, http.MethodDelete, path, nil)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s DELETE code = %d, want 405", path, w.Code)
		}
		if decode(t, w)["code"] != "MAS-7002" {
			t.Errorf("%s body = %s", path, w.Body.String())
		}
	}
}

func TestUnknownPathIsCoded(t *testing.T) {
	w := do(t, newServer(t), http.MethodGet, "/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d", w.Code)
	}
	if decode(t, w)["code"] != "MAS-7404" {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// TestErrorsAlwaysCarryACode is the FR-017 gate at the API boundary.
func TestErrorsAlwaysCarryACode(t *testing.T) {
	s := newServer(t)
	requests := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/diagnoses", map[string]any{"symptom": "x"}},
		{http.MethodGet, "/api/v1/diagnoses/ghost", nil},
		{http.MethodDelete, "/api/v1/targets", nil},
		{http.MethodGet, "/does/not/exist", nil},
	}
	for _, r := range requests {
		w := do(t, s, r.method, r.path, r.body)
		if w.Code < 400 {
			t.Errorf("%s %s returned %d; expected a failure", r.method, r.path, w.Code)
			continue
		}
		body := decode(t, w)
		code, _ := body["code"].(string)
		if !strings.HasPrefix(code, "MAS-") {
			t.Errorf("%s %s returned an uncoded error: %s", r.method, r.path, w.Body.String())
		}
	}
}

func TestLanguageIsHonoured(t *testing.T) {
	s := newServer(t)
	w := do(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", map[string]any{
		"target": "redis-prod", "symptom": "内存告警", "since": "1h", "language": "zh",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	if !containsHan(w.Body.String()) {
		t.Fatal("a zh request produced no Chinese text")
	}
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
