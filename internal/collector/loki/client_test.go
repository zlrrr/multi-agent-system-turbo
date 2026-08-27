package loki

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	lastQuery  map[string][]string
	lastHeader http.Header
}

func newStub(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) *stub {
	t.Helper()
	s := &stub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath, s.lastQuery, s.lastHeader = r.URL.Path, r.URL.Query(), r.Header.Clone()
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func clientFor(t *testing.T, s *stub, mutate func(*config.LogsSource)) *Client {
	t.Helper()
	cfg := config.LogsSource{Name: "primary", Type: "loki", URL: s.URL,
		Timeout: config.Duration(2 * time.Second), MaxLines: 100}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg, s.Client())
}

func streamBody(lines ...string) string {
	vals := make([]string, 0, len(lines))
	base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	for i, l := range lines {
		ts := strconv.FormatInt(base.Add(time.Duration(i)*time.Second).UnixNano(), 10)
		vals = append(vals, fmt.Sprintf(`[%q,%q]`, ts, l))
	}
	return fmt.Sprintf(`{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"job":"redis","pod":"redis-0"},"values":[%s]}]}}`, strings.Join(vals, ","))
}

func testWindow() core.Window {
	now := time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
	return core.Window{From: now.Add(-time.Hour), To: now}
}

func TestQuery(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(streamBody("first line", "OOM command not allowed", "third line")))
	})
	c := clientFor(t, s, nil)

	res, err := c.Query(context.Background(), `{job="redis"}`, testWindow(), 0, Backward)
	if err != nil {
		t.Fatal(err)
	}
	if s.lastPath != "/loki/api/v1/query_range" {
		t.Fatalf("path = %s", s.lastPath)
	}
	if got := s.lastQuery["query"][0]; got != `{job="redis"}` {
		t.Fatalf("query = %s", got)
	}
	if len(res.Lines) != 3 || res.Streams != 1 {
		t.Fatalf("result = %+v", res)
	}
	// Newest first: an incident is read from the present backwards.
	if res.Lines[0].Text != "third line" {
		t.Fatalf("lines are not newest-first: %+v", res.Lines)
	}
	if res.Lines[0].Labels["pod"] != "redis-0" {
		t.Fatal("stream labels were dropped")
	}
	if !strings.Contains(res.Summary(), "3 lines") || !strings.Contains(res.Summary(), "third line") {
		t.Fatalf("summary = %s", res.Summary())
	}
}

func TestLimitIsEnforcedAndCapped(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		lines := make([]string, 30)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i)
		}
		_, _ = w.Write([]byte(streamBody(lines...)))
	})
	c := clientFor(t, s, func(l *config.LogsSource) { l.MaxLines = 10 })

	res, err := c.Query(context.Background(), `{job="redis"}`, testWindow(), 5, Backward)
	if err != nil {
		t.Fatal(err)
	}
	if s.lastQuery["limit"][0] != "5" {
		t.Fatalf("limit not passed through: %v", s.lastQuery["limit"])
	}
	if len(res.Lines) != 5 || !res.Truncated {
		t.Fatalf("limit not enforced locally: %d lines, truncated=%v", len(res.Lines), res.Truncated)
	}

	// A caller asking for more than the source ceiling is clamped, not refused.
	if _, err := c.Query(context.Background(), `{job="redis"}`, testWindow(), 9999, Backward); err != nil {
		t.Fatal(err)
	}
	if s.lastQuery["limit"][0] != "10" {
		t.Fatalf("limit not clamped to max_lines: %v", s.lastQuery["limit"])
	}
}

func TestWindowIsSentAsNanoseconds(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(streamBody("x")))
	})
	c := clientFor(t, s, nil)
	win := testWindow()
	if _, err := c.Query(context.Background(), `{job="x"}`, win, 0, Backward); err != nil {
		t.Fatal(err)
	}
	if s.lastQuery["start"][0] != strconv.FormatInt(win.From.UnixNano(), 10) {
		t.Fatalf("start = %v", s.lastQuery["start"])
	}
	if s.lastQuery["direction"][0] != "backward" {
		t.Fatalf("direction = %v", s.lastQuery["direction"])
	}
}

