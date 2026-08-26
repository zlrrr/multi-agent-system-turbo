package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/httpapi"
)

const readerToken = "reader-token-value"
const oncallToken = "oncall-token-value"

// withTokens configures one read-only credential and one that may also start a
// diagnosis — the split a status page and an on-call engineer actually need.
func withTokens(cfg *config.Config) {
	cfg.Server.Auth = config.ServerAuth{Tokens: []config.APIToken{
		{Name: "dashboard", Token: config.Secret(readerToken), Scopes: []string{"read"}},
		{Name: "oncall", Token: config.Secret(oncallToken), Scopes: []string{"read", "diagnose"}},
	}}
}

func request(t *testing.T, s *httpapi.Server, method, path, token string, body any) *httptest.ResponseRecorder {
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
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestAnonymousRequestIsRefused is FR-001. A missing credential and an unknown
// one deliberately produce the same code and body: telling an attacker which
// half is wrong tells them which half to work on.
func TestAnonymousRequestIsRefused(t *testing.T) {
	s := newServerWith(t, withTokens)

	for _, c := range []struct{ name, token string }{
		{"no credential", ""},
		{"unknown token", "not-a-configured-token"},
		{"right shape, wrong value", readerToken + "x"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := request(t, s, http.MethodGet, "/api/v1/targets", c.token, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401\n%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "MAS-7012") {
				t.Errorf("the refusal carries no code:\n%s", w.Body.String())
			}
		})
	}

	// A malformed header is the same refusal, not a 500.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil)
	r.Header.Set("Authorization", "Basic "+readerToken)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a non-bearer scheme produced %d, want 401", w.Code)
	}
}

// TestScopeIsEnforcedPerRoute is FR-002. Starting a diagnosis spends model
// tokens and reads production; a status page needs neither.
func TestScopeIsEnforcedPerRoute(t *testing.T) {
	s := newServerWith(t, withTokens)
	body := map[string]any{"target": "redis-prod", "symptom": "latency", "since": "1h"}

	// The read-only credential reads.
	if w := request(t, s, http.MethodGet, "/api/v1/targets", readerToken, nil); w.Code != http.StatusOK {
		t.Errorf("read scope could not read targets: %d\n%s", w.Code, w.Body.String())
	}

	// …and may not spend.
	w := request(t, s, http.MethodPost, "/api/v1/diagnoses", readerToken, body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("read scope started a diagnosis: %d\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "MAS-7014") {
		t.Errorf("the refusal carries no code:\n%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "diagnose") {
		t.Errorf("the refusal does not name the scope required:\n%s", w.Body.String())
	}

	// The on-call credential may.
	if w := request(t, s, http.MethodPost, "/api/v1/diagnoses", oncallToken, body); w.Code != http.StatusAccepted &&
		w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Errorf("diagnose scope was refused: %d\n%s", w.Code, w.Body.String())
	}
}

// TestHealthEndpointsStayAnonymous is FR-005: a liveness probe that needs a
// credential is a liveness probe that fails during a credential problem.
func TestHealthEndpointsStayAnonymous(t *testing.T) {
	s := newServerWith(t, withTokens)
	for _, path := range []string{"/healthz", "/readyz"} {
		if w := request(t, s, http.MethodGet, path, "", nil); w.Code != http.StatusOK {
			t.Errorf("%s needed a credential: %d\n%s", path, w.Code, w.Body.String())
		}
	}
	// Metrics are not health: they carry target names and run counts.
	if w := request(t, s, http.MethodGet, "/metrics", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("/metrics answered anonymously: %d", w.Code)
	}
	if w := request(t, s, http.MethodGet, "/metrics", readerToken, nil); w.Code != http.StatusOK {
		t.Errorf("/metrics refused a read credential: %d", w.Code)
	}
}

// TestEveryRouteIsGuarded is FR-006, CON-001 and CON-002: deny by default, so
// adding a handler without wiring its scope fails closed rather than open.
func TestEveryRouteIsGuarded(t *testing.T) {
	s := newServerWith(t, withTokens)

	anonymous := map[string]bool{"/healthz": true, "/readyz": true}
	for _, route := range s.Routes() {
		path := route
		if strings.HasSuffix(path, "/") && path != "/" {
			path += "sample"
		}
		w := request(t, s, http.MethodGet, path, "", nil)
		if anonymous[route] {
			if w.Code == http.StatusUnauthorized {
				t.Errorf("%s is meant to be anonymous but demanded a credential", route)
			}
			continue
		}
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s answered without a credential (%d); every route must be "+
				"either guarded or deliberately anonymous", route, w.Code)
		}
	}

	// An unregistered path must not become a hole either.
	if w := request(t, s, http.MethodGet, "/api/v1/whatever-comes-next", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("an unknown path answered without a credential: %d", w.Code)
	}
	// …and with a credential it is still not granted a scope it has no entry for.
	if w := request(t, s, http.MethodPatch, "/api/v1/targets", readerToken, nil); w.Code == http.StatusOK {
		t.Errorf("an unhandled method on a known route was allowed through")
	}
}

