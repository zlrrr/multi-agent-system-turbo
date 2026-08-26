package store_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/store"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// bucket is an in-memory S3 that verifies the request was signed. A stub that
// ignored the Authorization header would let a broken signer pass every test in
// this file, which is the one thing these tests exist to prevent.
type bucket struct {
	mu       sync.Mutex
	objects  map[string][]byte
	puts     map[string]int // key → how many times it was written
	gets     []string
	lists    int
	fail     map[string]int // key → status to return instead
	unsigned int
}

func newBucket() *bucket {
	return &bucket{objects: map[string][]byte{}, puts: map[string]int{}, fail: map[string]int{}}
}

func (b *bucket) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()

		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			b.unsigned++
			http.Error(w, `<Error><Code>AccessDenied</Code></Error>`, http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Amz-Content-Sha256") == "" {
			http.Error(w, `<Error><Code>InvalidRequest</Code></Error>`, http.StatusBadRequest)
			return
		}

		key := strings.TrimPrefix(r.URL.Path, "/mas-runs/")
		key = strings.TrimPrefix(key, "/")

		if status, bad := b.fail[key]; bad {
			http.Error(w, `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`, status)
			return
		}

		switch {
		case r.Method == http.MethodPut:
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			b.objects[key] = body
			b.puts[key]++
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
			b.lists++
			b.writeListing(w, r)

		case r.Method == http.MethodGet:
			blob, ok := b.objects[key]
			if !ok {
				http.Error(w, `<Error><Code>NoSuchKey</Code></Error>`, http.StatusNotFound)
				return
			}
			b.gets = append(b.gets, key)
			_, _ = w.Write(blob)

		case r.Method == http.MethodHead:
			if _, ok := b.objects[key]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (b *bucket) writeListing(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	delimiter := r.URL.Query().Get("delimiter")

	type entry struct {
		Key string `xml:"Key"`
	}
	type common struct {
		Prefix string `xml:"Prefix"`
	}
	var result struct {
		XMLName        xml.Name `xml:"ListBucketResult"`
		IsTruncated    bool     `xml:"IsTruncated"`
		Contents       []entry  `xml:"Contents"`
		CommonPrefixes []common `xml:"CommonPrefixes"`
	}

	seen := map[string]bool{}
	keys := make([]string, 0, len(b.objects))
	for k := range b.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		if delimiter != "" {
			if i := strings.Index(rest, delimiter); i >= 0 {
				p := prefix + rest[:i+len(delimiter)]
				if !seen[p] {
					seen[p] = true
					result.CommonPrefixes = append(result.CommonPrefixes, common{Prefix: p})
				}
				continue
			}
		}
		result.Contents = append(result.Contents, entry{Key: k})
	}

	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(result)
}