func TestInvalidWindowRefused(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {})
	c := clientFor(t, s, nil)
	now := time.Now()
	_, err := c.Query(context.Background(), `{}`, core.Window{From: now, To: now.Add(-time.Minute)}, 0, Backward)
	if errs.CodeOf(err) != "MAS-1010" {
		t.Fatalf("got %v, want MAS-1010", err)
	}
}

func TestLabels(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":["pod","namespace","job"]}`))
	})
	c := clientFor(t, s, nil)
	got, err := c.Labels(context.Background(), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "job" {
		t.Fatalf("labels should be sorted: %v", got)
	}
	if s.lastPath != "/loki/api/v1/labels" {
		t.Fatalf("path = %s", s.lastPath)
	}
}

func TestLabelValues(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":["redis-1","redis-0"]}`))
	})
	c := clientFor(t, s, nil)
	got, err := c.LabelValues(context.Background(), "pod", testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "redis-0" {
		t.Fatalf("values should be sorted: %v", got)
	}
	if s.lastPath != "/loki/api/v1/label/pod/values" {
		t.Fatalf("path = %s", s.lastPath)
	}
}

func TestLabelNameIsValidated(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {})
	c := clientFor(t, s, nil)
	for _, bad := range []string{"pod/../../etc", "pod name", "1pod", ""} {
		if _, err := c.LabelValues(context.Background(), bad, testWindow()); errs.CodeOf(err) != "MAS-4104" {
			t.Errorf("label %q: got %v, want MAS-4104", bad, err)
		}
	}
}

func TestTenantHeader(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(streamBody("x")))
	})
	c := clientFor(t, s, func(l *config.LogsSource) { l.TenantID = "team-a" })
	if _, err := c.Query(context.Background(), `{}`, testWindow(), 0, Backward); err != nil {
		t.Fatal(err)
	}
	if s.lastHeader.Get("X-Scope-OrgID") != "team-a" {
		t.Fatalf("tenant header = %q", s.lastHeader.Get("X-Scope-OrgID"))
	}
}

func TestAuthHeaders(t *testing.T) {
	t.Setenv("TEST_LOKI_TOKEN", "loki-token-abcdef")
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(streamBody("x")))
	})
	c := clientFor(t, s, func(l *config.LogsSource) {
		l.Auth = config.AuthConfig{Type: "bearer", Token: "${env:TEST_LOKI_TOKEN}"}
	})
	if _, err := c.Query(context.Background(), `{}`, testWindow(), 0, Backward); err != nil {
		t.Fatal(err)
	}
	if s.lastHeader.Get("Authorization") != "Bearer loki-token-abcdef" {
		t.Fatalf("Authorization = %q", s.lastHeader.Get("Authorization"))
	}
}

func TestErrorMapping(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"rejected query": {http.StatusBadRequest, "parse error at line 1", "MAS-4104"},
		"forbidden":      {http.StatusForbidden, "no", "MAS-4102"},
		"server error":   {http.StatusInternalServerError, "boom", "MAS-4102"},
		"not json":       {http.StatusOK, "<html/>", "MAS-4103"},
		"status error":   {http.StatusOK, `{"status":"error"}`, "MAS-4104"},
		"bad timestamp":  {http.StatusOK, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{},"values":[["not-a-number","x"]]}]}}`, "MAS-4103"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			c := clientFor(t, s, nil)
			_, err := c.Query(context.Background(), `{}`, testWindow(), 0, Backward)
			if errs.CodeOf(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, errs.CodeOf(err), tc.want)
			}
		})
	}
}

func TestUnreachableIsCoded(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {})
	c := clientFor(t, s, nil)
	s.Close()
	_, err := c.Query(context.Background(), `{}`, testWindow(), 0, Backward)
	if errs.CodeOf(err) != "MAS-4101" {
		t.Fatalf("got %v, want MAS-4101", err)
	}
}

func TestEmptyResult(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	})
	c := clientFor(t, s, nil)
	res, err := c.Query(context.Background(), `{job="ghost"}`, testWindow(), 0, Backward)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() || !strings.Contains(res.Summary(), "no matching") {
		t.Fatalf("empty result not reported clearly: %s", res.Summary())
	}
}

func TestHealth(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":["job"]}`))
	})
	if err := clientFor(t, s, nil).Health(context.Background()); err != nil {
		t.Fatalf("health probe failed: %v", err)
	}
}
