package store

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// s3Client is the smallest S3 surface a run store needs: PUT, GET, LIST, HEAD.
//
// Written over net/http rather than an SDK because every feature in this
// project has held go.mod unchanged, and the whole of what is needed here is
// four verbs and one XML response shape.
type s3Client struct {
	cfg  config.S3Config
	http *http.Client
	now  func() time.Time
}

func newS3Client(cfg config.S3Config) (*s3Client, error) {
	timeout := cfg.Timeout.D()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &s3Client{
		cfg:  cfg,
		http: &http.Client{Timeout: timeout},
		now:  func() time.Time { return time.Now().UTC() },
	}, nil
}

// endpointFor builds the URL for a key.
//
// Path-style puts the bucket in the path, which is what MinIO, Ceph RGW and
// most self-hosted deployments need; virtual-host puts it in the hostname,
// which is what AWS prefers (FR-010).
func (c *s3Client) endpointFor(key string, query url.Values) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(c.cfg.Endpoint, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errs.New("MAS-6010", "store.s3.endpoint", "must be an absolute URL")
	}
	u := *base
	if c.cfg.PathStyle {
		u.Path = "/" + c.cfg.Bucket
	} else {
		u.Host = c.cfg.Bucket + "." + base.Host
		u.Path = ""
	}
	if key != "" {
		u.Path += "/" + strings.TrimLeft(key, "/")
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return &u, nil
}

func (c *s3Client) do(ctx context.Context, method, key string, query url.Values, body []byte) ([]byte, error) {
	u, err := c.endpointFor(key, query)
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-6012", c.cfg.Endpoint, err.Error())
	}
	if body != nil {
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
	}

	// Signing is skipped only when no credentials are configured, which is a
	// bucket the operator has made anonymously writable — their choice, and one
	// they had to make explicitly at the bucket.
	if !c.cfg.AccessKeyID.IsZero() {
		id, err := c.cfg.AccessKeyID.Reveal()
		if err != nil {
			return nil, err
		}
		secret, err := c.cfg.SecretAccessKey.Reveal()
		if err != nil {
			return nil, err
		}
		Sign(req, hexSHA256(body), id, secret, c.cfg.Region, "s3", c.now())
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-6012", c.cfg.Endpoint, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, errs.Wrap(err, "MAS-6012", c.cfg.Endpoint, err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The S3 error code is in the body and says what to do: AccessDenied is
		// credentials, NoSuchBucket is the wrong bucket or region,
		// SignatureDoesNotMatch is usually a skewed clock.
		return nil, errs.New("MAS-6011", resp.StatusCode, key, s3ErrorCode(payload, resp.Status))
	}
	return payload, nil
}

// s3Error is the error document every S3 implementation returns.
type s3Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func s3ErrorCode(body []byte, fallback string) string {
	var e s3Error
	if err := xml.Unmarshal(body, &e); err == nil && e.Code != "" {
		if e.Message != "" {
			return e.Code + ": " + e.Message
		}
		return e.Code
	}
	if len(body) > 0 && len(body) < 200 {
		return strings.TrimSpace(string(body))
	}
	return fallback
}

func (c *s3Client) put(ctx context.Context, key string, body []byte) error {
	_, err := c.do(ctx, http.MethodPut, key, nil, body)
	return err
}

func (c *s3Client) get(ctx context.Context, key string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, key, nil, nil)
}

// listResult is the subset of ListObjectsV2 this store reads.
type listResult struct {
	XMLName     xml.Name `xml:"ListBucketResult"`
	IsTruncated bool     `xml:"IsTruncated"`
	NextToken   string   `xml:"NextContinuationToken"`
	Contents    []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

func (c *s3Client) list(ctx context.Context, prefix, delimiter, token string, max int) (listResult, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if delimiter != "" {
		q.Set("delimiter", delimiter)
	}
	if token != "" {
		q.Set("continuation-token", token)
	}
	if max > 0 {
		q.Set("max-keys", strconv.Itoa(max))
	}

	body, err := c.do(ctx, http.MethodGet, "", q, nil)
	if err != nil {
		return listResult{}, err
	}
	var out listResult
	if err := xml.Unmarshal(body, &out); err != nil {
		return listResult{}, errs.Wrap(err, "MAS-6013", prefix, err.Error())
	}
	return out, nil
}