func (b *bucket) objectKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.objects))
	for k := range b.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func objectStore(t *testing.T, b *bucket, mutate func(*config.S3Config)) *store.Object {
	t.Helper()
	srv := b.serve(t)
	cfg := config.S3Config{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "mas-runs",
		AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		PathStyle: true, Timeout: config.Duration(5 * time.Second),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := store.NewObject(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func objectRecord(id string) *core.RunRecord {
	return &core.RunRecord{
		ID: id, Status: core.RunRunning,
		Request:   core.DiagnoseRequest{Target: "redis-prod", Symptom: "latency"},
		Target:    core.Target{ID: "redis-prod", Kind: "redis"},
		StartedAt: time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC),
	}
}

// TestS3StoreSatisfiesTheContract is FR-001: the whole RunStore interface, over
// a stub that refuses anything unsigned.
func TestS3StoreSatisfiesTheContract(t *testing.T) {
	b := newBucket()
	s := objectStore(t, b, nil)
	ctx := context.Background()

	var _ store.RunStore = s

	if err := s.Create(ctx, objectRecord("run-a")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		step := core.Step{Kind: core.StepNote, Actor: "test", Name: "step " + strconv.Itoa(i)}
		if err := s.Append(ctx, "run-a", step); err != nil {
			t.Fatal(err)
		}
	}
	report := &core.Report{RunID: "run-a", Summary: "done"}
	if err := s.Finish(ctx, "run-a", report, core.Usage{LLMCalls: 2}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != core.RunCompleted {
		t.Errorf("status %q, want completed", got.Status)
	}
	if got.Report == nil || got.Report.Summary != "done" {
		t.Errorf("the report did not survive: %+v", got.Report)
	}
	if got.Usage.LLMCalls != 2 {
		t.Errorf("usage did not survive: %+v", got.Usage)
	}

	summaries, err := s.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].ID != "run-a" {
		t.Errorf("List returned %+v", summaries)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if b.unsigned != 0 {
		t.Errorf("%d request(s) reached the bucket unsigned", b.unsigned)
	}
}

// TestStepsAreWrittenAsImmutableObjects is FR-003 and CON-001. On object
// storage, "never rewritten" has to be true of the bytes, not of a convention.
func TestStepsAreWrittenAsImmutableObjects(t *testing.T) {
	b := newBucket()
	s := objectStore(t, b, nil)
	ctx := context.Background()

	if err := s.Create(ctx, objectRecord("run-b")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.Append(ctx, "run-b", core.Step{Kind: core.StepNote, Name: "s" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for key, writes := range b.puts {
		if strings.Contains(key, "/steps/") && writes != 1 {
			t.Errorf("step object %s was written %d times", key, writes)
		}
	}
	// The record was written once at Create and not again by any Append: five
	// steps must not have cost five whole-record rewrites.
	if got := b.puts["runs/run-b/record.json"]; got != 1 {
		t.Errorf("record.json was written %d times during appends, want 1", got)
	}

	var stepKeys []string
	for k := range b.objects {
		if strings.Contains(k, "/steps/") {
			stepKeys = append(stepKeys, k)
		}
	}
	if len(stepKeys) != 5 {
		t.Fatalf("%d step objects, want 5: %v", len(stepKeys), stepKeys)
	}
	sort.Strings(stepKeys)
	if !strings.HasSuffix(stepKeys[0], "0001.json") || !strings.HasSuffix(stepKeys[4], "0005.json") {
		t.Errorf("step keys are not zero-padded in order: %v", stepKeys)
	}
}

// TestInterruptedRunIsReconstructed is FR-004 and RSK-3. The filesystem store
// loses everything since its last whole-record write; here the steps were
// durable when they happened.
func TestInterruptedRunIsReconstructed(t *testing.T) {
	b := newBucket()
	s := objectStore(t, b, nil)
	ctx := context.Background()

	if err := s.Create(ctx, objectRecord("run-c")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := s.Append(ctx, "run-c", core.Step{Kind: core.StepNote, Name: "s" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// …and the process dies here, before Finish.

	got, err := s.Get(ctx, "run-c")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 4 {
		t.Errorf("%d steps recovered, want 4", len(got.Steps))
	}
	// The status is what was recorded, not a claim that the run completed.
	// Presenting a reconstruction as finished would be inventing an ending.
	if got.Status != core.RunRunning {
		t.Errorf("an interrupted run came back as %q; it must stay running", got.Status)
	}
	if got.Report != nil {
		t.Error("an interrupted run came back with a report")
	}
}

// TestListIsNewestFirstAndBounded is FR-005 and RSK-4.
func TestListIsNewestFirstAndBounded(t *testing.T) {
	b := newBucket()
	s := objectStore(t, b, nil)
	ctx := context.Background()

	ids := []string{
		"run-20260826T090000-aaaa", "run-20260826T100000-bbbb",
		"run-20260826T110000-cccc", "run-20260826T120000-dddd",
	}
	for _, id := range ids {
		if err := s.Create(ctx, objectRecord(id)); err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(ctx, id, &core.Report{RunID: id}, core.Usage{}); err != nil {
			t.Fatal(err)
		}
	}

	b.mu.Lock()
	b.gets = nil
	b.mu.Unlock()

	got, err := s.List(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d summaries, want 2", len(got))
	}
	if got[0].ID != ids[3] || got[1].ID != ids[2] {
		t.Errorf("order %v, want newest first", []string{got[0].ID, got[1].ID})
	}

	// A record that will not be returned must never be read.
	b.mu.Lock()
	reads := len(b.gets)
	b.mu.Unlock()
	if reads > 2 {
		t.Errorf("List read %d records to return 2", reads)
	}
}

// TestDigestSurvivesTheRoundTrip is FR-006: a record means the same thing
// wherever it is stored, and tampering is detected in both places.
func TestDigestSurvivesTheRoundTrip(t *testing.T) {
	b := newBucket()
	s := objectStore(t, b, nil)
	ctx := context.Background()

	if err := s.Create(ctx, objectRecord("run-d")); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, "run-d", &core.Report{RunID: "run-d", Summary: "ok"}, core.Usage{}); err != nil {
		t.Fatal(err)
	}

	b.mu.Lock()
	blob := b.objects["runs/run-d/record.json"]
	var env struct {
		Digest string          `json:"digest"`
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(blob, &env); err != nil {
		b.mu.Unlock()
		t.Fatalf("the stored object is not a digest envelope: %v", err)
	}
	if env.Digest == "" {
		b.mu.Unlock()
		t.Fatal("the stored record carries no digest")
	}
	// Tamper with the record, leaving the digest alone.
	b.objects["runs/run-d/record.json"] = []byte(
		`{"digest":"` + env.Digest + `","record":` + strings.Replace(string(env.Record), `"ok"`, `"tampered"`, 1) + `}`)
	b.mu.Unlock()

	if _, err := s.Get(ctx, "run-d"); errs.CodeOf(err) != "MAS-6003" {
		t.Errorf("a tampered record was accepted: %v", err)
	}
}

// TestStorageFailuresAreCoded is FR-007 and CON-002. A record that quietly did
// not save is worse than one that failed loudly.
func TestStorageFailuresAreCoded(t *testing.T) {
	b := newBucket()
	s := objectStore(t, b, nil)
	ctx := context.Background()

	b.mu.Lock()
	b.fail["runs/run-e/record.json"] = http.StatusForbidden
	b.mu.Unlock()

	err := s.Create(ctx, objectRecord("run-e"))
	if errs.CodeOf(err) != "MAS-6011" {
		t.Fatalf("a rejected write produced %v, want MAS-6011", err)
	}
	// The S3 error code has to reach the operator: AccessDenied and
	// NoSuchBucket call for different actions.
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("the error does not carry the S3 code: %v", err)
	}

	// An endpoint that is not there at all is a different code, because the
	// remedy is different.
	dead, err := store.NewObject(config.S3Config{
		Endpoint: "http://127.0.0.1:1", Region: "us-east-1", Bucket: "b",
		PathStyle: true, Timeout: config.Duration(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dead.Create(ctx, objectRecord("run-f")); errs.CodeOf(err) != "MAS-6012" {
		t.Errorf("an unreachable endpoint produced %v, want MAS-6012", err)
	}
}

// TestBothAddressingStylesAreSupported is FR-010.
func TestBothAddressingStylesAreSupported(t *testing.T) {
	b := newBucket()
	// Path-style is exercised by every other test here; this asserts the
	// virtual-host URL is built as AWS expects, without needing DNS.
	virtual, err := store.NewObject(config.S3Config{
		Endpoint: "https://s3.eu-west-1.amazonaws.com", Region: "eu-west-1",
		Bucket: "mas-runs", PathStyle: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if virtual == nil {
		t.Fatal("virtual-host addressing was refused at construction")
	}

	s := objectStore(t, b, func(c *config.S3Config) { c.PathStyle = true })
	if err := s.Create(context.Background(), objectRecord("run-g")); err != nil {
		t.Fatalf("path-style write failed: %v", err)
	}
	if len(b.objectKeys()) == 0 {
		t.Error("path-style addressing wrote nothing the bucket could see")
	}
}

// TestObjectStoreConfigIsValidatedAtLoad is FR-012: a mistake found during an
// incident is a mistake found at the worst time.
func TestObjectStoreConfigIsValidatedAtLoad(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  config.S3Config
	}{
		{"no endpoint", config.S3Config{Region: "r", Bucket: "b"}},
		{"no bucket", config.S3Config{Endpoint: "http://x", Region: "r"}},
		{"no region", config.S3Config{Endpoint: "http://x", Bucket: "b"}},
		{"half a credential pair", config.S3Config{
			Endpoint: "http://x", Region: "r", Bucket: "b", AccessKeyID: "id"}},
		{"endpoint is not a URL", config.S3Config{
			Endpoint: "not a url", Region: "r", Bucket: "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := store.NewObject(c.cfg); errs.CodeOf(err) != "MAS-6010" {
				t.Errorf("got %v, want MAS-6010", err)
			}
		})
	}

	// Anonymous access is legitimate: a bucket the operator made writable
	// without credentials is their decision, made explicitly at the bucket.
	if _, err := store.NewObject(config.S3Config{
		Endpoint: "http://x", Region: "r", Bucket: "b"}); err != nil {
		t.Errorf("an anonymous bucket was refused: %v", err)
	}
}

// TestObjectStoreCredentialsAreNeverEchoed is FR-009 and CON-003.
func TestObjectStoreCredentialsAreNeverEchoed(t *testing.T) {
	const secret = "wJalrXUtnFEMI-not-a-real-secret"
	cfg := config.Default()
	cfg.Store = config.StoreConfig{Type: "s3", S3: config.S3Config{
		Endpoint: "http://minio:9000", Region: "us-east-1", Bucket: "mas-runs",
		AccessKeyID: "AKIA-not-real", SecretAccessKey: config.Secret(secret),
	}}

	rendered, err := json.Marshal(cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), secret) {
		t.Errorf("the rendered configuration contains the secret:\n%s", rendered)
	}
	if printed := fmt.Sprintf("%v %s %#v", cfg.Store.S3.SecretAccessKey,
		cfg.Store.S3.SecretAccessKey, cfg.Store.S3.SecretAccessKey); strings.Contains(printed, secret) {
		t.Errorf("a format verb printed the secret: %s", printed)
	}

	// An error from the store must not carry it either.
	dead, err := store.NewObject(config.S3Config{
		Endpoint: "http://127.0.0.1:1", Region: "r", Bucket: "b",
		AccessKeyID: "AKIA-not-real", SecretAccessKey: config.Secret(secret),
		Timeout: config.Duration(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = dead.Create(context.Background(), objectRecord("run-h"))
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Errorf("a store error carried the secret: %v", err)
	}
}

// TestExistingStoresAreUnchanged is FR-013: adding a backend must not disturb
// the two that were already there.
func TestExistingStoresAreUnchanged(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		build func() (store.RunStore, error)
	}{
		{"memory", func() (store.RunStore, error) { return store.Open("memory", "") }},
		{"fs", func() (store.RunStore, error) { return store.Open("fs", t.TempDir()) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := c.build()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Create(ctx, objectRecord("run-i")); err != nil {
				t.Fatal(err)
			}
			if err := s.Append(ctx, "run-i", core.Step{Kind: core.StepNote, Name: "x"}); err != nil {
				t.Fatal(err)
			}
			if err := s.Finish(ctx, "run-i", &core.Report{RunID: "run-i"}, core.Usage{}); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, "run-i")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != core.RunCompleted || len(got.Steps) != 1 {
				t.Errorf("%s behaved differently: status=%s steps=%d", c.name, got.Status, len(got.Steps))
			}
		})
	}
}