// TestCredentialsAreNeverEchoed is FR-004 and CON-004.
func TestCredentialsAreNeverEchoed(t *testing.T) {
	var logged bytes.Buffer
	s := newServerWith(t, func(cfg *config.Config) {
		withTokens(cfg)
		cfg.Log.Level = "debug"
	})

	// Every response body, on every outcome.
	for _, c := range []struct{ path, token string }{
		{"/api/v1/targets", ""},
		{"/api/v1/targets", readerToken},
		{"/api/v1/targets", "wrong-" + readerToken},
	} {
		w := request(t, s, http.MethodGet, c.path, c.token, nil)
		if strings.Contains(w.Body.String(), readerToken) ||
			strings.Contains(w.Body.String(), oncallToken) {
			t.Errorf("a response echoed a credential:\n%s", w.Body.String())
		}
	}

	// The rendered configuration.
	cfg := config.Default()
	withTokens(cfg)
	rendered, err := json.Marshal(cfg.Server)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), readerToken) {
		t.Errorf("the rendered configuration contains a credential:\n%s", rendered)
	}

	// The audit log.
	auth, err := httpapi.NewAuthorizer(cfg.Server, "en",
		slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	handler := auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil)
	r.Header.Set("Authorization", "Bearer "+readerToken)
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if strings.Contains(logged.String(), readerToken) {
		t.Errorf("the audit log contains the credential:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "dashboard") {
		t.Errorf("the audit log does not name the principal:\n%s", logged.String())
	}
	_ = os.Stdout
}

// TestTokenComparisonIsConstantTime is FR-003, checked structurally. Behaviour
// cannot distinguish a constant-time comparison from a lucky one, and a timing
// test on a shared CI runner measures the runner.
func TestTokenComparisonIsConstantTime(t *testing.T) {
	src, err := os.ReadFile("auth.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	if !strings.Contains(body, "subtle.ConstantTimeCompare") {
		t.Error("token comparison does not use crypto/subtle")
	}
	// Both sides are reduced to a fixed-size digest first, so the comparison
	// runs over equal lengths and length never branches.
	if !strings.Contains(body, "sha256.Sum256") {
		t.Error("tokens are not reduced to a fixed-size digest before comparison")
	}
	// An == on the raw token would short-circuit on the first differing byte.
	for _, banned := range []string{
		`presented == `, `== presented`,
		`strings.EqualFold(presented`, `bytes.Equal(digest`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("a non-constant-time comparison appears in the credential path: %q", banned)
		}
	}
}

// TestAuthDecisionsAreAudited is FR-012. Grants as well as refusals: a log that
// shows only denials cannot answer "who ran this", which is the question
// actually asked afterwards.
func TestAuthDecisionsAreAudited(t *testing.T) {
	cfg := config.Default()
	withTokens(cfg)

	var logged bytes.Buffer
	auth, err := httpapi.NewAuthorizer(cfg.Server, "en",
		slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err != nil {
		t.Fatal(err)
	}
	handler := auth.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	send := func(method, path, token string) {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}

	send(http.MethodGet, "/api/v1/targets", oncallToken)    // allowed
	send(http.MethodPost, "/api/v1/diagnoses", readerToken) // denied, wrong scope
	send(http.MethodGet, "/api/v1/targets", "")             // denied, no credential

	out := logged.String()
	for _, want := range []string{
		"principal=oncall", "outcome=allowed",
		"principal=dashboard", "outcome=denied",
		"principal=anonymous",
		"/api/v1/diagnoses",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the audit log is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Authorization") || strings.Contains(out, oncallToken) {
		t.Errorf("the audit log leaked the credential or its header:\n%s", out)
	}
}

// TestRunRecordCarriesThePrincipal is FR-011. Authorisation without
// attribution is half a feature: when a diagnosis turns out to have been
// expensive, there has to be something to look at.
func TestRunRecordCarriesThePrincipal(t *testing.T) {
	s := newServerWith(t, withTokens)
	body := map[string]any{
		"target": "redis-prod", "symptom": "latency spike", "since": "1h", "wait": true,
	}

	w := request(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", oncallToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	var report struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}

	list := request(t, s, http.MethodGet, "/api/v1/diagnoses/"+report.RunID, oncallToken, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("could not read the run back: %d\n%s", list.Code, list.Body.String())
	}
	var record struct {
		Principal string `json:"principal"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.Principal != "oncall" {
		t.Errorf("the run record says the caller was %q, want oncall", record.Principal)
	}
}
