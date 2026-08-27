package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
)

const (
	paymentsToken = "payments-token-value"
	searchToken   = "search-token-value"
	platformToken = "platform-token-value"
)

// withTenants configures two tenanted targets and three credentials: one per
// tenant, and one that spans both.
func withTenants(cfg *config.Config) {
	cfg.Targets = []config.TargetConfig{
		{ID: "payments-redis", Kind: "redis", Version: "7.2.4",
			Labels: map[string]string{"instance": "redis-0"}, Tenant: "payments"},
		{ID: "search-redis", Kind: "redis", Version: "7.2.4",
			Labels: map[string]string{"instance": "redis-0"}, Tenant: "search"},
	}
	cfg.Server.Auth = config.ServerAuth{Tokens: []config.APIToken{
		{Name: "payments-oncall", Token: paymentsToken,
			Scopes: []string{"read", "diagnose"}, Tenants: []string{"payments"}},
		{Name: "search-oncall", Token: searchToken,
			Scopes: []string{"read", "diagnose"}, Tenants: []string{"search"}},
		{Name: "platform", Token: platformToken,
			Scopes: []string{"read", "diagnose"}, Tenants: []string{"payments", "search"}},
	}}
}

func diagnoseBody(target string) map[string]any {
	return map[string]any{"target": target, "symptom": "latency spike", "since": "1h"}
}

// TestDiagnosingAnotherTenantIsRefused is FR-005.
func TestDiagnosingAnotherTenantIsRefused(t *testing.T) {
	s := newServerWith(t, withTenants)

	// Its own target: allowed.
	w := request(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", paymentsToken,
		diagnoseBody("payments-redis"))
	if w.Code != http.StatusOK {
		t.Fatalf("a tenant could not diagnose its own target: %d\n%s", w.Code, w.Body.String())
	}

	// Somebody else's: refused.
	w = request(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", paymentsToken,
		diagnoseBody("search-redis"))
	if w.Code == http.StatusOK {
		t.Fatalf("a tenant diagnosed another tenant's target\n%s", w.Body.String())
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 — a 403 would confirm the target exists", w.Code)
	}

	// A credential spanning both reaches both.
	for _, target := range []string{"payments-redis", "search-redis"} {
		w := request(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", platformToken,
			diagnoseBody(target))
		if w.Code != http.StatusOK {
			t.Errorf("the platform credential could not reach %s: %d", target, w.Code)
		}
	}
}

// TestCrossTenantRefusalRevealsNothing is FR-009 and CON-003. A 403 naming the
// target confirms it exists, which is the neighbour's information rather than
// the caller's — and it leaks once per guessed id.
func TestCrossTenantRefusalRevealsNothing(t *testing.T) {
	s := newServerWith(t, withTenants)

	other := request(t, s, http.MethodPost, "/api/v1/diagnoses", paymentsToken,
		diagnoseBody("search-redis"))
	absent := request(t, s, http.MethodPost, "/api/v1/diagnoses", paymentsToken,
		diagnoseBody("no-such-target-anywhere"))

	if other.Code != absent.Code {
		t.Errorf("status %d for another tenant's target, %d for one that does not exist",
			other.Code, absent.Code)
	}

	// The bodies may differ only by the id the caller supplied — echoing their
	// own input back tells them nothing. Everything else must be identical, or
	// the difference is the answer to "does this exist", which belongs to the
	// other tenant and not to this caller.
	normalise := func(w *httptest.ResponseRecorder, id string) string {
		return strings.ReplaceAll(w.Body.String(), id, "<id>")
	}
	if a, b := normalise(other, "search-redis"), normalise(absent, "no-such-target-anywhere"); a != b {
		t.Errorf("the two refusals differ beyond the id the caller sent:\n other:  %s\n absent: %s", a, b)
	}

	// The same for reading a run that belongs to someone else.
	made := request(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", searchToken,
		diagnoseBody("search-redis"))
	if made.Code != http.StatusOK {
		t.Fatalf("could not create the other tenant's run: %d\n%s", made.Code, made.Body.String())
	}
	var report struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(made.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}

	foreign := request(t, s, http.MethodGet, "/api/v1/diagnoses/"+report.RunID, paymentsToken, nil)
	unknown := request(t, s, http.MethodGet, "/api/v1/diagnoses/run-does-not-exist", paymentsToken, nil)
	if foreign.Code != http.StatusNotFound || foreign.Code != unknown.Code {
		t.Errorf("foreign run %d, unknown run %d; both must be 404", foreign.Code, unknown.Code)
	}
}

