package kube

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Client reads a Kubernetes API server. Every exported method issues a GET; no
// method can issue any other verb, and TestClientHasNoMutatingMethods asserts it.
type Client struct {
	creds   credentials
	hc      *http.Client
	timeout time.Duration
}

// NewClient resolves credentials in the documented order — explicit
// configuration, then kubeconfig, then in-cluster — and builds a TLS client.
func NewClient(cfg config.EnvConfig) (*Client, error) {
	timeout := cfg.Timeout.D()
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	creds, err := resolveCredentials(cfg)
	if err != nil {
		return nil, err
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12} //nolint:gosec // InsecureSkipVerify is operator-controlled below
	if creds.insecure || cfg.TLSSkip {
		tlsCfg.InsecureSkipVerify = true
	}
	if len(creds.caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(creds.caPEM) {
			return nil, errs.New("MAS-4202", "the configured certificate authority is not valid PEM")
		}
		tlsCfg.RootCAs = pool
	}
	if len(creds.clientCertPEM) > 0 && len(creds.clientKeyPEM) > 0 {
		cert, err := tls.X509KeyPair(creds.clientCertPEM, creds.clientKeyPEM)
		if err != nil {
			return nil, errs.Wrap(err, "MAS-4202", "client certificate and key do not form a valid pair")
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return &Client{
		creds:   creds,
		timeout: timeout,
		hc: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg, MaxIdleConnsPerHost: 4},
		},
	}, nil
}

func resolveCredentials(cfg config.EnvConfig) (credentials, error) {
	if cfg.APIServer != "" {
		c := credentials{
			server: strings.TrimRight(cfg.APIServer, "/"), namespace: cfg.Namespace,
			insecure: cfg.TLSSkip, source: "explicit api_server configuration",
		}
		if !cfg.Token.IsZero() {
			tok, err := cfg.Token.Reveal()
			if err != nil {
				return credentials{}, err
			}
			c.bearerToken = tok
		}
		if cfg.CAFile != "" {
			pem, err := readFile(cfg.CAFile)
			if err != nil {
				return credentials{}, errs.Wrap(err, "MAS-4202", "ca_file unreadable")
			}
			c.caPEM = pem
		}
		return c, nil
	}

	if cfg.Kubeconfig != "" {
		c, err := loadKubeconfig(cfg.Kubeconfig, cfg.Context)
		if err != nil {
			return credentials{}, err
		}
		if cfg.Namespace != "" {
			c.namespace = cfg.Namespace
		}
		return c, nil
	}

	if c, err := loadInCluster(); err == nil {
		if cfg.Namespace != "" {
			c.namespace = cfg.Namespace
		}
		return c, nil
	}

	if p := DefaultKubeconfigPath(); p != "" {
		if c, err := loadKubeconfig(p, cfg.Context); err == nil {
			if cfg.Namespace != "" {
				c.namespace = cfg.Namespace
			}
			return c, nil
		}
	}
	return credentials{}, errs.New("MAS-4202",
		"no in-cluster service account, no envs.<name>.kubeconfig and no usable ~/.kube/config")
}

// Server reports the API server this client talks to.
func (c *Client) Server() string { return c.creds.server }

// Namespace reports the default namespace from the resolved credentials.
func (c *Client) Namespace() string { return c.creds.namespace }

// CredentialSource describes how credentials were obtained, for `mas doctor`.
func (c *Client) CredentialSource() string { return c.creds.source }

// Timeout reports the per-request timeout.
func (c *Client) Timeout() time.Duration { return c.timeout }

// URLFor builds an absolute URL for an API path, which the tool layer hands to
// the guard before any request is made.
func (c *Client) URLFor(path string) string { return c.creds.server + path }

// ── resource shapes ─────────────────────────────────────────────────────────
// Only the fields a diagnosis actually reads are modelled. Keeping the shapes
// narrow keeps the audit surface small and the evidence payloads readable.

// ContainerStatus summarises one container's health.
type ContainerStatus struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restart_count"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	LastReason   string `json:"last_reason,omitempty"`
	LastExitCode int    `json:"last_exit_code,omitempty"`
}

