// Package promql reads a Prometheus-compatible HTTP API v1 — Prometheus,
// VictoriaMetrics, Thanos or Mimir, which share the wire shape.
//
// Governs: specs/001-mvp-core/design-lld.md §2.7
package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Client queries one metrics source. It issues GET and POST-to-query only; it
// has no method capable of any other verb (HLD §7.3.1).
type Client struct {
	name    string
	baseURL string
	auth    config.AuthConfig
	headers map[string]string
	timeout time.Duration
	maxN    int
	hc      *http.Client
}

// New builds a client for a configured source.
func New(cfg config.MetricsSource, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout.D()}
	}
	maxN := cfg.MaxSamples
	if maxN <= 0 {
		maxN = 11000
	}
	to := cfg.Timeout.D()
	if to <= 0 {
		to = 15 * time.Second
	}
	return &Client{
		name: cfg.Name, baseURL: strings.TrimRight(cfg.URL, "/"), auth: cfg.Auth,
		headers: cfg.Headers, timeout: to, maxN: maxN, hc: hc,
	}
}

// Name reports the configured source name.
func (c *Client) Name() string { return c.name }

// BaseURL reports the configured endpoint.
func (c *Client) BaseURL() string { return c.baseURL }

// Timeout reports the per-request timeout.
func (c *Client) Timeout() time.Duration { return c.timeout }

// MaxSamples reports the truncation ceiling.
func (c *Client) MaxSamples() int { return c.maxN }

// URLFor builds the absolute URL for an API path, which the tool layer hands to
// the guard before the request is made.
func (c *Client) URLFor(path string) string { return c.baseURL + path }

// Sample is one (timestamp, value) point.
type Sample struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// Series is one labelled time series with its points and derived statistics.
type Series struct {
	Metric map[string]string `json:"metric"`
	Points []Sample          `json:"points,omitempty"`
	Last   float64           `json:"last"`
	Min    float64           `json:"min"`
	Max    float64           `json:"max"`
	Avg    float64           `json:"avg"`
	Count  int               `json:"count"`
}

// Result is the normalised outcome of a query.
type Result struct {
	Query      string   `json:"query"`
	ResultType string   `json:"result_type"`
	Series     []Series `json:"series"`
	Truncated  bool     `json:"truncated"`
	Warnings   []string `json:"warnings,omitempty"`
}

// Empty reports whether the query returned no series.
func (r Result) Empty() bool { return len(r.Series) == 0 }

// Summary renders a one-line digest for a report or a model prompt.
func (r Result) Summary() string {
	if r.Empty() {
		return fmt.Sprintf("%s → no data", r.Query)
	}
	if len(r.Series) == 1 {
		s := r.Series[0]
		if s.Count <= 1 {
			return fmt.Sprintf("%s → %s", r.Query, formatValue(s.Last))
		}
		return fmt.Sprintf("%s → last=%s min=%s max=%s avg=%s over %d points",
			r.Query, formatValue(s.Last), formatValue(s.Min), formatValue(s.Max), formatValue(s.Avg), s.Count)
	}
	parts := make([]string, 0, 3)
	for i, s := range r.Series {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("… %d more series", len(r.Series)-3))
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%s", labelDigest(s.Metric), formatValue(s.Last)))
	}
	return fmt.Sprintf("%s → %d series: %s", r.Query, len(r.Series), strings.Join(parts, ", "))
}

func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case v == math.Trunc(v) && math.Abs(v) < 1e15:
		return strconv.FormatFloat(v, 'f', 0, 64)
	default:
		return strconv.FormatFloat(v, 'g', 4, 64)
	}
}

func labelDigest(m map[string]string) string {
	for _, k := range []string{"instance", "pod", "node", "topic", "job"} {
		if v, ok := m[k]; ok {
			return v
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if k != "__name__" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return m["__name__"]
	}
	return m[keys[0]]
}

// Instant runs an instant query.
func (c *Client) Instant(ctx context.Context, query string, at time.Time) (Result, error) {
	form := url.Values{"query": {query}}
	if !at.IsZero() {
		form.Set("time", strconv.FormatInt(at.Unix(), 10))
	}
	return c.query(ctx, "/api/v1/query", query, form)
}

// Range runs a range query, choosing a step that keeps the result under the
// source's sample ceiling so a wide window degrades resolution rather than
// being refused.
func (c *Client) Range(ctx context.Context, query string, w core.Window, step time.Duration) (Result, error) {
	if err := w.Validate(); err != nil {
		return Result{}, err
	}
	if step <= 0 {
		step = AutoStep(w, c.maxN)
	}
	if points := int(w.Duration() / step); points > c.maxN {
		step = AutoStep(w, c.maxN)
	}
	form := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(w.From.Unix(), 10)},
		"end":   {strconv.FormatInt(w.To.Unix(), 10)},
		"step":  {strconv.Itoa(int(step.Seconds()))},
	}
	return c.query(ctx, "/api/v1/query_range", query, form)
}

