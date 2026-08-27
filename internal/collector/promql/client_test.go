package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

type stub struct {
	*httptest.Server
	lastPath   string
	lastForm   map[string][]string
	lastHeader http.Header
}

// newStub stands in for Prometheus/VictoriaMetrics so no test needs a network
// (NFR-006).
func newStub(t *testing.T, handler func(s *stub, w http.ResponseWriter, r *http.Request)) *stub {
	t.Helper()
	s := &stub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.lastPath, s.lastForm, s.lastHeader = r.URL.Path, r.Form, r.Header.Clone()
		handler(s, w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func vectorBody(pairs ...[2]string) string {
	results := make([]string, 0, len(pairs))
	for _, p := range pairs {
		results = append(results, fmt.Sprintf(
			`{"metric":{"__name__":"m","instance":%q},"value":[1724400000,%q]}`, p[0], p[1]))
	}
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(results, ",") + `]}}`
}

func clientFor(t *testing.T, s *stub, mutate func(*config.MetricsSource)) *Client {
	t.Helper()
	cfg := config.MetricsSource{Name: "primary", Type: "prometheus", URL: s.URL,
		Timeout: config.Duration(2 * time.Second), MaxSamples: 100}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg, s.Client())
}

func TestInstant(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorBody([2]string{"redis-0", "1048576"})))
	})
	c := clientFor(t, s, nil)

	res, err := c.Instant(context.Background(), "redis_memory_used_bytes", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if s.lastPath != "/api/v1/query" {
		t.Fatalf("path = %s", s.lastPath)
	}
	if got := s.lastForm["query"][0]; got != "redis_memory_used_bytes" {
		t.Fatalf("query = %s", got)
	}
	if len(res.Series) != 1 || res.Series[0].Last != 1048576 {
		t.Fatalf("series = %+v", res.Series)
	}
	if !strings.Contains(res.Summary(), "1048576") {
		t.Fatalf("summary = %s", res.Summary())
	}
}

func TestInstantAtTimestamp(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorBody([2]string{"a", "1"})))
	})
	c := clientFor(t, s, nil)
	at := time.Unix(1724400000, 0).UTC()
	if _, err := c.Instant(context.Background(), "up", at); err != nil {
		t.Fatal(err)
	}
	if s.lastForm["time"][0] != "1724400000" {
		t.Fatalf("time = %v", s.lastForm["time"])
	}
}

func TestRange(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"instance":"redis-0"},"values":[[1724400000,"10"],[1724400060,"30"],[1724400120,"20"]]}]}}`))
	})
	c := clientFor(t, s, nil)
	now := time.Unix(1724400120, 0).UTC()
	w := core.Window{From: now.Add(-time.Hour), To: now}

	res, err := c.Range(context.Background(), "rate(x[5m])", w, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if s.lastPath != "/api/v1/query_range" {
		t.Fatalf("path = %s", s.lastPath)
	}
	got := res.Series[0]
	if got.Count != 3 || got.Last != 20 || got.Min != 10 || got.Max != 30 || got.Avg != 20 {
		t.Fatalf("statistics wrong: %+v", got)
	}
	if !strings.Contains(res.Summary(), "min=10") || !strings.Contains(res.Summary(), "max=30") {
		t.Fatalf("summary = %s", res.Summary())
	}
}

func TestRangeRejectsInvalidWindow(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {})
	c := clientFor(t, s, nil)
	now := time.Now()
	_, err := c.Range(context.Background(), "x", core.Window{From: now, To: now.Add(-time.Hour)}, time.Minute)
	if errs.CodeOf(err) != "MAS-1010" {
		t.Fatalf("got %v, want MAS-1010", err)
	}
}

func TestAutoStepKeepsResultsBounded(t *testing.T) {
	now := time.Now()
	for _, span := range []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour, 30 * 24 * time.Hour} {
		w := core.Window{From: now.Add(-span), To: now}
		step := AutoStep(w, 500)
		if step <= 0 {
			t.Fatalf("span %s produced step %s", span, step)
		}
		if points := int(span / step); points > 500 {
			t.Fatalf("span %s step %s yields %d points, above the 500 ceiling", span, step, points)
		}
	}
}

func TestRangeNarrowsStepToRespectSampleCeiling(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	})
	c := clientFor(t, s, func(m *config.MetricsSource) { m.MaxSamples = 10 })
	now := time.Now().UTC()
	w := core.Window{From: now.Add(-24 * time.Hour), To: now}

	if _, err := c.Range(context.Background(), "x", w, time.Second); err != nil {
		t.Fatal(err)
	}
	stepSec := s.lastForm["step"][0]
	if stepSec == "1" {
		t.Fatal("a one-second step over 24h was passed through despite a 10-sample ceiling")
	}
}

func TestTruncation(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		pairs := make([][2]string, 0, 20)
		for i := 0; i < 20; i++ {
			pairs = append(pairs, [2]string{fmt.Sprintf("redis-%02d", i), "1"})
		}
		_, _ = w.Write([]byte(vectorBody(pairs...)))
	})
	c := clientFor(t, s, func(m *config.MetricsSource) { m.MaxSamples = 5 })

	res, err := c.Instant(context.Background(), "up", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("oversized result not flagged as truncated")
	}
	if len(res.Series) > 5 {
		t.Fatalf("returned %d series above the ceiling of 5", len(res.Series))
	}
}

func TestAuthHeaders(t *testing.T) {
	t.Setenv("TEST_PROM_TOKEN", "tok-abcdef123456")
	cases := map[string]struct {
		auth  config.AuthConfig
		check func(t *testing.T, h http.Header)
	}{
		"bearer": {
			auth: config.AuthConfig{Type: "bearer", Token: "${env:TEST_PROM_TOKEN}"},
			check: func(t *testing.T, h http.Header) {
				if h.Get("Authorization") != "Bearer tok-abcdef123456" {
					t.Fatalf("Authorization = %q", h.Get("Authorization"))
				}
			},
		},
		"basic": {
			auth: config.AuthConfig{Type: "basic", Username: "u", Password: "p-secret"},
			check: func(t *testing.T, h http.Header) {
				if !strings.HasPrefix(h.Get("Authorization"), "Basic ") {
					t.Fatalf("Authorization = %q", h.Get("Authorization"))
				}
			},
		},
		"custom header": {
			auth: config.AuthConfig{Type: "header", Header: "X-Scope-OrgID", Token: "tenant-a"},
			check: func(t *testing.T, h http.Header) {
				if h.Get("X-Scope-OrgID") != "tenant-a" {
					t.Fatalf("X-Scope-OrgID = %q", h.Get("X-Scope-OrgID"))
				}
			},
		},
		"none": {
			auth: config.AuthConfig{},
			check: func(t *testing.T, h http.Header) {
				if h.Get("Authorization") != "" {
					t.Fatalf("unexpected Authorization: %q", h.Get("Authorization"))
				}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(vectorBody([2]string{"a", "1"})))
			})
			c := clientFor(t, s, func(m *config.MetricsSource) { m.Auth = tc.auth })
			if _, err := c.Instant(context.Background(), "up", time.Time{}); err != nil {
				t.Fatal(err)
			}
			tc.check(t, s.lastHeader)
		})
	}
}

func TestUnresolvableSecretIsCoded(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {})
	c := clientFor(t, s, func(m *config.MetricsSource) {
		m.Auth = config.AuthConfig{Type: "bearer", Token: "${env:MAS_NOT_SET_ANYWHERE}"}
	})
	_, err := c.Instant(context.Background(), "up", time.Time{})
	if errs.CodeOf(err) != "MAS-1006" {
		t.Fatalf("got %v, want MAS-1006", err)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"rejected query":  {http.StatusBadRequest, `{"status":"error","error":"parse error"}`, "MAS-4004"},
		"unprocessable":   {http.StatusUnprocessableEntity, `{"status":"error","error":"execution"}`, "MAS-4004"},
		"unauthorised":    {http.StatusUnauthorized, `no`, "MAS-4002"},
		"server error":    {http.StatusInternalServerError, `boom`, "MAS-4002"},
		"not json":        {http.StatusOK, `<html>not prometheus</html>`, "MAS-4003"},
		"status not ok":   {http.StatusOK, `{"status":"error","error":"bad"}`, "MAS-4004"},
		"bad sample type": {http.StatusOK, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,2]}]}}`, "MAS-4003"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			c := clientFor(t, s, nil)
			_, err := c.Instant(context.Background(), "up", time.Time{})
			if errs.CodeOf(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, errs.CodeOf(err), tc.want)
			}
		})
	}
}