// Pod is the diagnostic projection of a pod.
type Pod struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Node       string            `json:"node,omitempty"`
	Phase      string            `json:"phase"`
	PodIP      string            `json:"pod_ip,omitempty"`
	StartedAt  time.Time         `json:"started_at,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Containers []ContainerStatus `json:"containers,omitempty"`
	Conditions map[string]string `json:"conditions,omitempty"`
}

// Restarts totals container restarts, the single most diagnostic pod number.
func (p Pod) Restarts() int {
	n := 0
	for _, c := range p.Containers {
		n += c.RestartCount
	}
	return n
}

// Ready reports whether every container is ready.
func (p Pod) Ready() bool {
	if len(p.Containers) == 0 {
		return false
	}
	for _, c := range p.Containers {
		if !c.Ready {
			return false
		}
	}
	return true
}

// Event is the diagnostic projection of a Kubernetes event.
type Event struct {
	Namespace string    `json:"namespace"`
	Type      string    `json:"type"`
	Reason    string    `json:"reason"`
	Object    string    `json:"object"`
	Message   string    `json:"message"`
	Count     int       `json:"count"`
	FirstAt   time.Time `json:"first_at,omitempty"`
	LastAt    time.Time `json:"last_at,omitempty"`
}

// Node is the diagnostic projection of a node.
type Node struct {
	Name        string            `json:"name"`
	Ready       string            `json:"ready"`
	Conditions  map[string]string `json:"conditions,omitempty"`
	Allocatable map[string]string `json:"allocatable,omitempty"`
	Capacity    map[string]string `json:"capacity,omitempty"`
	KubeletVer  string            `json:"kubelet_version,omitempty"`
	Unschedule  bool              `json:"unschedulable,omitempty"`
}

// Workload is the diagnostic projection of a Deployment or StatefulSet.
type Workload struct {
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Replicas  int               `json:"replicas"`
	Ready     int               `json:"ready"`
	Updated   int               `json:"updated"`
	Available int               `json:"available"`
	Images    []string          `json:"images,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// LogOptions bounds a pod-log read.
type LogOptions struct {
	Container    string
	TailLines    int
	SinceSeconds int
	Previous     bool
	LimitBytes   int
}

