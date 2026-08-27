// Package loki reads a Loki-compatible HTTP API v1.
//
// Governs: specs/001-mvp-core/design-lld.md §2.8
package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Direction selects which end of the window to read from when the result is
// capped: backward returns the newest lines, which is what an incident needs.
type Direction string

const (
	Backward Direction = "backward"
	Forward  Direction = "forward"
)

// Client queries one log source.
type Client struct {
	name     string
	baseURL  string
	auth     config.AuthConfig
	tenantID string
	headers  map[string]string
	timeout  time.Duration
	maxLines int
	hc       *http.Client
}

// New builds a client for a configured source.
func New(cfg config.LogsSource, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout.D()}
	}
	maxLines := cfg.MaxLines
	if maxLines <= 0 {
		maxLines = 1000
	}
	to := cfg.Timeout.D()
	if to <= 0 {
		to = 20 * time.Second
	}
	return &Client{
		name: cfg.Name, baseURL: strings.TrimRight(cfg.URL, "/"), auth: cfg.Auth,
		tenantID: cfg.TenantID, headers: cfg.Headers, timeout: to, maxLines: maxLines, hc: hc,
	}
}

// Name reports the configured source name.
func (c *Client) Name() string { return c.name }

// BaseURL reports the configured endpoint.
func (c *Client) BaseURL() string { return c.baseURL }

// Timeout reports the per-request timeout.
func (c *Client) Timeout() time.Duration { return c.timeout }

// MaxLines reports the result ceiling.
func (c *Client) MaxLines() int { return c.maxLines }

// URLFor builds the absolute URL for an API path.
func (c *Client) URLFor(path string) string { return c.baseURL + path }

// Line is one log entry.
type Line struct {
	At     time.Time         `json:"at"`
	Labels map[string]string `json:"labels,omitempty"`
	Text   string            `json:"text"`
}

// Result is a normalised log query outcome.
type Result struct {
	Query     string   `json:"query"`
	Lines     []Line   `json:"lines"`
	Truncated bool     `json:"truncated"`
	Streams   int      `json:"streams"`
	Stats     string   `json:"stats,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Empty reports whether nothing matched.
func (r Result) Empty() bool { return len(r.Lines) == 0 }

// Summary renders a one-line digest naming the busiest labels and the newest
// line, which is usually the one an operator wants first.
func (r Result) Summary() string {
	if r.Empty() {
		return fmt.Sprintf("%s → no matching log lines", r.Query)
	}
	newest := r.Lines[0]
	for _, l := range r.Lines {
		if l.At.After(newest.At) {
			newest = l
		}
	}
	text := strings.TrimSpace(newest.Text)
	if len(text) > 160 {
		text = text[:157] + "…"
	}
	suffix := ""
	if r.Truncated {
		suffix = " (truncated)"
	}
	return fmt.Sprintf("%s → %d lines across %d streams%s; newest at %s: %s",
		r.Query, len(r.Lines), r.Streams, suffix, newest.At.UTC().Format(time.RFC3339), text)
}

// Query runs a LogQL query over a window.
func (c *Client) Query(ctx context.Context, logQL string, w core.Window, limit int, dir Direction) (Result, error) {
	if err := w.Validate(); err != nil {
		return Result{}, err
	}
	if limit <= 0 || limit > c.maxLines {
		limit = c.maxLines
	}
	if dir == "" {
		dir = Backward
	}
	form := url.Values{
		"query":     {logQL},
		"start":     {strconv.FormatInt(w.From.UnixNano(), 10)},
		"end":       {strconv.FormatInt(w.To.UnixNano(), 10)},
		"limit":     {strconv.Itoa(limit)},
		"direction": {string(dir)},
	}
	body, err := c.get(ctx, "/loki/api/v1/query_range", form)
	if err != nil {
		return Result{}, err
	}
	res, err := parseQuery(c.name, logQL, body, limit)
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// Labels lists the label names present in the window.
func (c *Client) Labels(ctx context.Context, w core.Window) ([]string, error) {
	form := url.Values{}
	if !w.From.IsZero() {
		form.Set("start", strconv.FormatInt(w.From.UnixNano(), 10))
		form.Set("end", strconv.FormatInt(w.To.UnixNano(), 10))
	}
	return c.stringList(ctx, "/loki/api/v1/labels", form)
}

// LabelValues lists the values a label takes in the window.
func (c *Client) LabelValues(ctx context.Context, label string, w core.Window) ([]string, error) {
	if !validLabel.MatchString(label) {
		return nil, errs.New("MAS-4104", fmt.Sprintf("%q is not a valid label name", label))
	}
	form := url.Values{}
	if !w.From.IsZero() {
		form.Set("start", strconv.FormatInt(w.From.UnixNano(), 10))
		form.Set("end", strconv.FormatInt(w.To.UnixNano(), 10))
	}
	return c.stringList(ctx, "/loki/api/v1/label/"+url.PathEscape(label)+"/values", form)
}

var validLabel = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Health probes the source, for `mas doctor`.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.Labels(ctx, core.Window{})
	return err
}

func (c *Client) stringList(ctx context.Context, path string, form url.Values) ([]string, error) {
	body, err := c.get(ctx, path, form)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, errs.Wrap(err, "MAS-4103", c.name, "response is not JSON")
	}
	if resp.Status != "success" {
		return nil, errs.New("MAS-4104", "label query rejected")
	}
	sort.Strings(resp.Data)
	return resp.Data, nil
}

func (c *Client) get(ctx context.Context, path string, form url.Values) ([]byte, error) {
	u := c.baseURL + path
	if enc := form.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4101", c.name, err.Error())
	}
	req.Header.Set("Accept", "application/json")
	if c.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.tenantID)
	}
	if err := applyAuth(req, c.auth, c.headers); err != nil {
		return nil, err
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4101", c.name, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, errs.Wrap(err, "MAS-4103", c.name, err.Error())
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusBadRequest:
		return nil, errs.New("MAS-4104", strings.TrimSpace(string(body)))
	default:
		return nil, errs.New("MAS-4102", c.name, resp.StatusCode)
	}
}

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

func parseQuery(source, query string, body []byte, limit int) (Result, error) {
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
			Stats json.RawMessage `json:"stats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Result{}, errs.Wrap(err, "MAS-4103", source, "response is not JSON")
	}
	if resp.Status != "success" {
		return Result{}, errs.New("MAS-4104", "query rejected by the log source")
	}

	out := Result{Query: query, Streams: len(resp.Data.Result)}
	for _, stream := range resp.Data.Result {
		for _, v := range stream.Values {
			if len(out.Lines) >= limit {
				out.Truncated = true
				break
			}
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				return Result{}, errs.Wrap(err, "MAS-4103", source, "log timestamp is not an integer")
			}
			out.Lines = append(out.Lines, Line{
				At: time.Unix(0, ns).UTC(), Labels: stream.Stream, Text: v[1],
			})
		}
		if out.Truncated {
			break
		}
	}
	// Newest first: an incident is read from the present backwards.
	sort.SliceStable(out.Lines, func(i, j int) bool { return out.Lines[i].At.After(out.Lines[j].At) })
	return out, nil
}

// LogLines implements core.LinesPayload, giving reasoning code a
// collector-independent view of this result.
func (r Result) LogLines() []string {
	out := make([]string, 0, len(r.Lines))
	for _, l := range r.Lines {
		out = append(out, l.Text)
	}
	return out
}
