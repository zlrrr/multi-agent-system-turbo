package kube

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

type apiStub struct {
	*httptest.Server
	requests []*http.Request
	routes   map[string]func(w http.ResponseWriter, r *http.Request)
}

func newAPIStub(t *testing.T) *apiStub {
	t.Helper()
	s := &apiStub{routes: map[string]func(http.ResponseWriter, *http.Request){}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests = append(s.requests, r.Clone(r.Context()))
		if h, ok := s.routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","message":"not found"}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *apiStub) route(path, body string) {
	s.routes[path] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

func (s *apiStub) status(path string, code int, body string) {
	s.routes[path] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

func clientFor(t *testing.T, s *apiStub) *Client {
	t.Helper()
	c, err := NewClient(config.EnvConfig{
		Type: "kubernetes", APIServer: s.URL, Namespace: "middleware",
		Token: "test-token-abcdef", Timeout: config.Duration(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	c.hc = s.Client()
	return c
}

const podListJSON = `{"items":[
 {"metadata":{"name":"redis-0","namespace":"middleware","labels":{"app":"redis","role":"master"}},
  "spec":{"nodeName":"node-a"},
  "status":{"phase":"Running","podIP":"10.1.0.5","startTime":"2026-08-23T09:00:00Z",
   "conditions":[{"type":"Ready","status":"True"}],
   "containerStatuses":[{"name":"redis","image":"redis:7.2.4","ready":true,"restartCount":0,
     "state":{"running":{}}}]}},
 {"metadata":{"name":"redis-1","namespace":"middleware","labels":{"app":"redis","role":"replica"}},
  "spec":{"nodeName":"node-b"},
  "status":{"phase":"Running","podIP":"10.1.0.6",
   "conditions":[{"type":"Ready","status":"False"}],
   "containerStatuses":[{"name":"redis","image":"redis:7.2.4","ready":false,"restartCount":7,
     "state":{"waiting":{"reason":"CrashLoopBackOff"}},
     "lastState":{"terminated":{"reason":"OOMKilled","exitCode":137}}}]}}
]}`

func TestListPods(t *testing.T) {
	s := newAPIStub(t)
	s.route("/api/v1/namespaces/middleware/pods", podListJSON)
	c := clientFor(t, s)

	pods, err := c.ListPods(context.Background(), "", "app=redis")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 2 {
		t.Fatalf("pods = %d", len(pods))
	}
	if pods[0].Name != "redis-0" || !pods[0].Ready() || pods[0].Restarts() != 0 {
		t.Fatalf("pod 0 wrong: %+v", pods[0])
	}
	p1 := pods[1]
	if p1.Ready() || p1.Restarts() != 7 {
		t.Fatalf("pod 1 wrong: %+v", p1)
	}
	if p1.Containers[0].Reason != "CrashLoopBackOff" || p1.Containers[0].LastReason != "OOMKilled" {
		t.Fatalf("container state lost: %+v", p1.Containers[0])
	}
	if p1.Containers[0].LastExitCode != 137 {
		t.Fatalf("exit code lost: %+v", p1.Containers[0])
	}
	if got := s.requests[0].URL.Query().Get("labelSelector"); got != "app=redis" {
		t.Fatalf("selector = %q", got)
	}
	if got := s.requests[0].Header.Get("Authorization"); got != "Bearer test-token-abcdef" {
		t.Fatalf("Authorization = %q", got)
	}
	if s.requests[0].Method != http.MethodGet {
		t.Fatalf("method = %s; this client must only ever issue GET", s.requests[0].Method)
	}
}

func TestPodLogs(t *testing.T) {
	s := newAPIStub(t)
	s.route("/api/v1/namespaces/middleware/pods/redis-0/log", "2026-08-23T09:00:00Z line one\n2026-08-23T09:00:01Z line two\n")
	c := clientFor(t, s)

	body, err := c.PodLogs(context.Background(), "", "redis-0", LogOptions{TailLines: 50, Previous: true, SinceSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "line two") {
		t.Fatalf("body = %q", body)
	}
	q := s.requests[0].URL.Query()
	for k, want := range map[string]string{"tailLines": "50", "previous": "true", "sinceSeconds": "600", "timestamps": "true"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestEvents(t *testing.T) {
	s := newAPIStub(t)
	s.route("/api/v1/namespaces/middleware/events", `{"items":[
	  {"metadata":{"namespace":"middleware"},"type":"Warning","reason":"OOMKilled","message":"container redis exceeded memory",
	   "count":3,"firstTimestamp":"2026-08-23T09:00:00Z","lastTimestamp":"2026-08-23T09:30:00Z",
	   "involvedObject":{"kind":"Pod","name":"redis-1"}},
	  {"metadata":{"namespace":"middleware"},"type":"Normal","reason":"Scheduled","message":"assigned",
	   "count":1,"firstTimestamp":"2026-08-23T08:00:00Z","lastTimestamp":"2026-08-23T08:00:00Z",
	   "involvedObject":{"kind":"Pod","name":"redis-0"}}]}`)
	c := clientFor(t, s)

	events, err := c.ListEvents(context.Background(), "", "involvedObject.name=redis-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d", len(events))
	}
	// Oldest first: an incident is read forwards through the event log.
	if events[0].Reason != "Scheduled" {
		t.Fatalf("events not sorted oldest-first: %+v", events)
	}
	if events[1].Object != "Pod/redis-1" || events[1].Count != 3 {
		t.Fatalf("event projection wrong: %+v", events[1])
	}
	if got := s.requests[0].URL.Query().Get("fieldSelector"); got != "involvedObject.name=redis-1" {
		t.Fatalf("fieldSelector = %q", got)
	}
}

func TestNodes(t *testing.T) {
	s := newAPIStub(t)
	s.route("/api/v1/nodes", `{"items":[
	  {"metadata":{"name":"node-b"},"spec":{"unschedulable":true},
	   "status":{"conditions":[{"type":"Ready","status":"True"},{"type":"MemoryPressure","status":"True"}],
	    "allocatable":{"cpu":"4","memory":"8Gi"},"capacity":{"cpu":"4","memory":"16Gi"},
	    "nodeInfo":{"kubeletVersion":"v1.30.2"}}},
	  {"metadata":{"name":"node-a"},"status":{"conditions":[{"type":"Ready","status":"True"}],
	    "nodeInfo":{"kubeletVersion":"v1.30.2"}}}]}`)
	c := clientFor(t, s)

	nodes, err := c.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Name != "node-a" {
		t.Fatalf("nodes not sorted: %+v", nodes)
	}
	b := nodes[1]
	if !b.Unschedule || b.Conditions["MemoryPressure"] != "True" || b.KubeletVer != "v1.30.2" {
		t.Fatalf("node projection wrong: %+v", b)
	}
}

func TestWorkloads(t *testing.T) {
	s := newAPIStub(t)
	s.route("/apis/apps/v1/namespaces/middleware/deployments", `{"items":[]}`)
	s.route("/apis/apps/v1/namespaces/middleware/statefulsets", `{"items":[
	  {"metadata":{"name":"redis","namespace":"middleware","labels":{"app":"redis"}},
	   "spec":{"replicas":3,"template":{"spec":{"containers":[{"image":"redis:7.2.4"}]}}},
	   "status":{"readyReplicas":2,"updatedReplicas":3,"availableReplicas":2}}]}`)
	c := clientFor(t, s)

	ws, err := c.ListWorkloads(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("workloads = %+v", ws)
	}
	w := ws[0]
	if w.Kind != "statefulset" || w.Replicas != 3 || w.Ready != 2 || w.Images[0] != "redis:7.2.4" {
		t.Fatalf("workload projection wrong: %+v", w)
	}
}

func TestWorkloadsToleratesPartialRBAC(t *testing.T) {
	s := newAPIStub(t)
	s.status("/apis/apps/v1/namespaces/middleware/deployments", http.StatusForbidden, `{"message":"forbidden"}`)
	s.route("/apis/apps/v1/namespaces/middleware/statefulsets", `{"items":[]}`)
	c := clientFor(t, s)

	if _, err := c.ListWorkloads(context.Background(), ""); err != nil {
		t.Fatalf("a forbidden deployment read should not fail the whole listing: %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := map[string]struct {
		code int
		body string
		want string
	}{
		"unauthorised": {http.StatusUnauthorized, `{"message":"Unauthorized"}`, "MAS-4202"},
		"forbidden":    {http.StatusForbidden, `{"message":"pods is forbidden"}`, "MAS-4201"},
		"not found":    {http.StatusNotFound, `{"message":"not found"}`, "MAS-4204"},
		"server error": {http.StatusInternalServerError, `{"message":"boom"}`, "MAS-4203"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newAPIStub(t)
			s.status("/api/v1/namespaces/middleware/pods", tc.code, tc.body)
			c := clientFor(t, s)
			_, err := c.ListPods(context.Background(), "", "")
			if errs.CodeOf(err) != tc.want {
				t.Fatalf("got %v (%s), want %s", err, errs.CodeOf(err), tc.want)
			}
		})
	}
}

func TestMalformedResponseIsCoded(t *testing.T) {
	s := newAPIStub(t)
	s.route("/api/v1/namespaces/middleware/pods", `<html>not kubernetes</html>`)
	c := clientFor(t, s)
	_, err := c.ListPods(context.Background(), "", "")
	if errs.CodeOf(err) != "MAS-4205" {
		t.Fatalf("got %v, want MAS-4205", err)
	}
}

func TestUnreachableIsCoded(t *testing.T) {
	s := newAPIStub(t)
	c := clientFor(t, s)
	s.Close()
	_, err := c.ListPods(context.Background(), "", "")
	if errs.CodeOf(err) != "MAS-4203" {
		t.Fatalf("got %v, want MAS-4203", err)
	}
}

func TestServerVersionAndProbe(t *testing.T) {
	s := newAPIStub(t)
	s.route("/version", `{"gitVersion":"v1.30.2"}`)
	c := clientFor(t, s)
	v, err := c.ServerVersion(context.Background())
	if err != nil || v != "v1.30.2" {
		t.Fatalf("version = %q, %v", v, err)
	}
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

// ── credential resolution ───────────────────────────────────────────────────

func writeKubeconfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAuthModes(t *testing.T) {
	t.Run("bearer token in kubeconfig", func(t *testing.T) {
		p := writeKubeconfig(t, `
apiVersion: v1
current-context: ctx
clusters: [{name: c, cluster: {server: "https://api.example:6443", insecure-skip-tls-verify: true}}]
contexts: [{name: ctx, context: {cluster: c, user: u, namespace: mw}}]
users: [{name: u, user: {token: kube-token-123456}}]
`)
		creds, err := loadKubeconfig(p, "")
		if err != nil {
			t.Fatal(err)
		}
		if creds.bearerToken != "kube-token-123456" || creds.namespace != "mw" || !creds.insecure {
			t.Fatalf("creds = %+v", creds)
		}
		if creds.server != "https://api.example:6443" {
			t.Fatalf("server = %s", creds.server)
		}
	})

	t.Run("client certificate", func(t *testing.T) {
		enc := base64.StdEncoding.EncodeToString
		p := writeKubeconfig(t, fmt.Sprintf(`
apiVersion: v1
current-context: ctx
clusters: [{name: c, cluster: {server: "https://api:6443", certificate-authority-data: %s}}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
users: [{name: u, user: {client-certificate-data: %s, client-key-data: %s}}]
`, enc([]byte("CA-PEM")), enc([]byte("CERT-PEM")), enc([]byte("KEY-PEM"))))
		creds, err := loadKubeconfig(p, "")
		if err != nil {
			t.Fatal(err)
		}
		if string(creds.caPEM) != "CA-PEM" || string(creds.clientCertPEM) != "CERT-PEM" || string(creds.clientKeyPEM) != "KEY-PEM" {
			t.Fatalf("PEM material not decoded: %+v", creds)
		}
	})

	t.Run("token file", func(t *testing.T) {
		dir := t.TempDir()
		tf := filepath.Join(dir, "token")
		if err := os.WriteFile(tf, []byte("file-token-abcdef\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		p := writeKubeconfig(t, fmt.Sprintf(`
apiVersion: v1
current-context: ctx
clusters: [{name: c, cluster: {server: "https://api:6443"}}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
users: [{name: u, user: {tokenFile: %s}}]
`, tf))
		creds, err := loadKubeconfig(p, "")
		if err != nil {
			t.Fatal(err)
		}
		if creds.bearerToken != "file-token-abcdef" {
			t.Fatalf("token = %q", creds.bearerToken)
		}
	})

	t.Run("named context selection", func(t *testing.T) {
		p := writeKubeconfig(t, `
apiVersion: v1
current-context: dev
clusters:
  - {name: c1, cluster: {server: "https://dev:6443"}}
  - {name: c2, cluster: {server: "https://prod:6443"}}
contexts:
  - {name: dev, context: {cluster: c1, user: u}}
  - {name: prod, context: {cluster: c2, user: u}}
users: [{name: u, user: {token: t-abcdef123}}]
`)
		creds, err := loadKubeconfig(p, "prod")
		if err != nil {
			t.Fatal(err)
		}
		if creds.server != "https://prod:6443" {
			t.Fatalf("named context ignored: %s", creds.server)
		}
	})
}

// TestExecPluginIsRefusedWithGuidance records the deliberate decision in
// kubeconfig.go: running an arbitrary binary named by a config file is exactly
// what deny-by-default exists to prevent.
func TestExecPluginIsRefusedWithGuidance(t *testing.T) {
	p := writeKubeconfig(t, `
apiVersion: v1
current-context: ctx
clusters: [{name: c, cluster: {server: "https://api:6443"}}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
users: [{name: u, user: {exec: {command: aws}}}]
`)
	_, err := loadKubeconfig(p, "")
	if errs.CodeOf(err) != "MAS-4202" {
		t.Fatalf("got %v, want MAS-4202", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("the refusal must tell the operator what to do instead: %v", err)
	}
}

func TestKubeconfigErrors(t *testing.T) {
	cases := map[string]string{
		"no current context": `apiVersion: v1
clusters: [{name: c, cluster: {server: "https://api:6443"}}]`,
		"missing context": `apiVersion: v1
current-context: ghost
clusters: [{name: c, cluster: {server: "https://api:6443"}}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
users: [{name: u, user: {token: abcdef123}}]`,
		"no credential": `apiVersion: v1
current-context: ctx
clusters: [{name: c, cluster: {server: "https://api:6443"}}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
users: [{name: u, user: {}}]`,
		"no server": `apiVersion: v1
current-context: ctx
clusters: [{name: c, cluster: {}}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
users: [{name: u, user: {token: abcdef123}}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadKubeconfig(writeKubeconfig(t, body), ""); errs.CodeOf(err) != "MAS-4202" {
				t.Fatalf("got %v, want MAS-4202", err)
			}
		})
	}
	if _, err := loadKubeconfig("/no/such/kubeconfig", ""); errs.CodeOf(err) != "MAS-4202" {
		t.Fatalf("missing file: got %v, want MAS-4202", err)
	}
}

func TestNoCredentialsAnywhereIsCoded(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("HOME", t.TempDir())
	_, err := NewClient(config.EnvConfig{Type: "kubernetes"})
	if errs.CodeOf(err) != "MAS-4202" {
		t.Fatalf("got %v, want MAS-4202", err)
	}
}

// TestClientHasNoMutatingMethods is the structural proof behind design-lld.md
// §2.9: the type simply cannot express a write.
func TestClientHasNoMutatingMethods(t *testing.T) {
	forbidden := []string{
		"Create", "Update", "Patch", "Delete", "Apply", "Replace", "Post", "Put",
		"Exec", "Attach", "PortForward", "Evict", "Scale", "Restart", "Drain", "Cordon",
	}
	typ := reflect.TypeOf(&Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("Client exposes %q; this client must be structurally incapable of mutation", name)
			}
		}
	}
}