// ListPods lists pods in a namespace, optionally filtered by a label selector.
func (c *Client) ListPods(ctx context.Context, namespace, selector string) ([]Pod, error) {
	q := url.Values{}
	if selector != "" {
		q.Set("labelSelector", selector)
	}
	body, err := c.get(ctx, c.nsPath(namespace, "pods"), q)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, errs.Wrap(err, "MAS-4205", "pod list is not JSON")
	}
	out := make([]Pod, 0, len(list.Items))
	for _, raw := range list.Items {
		p, err := parsePod(raw)
		if err != nil {
			return nil, errs.Wrap(err, "MAS-4205", err.Error())
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// PodLogs reads a pod's logs, bounded by the supplied options.
func (c *Client) PodLogs(ctx context.Context, namespace, pod string, opts LogOptions) (string, error) {
	q := url.Values{}
	if opts.Container != "" {
		q.Set("container", opts.Container)
	}
	if opts.TailLines > 0 {
		q.Set("tailLines", strconv.Itoa(opts.TailLines))
	}
	if opts.SinceSeconds > 0 {
		q.Set("sinceSeconds", strconv.Itoa(opts.SinceSeconds))
	}
	if opts.Previous {
		q.Set("previous", "true")
	}
	if opts.LimitBytes > 0 {
		q.Set("limitBytes", strconv.Itoa(opts.LimitBytes))
	}
	q.Set("timestamps", "true")
	body, err := c.get(ctx, c.nsPath(namespace, "pods")+"/"+url.PathEscape(pod)+"/log", q)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ListEvents lists events in a namespace, newest last.
func (c *Client) ListEvents(ctx context.Context, namespace, fieldSelector string) ([]Event, error) {
	q := url.Values{}
	if fieldSelector != "" {
		q.Set("fieldSelector", fieldSelector)
	}
	body, err := c.get(ctx, c.nsPath(namespace, "events"), q)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Type           string `json:"type"`
			Reason         string `json:"reason"`
			Message        string `json:"message"`
			Count          int    `json:"count"`
			FirstTimestamp string `json:"firstTimestamp"`
			LastTimestamp  string `json:"lastTimestamp"`
			EventTime      string `json:"eventTime"`
			InvolvedObject struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"involvedObject"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, errs.Wrap(err, "MAS-4205", "event list is not JSON")
	}
	out := make([]Event, 0, len(list.Items))
	for _, e := range list.Items {
		ev := Event{
			Namespace: e.Metadata.Namespace, Type: e.Type, Reason: e.Reason,
			Message: e.Message, Count: e.Count,
			Object: e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name,
		}
		ev.FirstAt = parseTime(e.FirstTimestamp)
		ev.LastAt = parseTime(firstNonEmpty(e.LastTimestamp, e.EventTime))
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastAt.Before(out[j].LastAt) })
	return out, nil
}

// ListNodes lists cluster nodes.
func (c *Client) ListNodes(ctx context.Context) ([]Node, error) {
	body, err := c.get(ctx, "/api/v1/nodes", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Unschedulable bool `json:"unschedulable"`
			} `json:"spec"`
			Status struct {
				Conditions []struct {
					Type    string `json:"type"`
					Status  string `json:"status"`
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"conditions"`
				Allocatable map[string]string `json:"allocatable"`
				Capacity    map[string]string `json:"capacity"`
				NodeInfo    struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, errs.Wrap(err, "MAS-4205", "node list is not JSON")
	}
	out := make([]Node, 0, len(list.Items))
	for _, n := range list.Items {
		node := Node{
			Name: n.Metadata.Name, Unschedule: n.Spec.Unschedulable,
			Allocatable: n.Status.Allocatable, Capacity: n.Status.Capacity,
			KubeletVer: n.Status.NodeInfo.KubeletVersion,
			Conditions: map[string]string{},
		}
		for _, cond := range n.Status.Conditions {
			node.Conditions[cond.Type] = cond.Status
			if cond.Type == "Ready" {
				node.Ready = cond.Status
			}
		}
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListWorkloads lists deployments and statefulsets in a namespace.
func (c *Client) ListWorkloads(ctx context.Context, namespace string) ([]Workload, error) {
	var out []Workload
	for _, kind := range []string{"deployments", "statefulsets"} {
		body, err := c.get(ctx, "/apis/apps/v1/namespaces/"+url.PathEscape(c.ns(namespace))+"/"+kind, nil)
		if err != nil {
			if errs.Is(err, "MAS-4201") || errs.Is(err, "MAS-4204") {
				continue // partial visibility is normal; the caller records a gap
			}
			return nil, err
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name      string            `json:"name"`
					Namespace string            `json:"namespace"`
					Labels    map[string]string `json:"labels"`
				} `json:"metadata"`
				Spec struct {
					Replicas int `json:"replicas"`
					Template struct {
						Spec struct {
							Containers []struct {
								Image string `json:"image"`
							} `json:"containers"`
						} `json:"spec"`
					} `json:"template"`
				} `json:"spec"`
				Status struct {
					ReadyReplicas     int `json:"readyReplicas"`
					UpdatedReplicas   int `json:"updatedReplicas"`
					AvailableReplicas int `json:"availableReplicas"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, errs.Wrap(err, "MAS-4205", kind+" list is not JSON")
		}
		singular := strings.TrimSuffix(kind, "s")
		for _, w := range list.Items {
			item := Workload{
				Kind: singular, Name: w.Metadata.Name, Namespace: w.Metadata.Namespace,
				Replicas: w.Spec.Replicas, Ready: w.Status.ReadyReplicas,
				Updated: w.Status.UpdatedReplicas, Available: w.Status.AvailableReplicas,
				Labels: w.Metadata.Labels,
			}
			for _, ct := range w.Spec.Template.Spec.Containers {
				item.Images = append(item.Images, ct.Image)
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ServerVersion reads the API server version, the cheapest connectivity probe.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	body, err := c.get(ctx, "/version", nil)
	if err != nil {
		return "", err
	}
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", errs.Wrap(err, "MAS-4205", "version response is not JSON")
	}
	return v.GitVersion, nil
}

// Probe verifies connectivity and credentials.
func (c *Client) Probe(ctx context.Context) error {
	_, err := c.ServerVersion(ctx)
	return err
}

func (c *Client) ns(namespace string) string {
	if namespace != "" {
		return namespace
	}
	if c.creds.namespace != "" {
		return c.creds.namespace
	}
	return "default"
}

func (c *Client) nsPath(namespace, resource string) string {
	return "/api/v1/namespaces/" + url.PathEscape(c.ns(namespace)) + "/" + resource
}

// PodsPath exposes the URL a pod listing will use, so the tool layer can declare
// the effect to the guard before issuing it.
func (c *Client) PodsPath(namespace string) string { return c.nsPath(namespace, "pods") }

// EventsPath exposes the events URL for guard planning.
func (c *Client) EventsPath(namespace string) string { return c.nsPath(namespace, "events") }

// PodLogPath exposes the pod-log URL for guard planning.
func (c *Client) PodLogPath(namespace, pod string) string {
	return c.nsPath(namespace, "pods") + "/" + url.PathEscape(pod) + "/log"
}

// WorkloadsPath exposes the workloads URL for guard planning.
func (c *Client) WorkloadsPath(namespace string) string {
	return "/apis/apps/v1/namespaces/" + url.PathEscape(c.ns(namespace)) + "/deployments"
}

// get is the only request primitive in this package, and it is GET-only.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := c.creds.server + path
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4203", err.Error())
	}
	req.Header.Set("Accept", "application/json, text/plain")
	if c.creds.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.creds.bearerToken)
	} else if c.creds.username != "" {
		req.SetBasicAuth(c.creds.username, c.creds.password)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4203", err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4205", err.Error())
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized:
		return nil, errs.New("MAS-4202", "the API server rejected the credentials (401)")
	case http.StatusForbidden:
		return nil, errs.New("MAS-4201", path, kubeMessage(body))
	case http.StatusNotFound:
		return nil, errs.New("MAS-4204", path)
	default:
		return nil, errs.New("MAS-4203", fmt.Sprintf("HTTP %d on %s: %s", resp.StatusCode, path, kubeMessage(body)))
	}
}

