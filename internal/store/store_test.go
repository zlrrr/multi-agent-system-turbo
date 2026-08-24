package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/store"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func sampleRecord(id string, started time.Time) *core.RunRecord {
	return &core.RunRecord{
		ID: id, Status: core.RunRunning, StartedAt: started,
		Request: core.DiagnoseRequest{Target: "redis-prod", Symptom: "latency", Topology: "supervisor"},
		Target:  core.Target{ID: "redis-prod", Kind: core.KindRedis},
	}
}

func sampleReport(runID string) *core.Report {
	return &core.Report{
		Schema: core.ReportSchema, RunID: runID, GeneratedAt: time.Now().UTC(),
		Summary:    "memory pressure",
		Hypotheses: []core.Hypothesis{{ID: "h-1", Statement: "at maxmemory", Confidence: 0.9}},
	}
}

// eachStore runs a test body against both implementations, so the interface
// contract is proven for the seam and not just for one side of it.
func eachStore(t *testing.T, body func(t *testing.T, s store.RunStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { body(t, store.NewMemory()) })
	t.Run("fs", func(t *testing.T) {
		s, err := store.NewFS(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		body(t, s)
	})
}

func TestRoundTrip(t *testing.T) {
	eachStore(t, func(t *testing.T, s store.RunStore) {
		ctx := context.Background()
		rec := sampleRecord("run-1", time.Now().UTC())
		if err := s.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if err := s.Append(ctx, "run-1", core.Step{
				Kind: core.StepToolCall, Actor: "tool", Name: "promql.instant", At: time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}
		}
		rep := sampleReport("run-1")
		if err := s.Finish(ctx, "run-1", rep, core.Usage{LLMCalls: 4, ToolCalls: 3}); err != nil {
			t.Fatal(err)
		}

		got, err := s.Get(ctx, "run-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != core.RunCompleted {
			t.Fatalf("status = %s", got.Status)
		}
		if len(got.Steps) != 3 {
			t.Fatalf("steps = %d", len(got.Steps))
		}
		if got.Report == nil || got.Report.Summary != "memory pressure" {
			t.Fatalf("report lost: %+v", got.Report)
		}
		if got.Usage.LLMCalls != 4 {
			t.Fatalf("usage lost: %+v", got.Usage)
		}
		if got.FinishedAt.IsZero() {
			t.Error("finish time not recorded")
		}
		for i, st := range got.Steps {
			if st.ID == "" {
				t.Errorf("step %d has no id", i)
			}
		}
	})
}

func TestAppendOnlyOrdering(t *testing.T) {
	eachStore(t, func(t *testing.T, s store.RunStore) {
		ctx := context.Background()
		if err := s.Create(ctx, sampleRecord("run-1", time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"first", "second", "third"} {
			if err := s.Append(ctx, "run-1", core.Step{Name: name, At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		}
		got, _ := s.Get(ctx, "run-1")
		for i, want := range []string{"first", "second", "third"} {
			if got.Steps[i].Name != want {
				t.Fatalf("step %d = %s, want %s; the record is not append-only in order", i, got.Steps[i].Name, want)
			}
		}
	})
}

func TestDuplicateCreateRefused(t *testing.T) {
	eachStore(t, func(t *testing.T, s store.RunStore) {
		ctx := context.Background()
		rec := sampleRecord("run-1", time.Now().UTC())
		if err := s.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if err := s.Create(ctx, rec); errs.CodeOf(err) != "MAS-6002" {
			t.Fatalf("got %v, want MAS-6002; overwriting a run would destroy an audit trail", err)
		}
	})
}

func TestUnknownRunIsCoded(t *testing.T) {
	eachStore(t, func(t *testing.T, s store.RunStore) {
		ctx := context.Background()
		if _, err := s.Get(ctx, "run-nope"); errs.CodeOf(err) != "MAS-6001" {
			t.Fatalf("Get: got %v, want MAS-6001", err)
		}
		if err := s.Append(ctx, "run-nope", core.Step{}); errs.CodeOf(err) != "MAS-6001" {
			t.Fatalf("Append: got %v, want MAS-6001", err)
		}
		if err := s.Finish(ctx, "run-nope", nil, core.Usage{}); errs.CodeOf(err) != "MAS-6001" {
			t.Fatalf("Finish: got %v, want MAS-6001", err)
		}
	})
}

func TestFailRecordsTheReason(t *testing.T) {
	eachStore(t, func(t *testing.T, s store.RunStore) {
		ctx := context.Background()
		if err := s.Create(ctx, sampleRecord("run-1", time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
		if err := s.Fail(ctx, "run-1", "MAS-4001", "metrics unreachable"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, "run-1")
		if got.Status != core.RunFailed {
			t.Fatalf("status = %s", got.Status)
		}
		last := got.Steps[len(got.Steps)-1]
		if last.Code != "MAS-4001" || last.Err == "" {
			t.Fatalf("failure not recorded in the trail: %+v", last)
		}
	})
}

func TestListNewestFirst(t *testing.T) {
	eachStore(t, func(t *testing.T, s store.RunStore) {
		ctx := context.Background()
		base := time.Now().UTC()
		for i, id := range []string{"run-old", "run-mid", "run-new"} {
			rec := sampleRecord(id, base.Add(time.Duration(i)*time.Minute))
			if err := s.Create(ctx, rec); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.List(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || got[0].ID != "run-new" {
			t.Fatalf("listing is not newest-first: %+v", got)
		}
		limited, _ := s.List(ctx, 2)
		if len(limited) != 2 {
			t.Fatalf("limit ignored: %d", len(limited))
		}
	})
}

func TestSummaryProjection(t *testing.T) {
	eachStore(t, func(t *testing.T, s store.RunStore) {
		ctx := context.Background()
		if err := s.Create(ctx, sampleRecord("run-1", time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
		_ = s.Finish(ctx, "run-1", sampleReport("run-1"), core.Usage{})
		got, _ := s.List(ctx, 0)
		if got[0].Target != "redis-prod" || got[0].Topology != "supervisor" || got[0].Hypotheses != 1 {
			t.Fatalf("summary = %+v", got[0])
		}
	})
}

// TestCorruptDetected proves a stored run is trustworthy: a modified record is
// refused rather than replayed as if it were genuine.
func TestCorruptDetected(t *testing.T) {
	dir := t.TempDir()
	s, err := store.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Create(ctx, sampleRecord("run-1", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "run-1.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte(replaceFirst(string(body), `"symptom":"latency"`, `"symptom":"tampered"`))
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Get(ctx, "run-1"); errs.CodeOf(err) != "MAS-6003" {
		t.Fatalf("got %v, want MAS-6003", err)
	}
}

func replaceFirst(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestTruncatedFileDetected(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.NewFS(dir)
	ctx := context.Background()
	_ = s.Create(ctx, sampleRecord("run-1", time.Now().UTC()))

	path := filepath.Join(dir, "run-1.json")
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, body[:len(body)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "run-1"); errs.CodeOf(err) != "MAS-6003" {
		t.Fatalf("got %v, want MAS-6003", err)
	}
}

func TestCorruptRecordDoesNotHideHealthyOnes(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.NewFS(dir)
	ctx := context.Background()
	_ = s.Create(ctx, sampleRecord("run-good", time.Now().UTC()))
	if err := os.WriteFile(filepath.Join(dir, "run-bad.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx, 0)
	if err != nil {
		t.Fatalf("a corrupt record broke the whole listing: %v", err)
	}
	if len(got) != 1 || got[0].ID != "run-good" {
		t.Fatalf("listing = %+v", got)
	}
}

func TestPathTraversalRefused(t *testing.T) {
	s, _ := store.NewFS(t.TempDir())
	ctx := context.Background()
	for _, id := range []string{"../escape", "a/b", "", "..", "with space"} {
		if _, err := s.Get(ctx, id); errs.CodeOf(err) != "MAS-6001" {
			t.Errorf("run id %q: got %v, want MAS-6001", id, err)
		}
	}
}

func TestWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.NewFS(dir)
	ctx := context.Background()
	_ = s.Create(ctx, sampleRecord("run-1", time.Now().UTC()))
	_ = s.Append(ctx, "run-1", core.Step{Name: "x", At: time.Now().UTC()})

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("a temporary file was left behind: %s", e.Name())
		}
	}
}

func TestOpenSelectsImplementation(t *testing.T) {
	if s, err := store.Open("memory", ""); err != nil || s == nil {
		t.Fatalf("memory: %v", err)
	}
	if s, err := store.Open("fs", t.TempDir()); err != nil || s == nil {
		t.Fatalf("fs: %v", err)
	}
	if s, err := store.Open("", t.TempDir()); err != nil || s == nil {
		t.Fatalf("default should be fs: %v", err)
	}
	if _, err := store.Open("s3", ""); errs.CodeOf(err) != "MAS-6004" {
		t.Fatalf("got %v, want MAS-6004", err)
	}
	if _, err := store.NewFS(""); errs.CodeOf(err) != "MAS-6004" {
		t.Fatalf("empty dir: got %v, want MAS-6004", err)
	}
}