// AutoStep picks a step that keeps a range query within maxSamples, rounded up
// to a human-friendly interval.
func AutoStep(w core.Window, maxSamples int) time.Duration {
	if maxSamples <= 0 {
		maxSamples = 11000
	}
	// Aim for ~250 points: enough to see shape, cheap to fetch and to summarise.
	target := w.Duration() / 250
	minimum := w.Duration() / time.Duration(maxSamples)
	if target < minimum {
		target = minimum
	}
	for _, candidate := range []time.Duration{
		15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
		5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
		time.Hour, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
	} {
		if candidate >= target {
			return candidate
		}
	}
	return 24 * time.Hour
}

// Series lists label sets matching the given selectors.
func (c *Client) Series(ctx context.Context, matchers []string, w core.Window) ([]map[string]string, error) {
	form := url.Values{"match[]": matchers}
	if !w.From.IsZero() {
		form.Set("start", strconv.FormatInt(w.From.Unix(), 10))
		form.Set("end", strconv.FormatInt(w.To.Unix(), 10))
	}
	body, err := c.post(ctx, "/api/v1/series", form)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
		Error  string              `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, errs.Wrap(err, "MAS-4003", c.name, "series response is not JSON")
	}
	if resp.Status != "success" {
		return nil, errs.New("MAS-4004", resp.Error)
	}
	if len(resp.Data) > c.maxN {
		resp.Data = resp.Data[:c.maxN]
	}
	return resp.Data, nil
}

// Health probes the source, for `mas doctor`.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.Instant(ctx, "vector(1)", time.Time{})
	return err
}

func (c *Client) query(ctx context.Context, path, query string, form url.Values) (Result, error) {
	body, err := c.post(ctx, path, form)
	if err != nil {
		return Result{}, err
	}
	return parseQueryResponse(c.name, query, body, c.maxN)
}

func (c *Client) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4001", c.name, err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if err := applyAuth(req, c.auth, c.headers); err != nil {
		return nil, err
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4001", c.name, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4003", c.name, err.Error())
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity:
		// Prometheus reports a rejected query with a JSON error body.
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(body))
		}
		return nil, errs.New("MAS-4004", e.Error)
	default:
		return nil, errs.New("MAS-4002", c.name, resp.StatusCode)
	}
}

// applyAuth attaches credentials. The resolved plaintext is written straight to
// the request header and never stored or logged.
func applyAuth(req *http.Request, a config.AuthConfig, headers map[string]string) error {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	switch a.Type {
	case "", "none":
		return nil
	case "bearer":
		tok, err := a.Token.Reveal()
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	case "basic":
		pw, err := a.Password.Reveal()
		if err != nil {
			return err
		}
		req.SetBasicAuth(a.Username, pw)
	case "header":
		tok, err := a.Token.Reveal()
		if err != nil {
			return err
		}
		req.Header.Set(a.Header, tok)
	}
	return nil
}

func parseQueryResponse(source, query string, body []byte, maxN int) (Result, error) {
	var resp struct {
		Status string   `json:"status"`
		Error  string   `json:"error"`
		Warns  []string `json:"warnings"`
		Data   struct {
			ResultType string            `json:"resultType"`
			Result     []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Result{}, errs.Wrap(err, "MAS-4003", source, "response is not JSON")
	}
	if resp.Status != "success" {
		return Result{}, errs.New("MAS-4004", firstNonEmpty(resp.Error, "query rejected"))
	}

	out := Result{Query: query, ResultType: resp.Data.ResultType, Warnings: resp.Warns}
	total := 0
	for _, raw := range resp.Data.Result {
		s, n, err := parseSeries(resp.Data.ResultType, raw)
		if err != nil {
			return Result{}, errs.Wrap(err, "MAS-4003", source, err.Error())
		}
		if total+n > maxN {
			out.Truncated = true
			break
		}
		total += n
		out.Series = append(out.Series, s)
	}
	sort.SliceStable(out.Series, func(i, j int) bool {
		return labelDigest(out.Series[i].Metric) < labelDigest(out.Series[j].Metric)
	})
	return out, nil
}

func parseSeries(resultType string, raw json.RawMessage) (Series, int, error) {
	switch resultType {
	case "vector":
		var v struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return Series{}, 0, err
		}
		p, err := parsePoint(v.Value)
		if err != nil {
			return Series{}, 0, err
		}
		return Series{Metric: v.Metric, Points: []Sample{p},
			Last: p.Value, Min: p.Value, Max: p.Value, Avg: p.Value, Count: 1}, 1, nil

	case "matrix":
		var m struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return Series{}, 0, err
		}
		s := Series{Metric: m.Metric, Min: math.Inf(1), Max: math.Inf(-1)}
		var sum float64
		for _, val := range m.Values {
			p, err := parsePoint(val)
			if err != nil {
				return Series{}, 0, err
			}
			s.Points = append(s.Points, p)
			if p.Value < s.Min {
				s.Min = p.Value
			}
			if p.Value > s.Max {
				s.Max = p.Value
			}
			sum += p.Value
			s.Last = p.Value
		}
		s.Count = len(s.Points)
		if s.Count > 0 {
			s.Avg = sum / float64(s.Count)
		} else {
			s.Min, s.Max = 0, 0
		}
		return s, max(s.Count, 1), nil

	case "scalar", "string":
		var v []any
		if err := json.Unmarshal(raw, &v); err != nil {
			return Series{}, 0, err
		}
		p, err := parsePoint(v)
		if err != nil {
			return Series{}, 0, err
		}
		return Series{Metric: map[string]string{}, Points: []Sample{p},
			Last: p.Value, Min: p.Value, Max: p.Value, Avg: p.Value, Count: 1}, 1, nil

	default:
		return Series{}, 0, fmt.Errorf("unsupported result type %q", resultType)
	}
}

func parsePoint(v []any) (Sample, error) {
	if len(v) != 2 {
		return Sample{}, fmt.Errorf("sample has %d elements, want 2", len(v))
	}
	ts, ok := v[0].(float64)
	if !ok {
		return Sample{}, fmt.Errorf("sample timestamp is not numeric")
	}
	raw, ok := v[1].(string)
	if !ok {
		return Sample{}, fmt.Errorf("sample value is not a string")
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		// Prometheus renders these as literal words.
		switch raw {
		case "NaN":
			f = math.NaN()
		case "+Inf":
			f = math.Inf(1)
		case "-Inf":
			f = math.Inf(-1)
		default:
			return Sample{}, fmt.Errorf("sample value %q is not a number", raw)
		}
	}
	sec, frac := math.Modf(ts)
	return Sample{At: time.Unix(int64(sec), int64(frac*1e9)).UTC(), Value: f}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Stats implements core.SeriesPayload, giving reasoning code a
// collector-independent view of this result.
func (r Result) Stats() core.SeriesStats {
	s := core.SeriesStats{
		Empty: r.Empty(), Series: len(r.Series), ByLabel: map[string]float64{},
		Min: math.Inf(1), Max: math.Inf(-1), Latest: math.Inf(-1), LatestMin: math.Inf(1),
	}
	if r.Empty() {
		s.Min, s.Max, s.Latest, s.LatestMin = 0, 0, 0, 0
		return s
	}
	var weighted float64
	var first, last float64
	haveFirst := false
	for _, series := range r.Series {
		if series.Last > s.Latest {
			s.Latest = series.Last
		}
		if series.Last < s.LatestMin {
			s.LatestMin = series.Last
		}
		if series.Min < s.Min {
			s.Min = series.Min
		}
		if series.Max > s.Max {
			s.Max = series.Max
		}
		s.Count += series.Count
		s.Sum += series.Last
		weighted += series.Avg * float64(series.Count)
		s.ByLabel[labelDigest(series.Metric)] = series.Last
		if len(series.Points) > 0 {
			if !haveFirst {
				first, haveFirst = series.Points[0].Value, true
			}
			last = series.Points[len(series.Points)-1].Value
		}
	}
	if s.Count > 0 {
		s.Avg = weighted / float64(s.Count)
	}
	if haveFirst {
		s.Delta = last - first
	}
	return s
}