func kubeMessage(body []byte) string {
	var s struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &s); err == nil && s.Message != "" {
		return s.Message
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:197] + "…"
	}
	return msg
}

func parsePod(raw json.RawMessage) (Pod, error) {
	var p struct {
		Metadata struct {
			Name      string            `json:"name"`
			Namespace string            `json:"namespace"`
			Labels    map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
		Status struct {
			Phase      string `json:"phase"`
			PodIP      string `json:"podIP"`
			StartTime  string `json:"startTime"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			ContainerStatuses []struct {
				Name         string `json:"name"`
				Image        string `json:"image"`
				Ready        bool   `json:"ready"`
				RestartCount int    `json:"restartCount"`
				State        map[string]struct {
					Reason string `json:"reason"`
				} `json:"state"`
				LastState map[string]struct {
					Reason   string `json:"reason"`
					ExitCode int    `json:"exitCode"`
				} `json:"lastState"`
			} `json:"containerStatuses"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Pod{}, fmt.Errorf("pod object is not JSON: %w", err)
	}
	out := Pod{
		Name: p.Metadata.Name, Namespace: p.Metadata.Namespace, Labels: p.Metadata.Labels,
		Node: p.Spec.NodeName, Phase: p.Status.Phase, PodIP: p.Status.PodIP,
		StartedAt: parseTime(p.Status.StartTime), Conditions: map[string]string{},
	}
	for _, cond := range p.Status.Conditions {
		out.Conditions[cond.Type] = cond.Status
	}
	for _, cs := range p.Status.ContainerStatuses {
		item := ContainerStatus{
			Name: cs.Name, Image: cs.Image, Ready: cs.Ready, RestartCount: cs.RestartCount,
		}
		for state, detail := range cs.State {
			item.State, item.Reason = state, detail.Reason
		}
		for _, detail := range cs.LastState {
			item.LastReason, item.LastExitCode = detail.Reason, detail.ExitCode
		}
		out.Containers = append(out.Containers, item)
	}
	return out, nil
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func readFile(path string) ([]byte, error) { return osReadFile(path) }