// TestTargetListingIsTenantScoped is FR-006.
func TestTargetListingIsTenantScoped(t *testing.T) {
	s := newServerWith(t, withTenants)

	for _, c := range []struct {
		token string
		want  []string
	}{
		{paymentsToken, []string{"payments-redis"}},
		{searchToken, []string{"search-redis"}},
		{platformToken, []string{"payments-redis", "search-redis"}},
	} {
		w := request(t, s, http.MethodGet, "/api/v1/targets", c.token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		var body struct {
			Targets []struct {
				ID string `json:"id"`
			} `json:"targets"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(body.Targets))
		for _, target := range body.Targets {
			got = append(got, target.ID)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("listing returned %v, want %v", got, c.want)
		}
	}
}

// TestRunAccessIsTenantScoped is FR-007.
func TestRunAccessIsTenantScoped(t *testing.T) {
	s := newServerWith(t, withTenants)

	for _, c := range []struct{ token, target string }{
		{paymentsToken, "payments-redis"},
		{searchToken, "search-redis"},
	} {
		if w := request(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", c.token,
			diagnoseBody(c.target)); w.Code != http.StatusOK {
			t.Fatalf("could not create a run for %s: %d\n%s", c.target, w.Code, w.Body.String())
		}
	}

	for _, c := range []struct {
		token string
		want  int
		only  string
	}{
		{paymentsToken, 1, "payments-redis"},
		{searchToken, 1, "search-redis"},
		{platformToken, 2, ""},
	} {
		w := request(t, s, http.MethodGet, "/api/v1/diagnoses", c.token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		var body struct {
			Runs []struct {
				Target string `json:"target"`
			} `json:"runs"`
			Count int `json:"count"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Runs) != c.want || body.Count != c.want {
			t.Errorf("%d run(s) listed, want %d", len(body.Runs), c.want)
		}
		if c.only == "" {
			continue
		}
		for _, run := range body.Runs {
			if run.Target != c.only {
				t.Errorf("a listing leaked a run for %s", run.Target)
			}
		}
	}
}

// TestRunRecordCarriesTheTenant is FR-008. Recorded when it happens rather than
// derived later: reading the target's tenant at query time answers which tenant
// owns it *now*, and audits ask about the past.
func TestRunRecordCarriesTheTenant(t *testing.T) {
	s := newServerWith(t, withTenants)

	w := request(t, s, http.MethodPost, "/api/v1/diagnoses?wait=true", paymentsToken,
		diagnoseBody("payments-redis"))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	var report struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}

	got := request(t, s, http.MethodGet, "/api/v1/diagnoses/"+report.RunID, paymentsToken, nil)
	var record struct {
		Tenant    string `json:"tenant"`
		Principal string `json:"principal"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.Tenant != "payments" {
		t.Errorf("the run records tenant %q, want payments", record.Tenant)
	}
	if record.Principal != "payments-oncall" {
		t.Errorf("the run records principal %q", record.Principal)
	}
}

// TestTenancyIsEnforcedInOnePlace is FR-010 and CON-002, checked structurally.
// A handler that compared tenants for itself would be a handler that gets
// copied without the comparison.
func TestTenancyIsEnforcedInOnePlace(t *testing.T) {
	src := readSource(t, "server.go")
	for _, banned := range []string{".Tenants[", "Principal.Tenants", ".Tenant =="} {
		if strings.Contains(src, banned) {
			t.Errorf("server.go compares tenants directly (%q); everything must go "+
				"through MayReach, or the next handler will forget", banned)
		}
	}
	if !strings.Contains(src, "MayReach") && !strings.Contains(src, "reachable") {
		t.Error("server.go never consults the tenancy check at all")
	}
}