func TestUnreachableIsCoded(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {})
	c := clientFor(t, s, nil)
	s.Close()
	_, err := c.Instant(context.Background(), "up", time.Time{})
	if errs.CodeOf(err) != "MAS-4001" {
		t.Fatalf("got %v, want MAS-4001", err)
	}
}

func TestSeries(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":[{"__name__":"redis_up","instance":"redis-0"},{"__name__":"redis_up","instance":"redis-1"}]}`))
	})
	c := clientFor(t, s, nil)
	now := time.Now().UTC()
	sets, err := c.Series(context.Background(), []string{"redis_up"},
		core.Window{From: now.Add(-time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 || sets[0]["instance"] != "redis-0" {
		t.Fatalf("sets = %+v", sets)
	}
	if s.lastForm["match[]"][0] != "redis_up" {
		t.Fatalf("match = %v", s.lastForm["match[]"])
	}
}

func TestSpecialFloatValues(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorBody([2]string{"a", "NaN"}, [2]string{"b", "+Inf"})))
	})
	c := clientFor(t, s, nil)
	res, err := c.Instant(context.Background(), "up", time.Time{})
	if err != nil {
		t.Fatalf("special float values should parse, got %v", err)
	}
	if len(res.Series) != 2 {
		t.Fatalf("series = %+v", res.Series)
	}
	if !strings.Contains(res.Summary(), "NaN") && !strings.Contains(res.Summary(), "Inf") {
		t.Fatalf("summary lost the special values: %s", res.Summary())
	}
}

func TestEmptyResult(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	})
	c := clientFor(t, s, nil)
	res, err := c.Instant(context.Background(), "absent_metric", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() || !strings.Contains(res.Summary(), "no data") {
		t.Fatalf("empty result not reported clearly: %+v %s", res, res.Summary())
	}
}

func TestResultIsJSONSerialisable(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorBody([2]string{"a", "1"})))
	})
	c := clientFor(t, s, nil)
	res, _ := c.Instant(context.Background(), "up", time.Time{})
	if _, err := json.Marshal(res); err != nil {
		t.Fatalf("Result must serialise into a run record: %v", err)
	}
}

func TestHealth(t *testing.T) {
	s := newStub(t, func(_ *stub, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorBody([2]string{"", "1"})))
	})
	if err := clientFor(t, s, nil).Health(context.Background()); err != nil {
		t.Fatalf("health probe failed: %v", err)
	}
}
