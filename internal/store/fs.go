package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// FS stores each run as a single JSON document with an integrity digest.
//
// One file per run rather than an append log: a run is written once at each
// lifecycle point and read whole, so the simpler shape wins. The digest is what
// makes tampering or truncation detectable at read time (MAS-6003).
type FS struct {
	dir string
	mu  sync.Mutex
}

// envelope wraps a record with its digest.
type envelope struct {
	Digest string          `json:"digest"`
	Record json.RawMessage `json:"record"`
}

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// NewFS opens or creates a filesystem store.
func NewFS(dir string) (*FS, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errs.New("MAS-6004", "store.dir is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, errs.Wrap(err, "MAS-6004", err.Error())
	}
	return &FS{dir: dir}, nil
}

// Dir reports where runs are stored.
func (f *FS) Dir() string { return f.dir }

func (f *FS) path(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errs.New("MAS-6001", runID)
	}
	return filepath.Join(f.dir, runID+".json"), nil
}

// Create writes the initial record.
func (f *FS) Create(_ context.Context, rec *core.RunRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.path(rec.ID)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(p); statErr == nil {
		return errs.New("MAS-6002", rec.ID, "a run with this id already exists")
	}
	return f.write(p, rec)
}

// Append adds a step.
func (f *FS) Append(_ context.Context, runID string, step core.Step) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, p, err := f.load(runID)
	if err != nil {
		return err
	}
	if step.ID == "" {
		step.ID = nextStepID(len(rec.Steps))
	}
	rec.Steps = append(rec.Steps, step)
	return f.write(p, rec)
}

// Finish attaches the report and marks the run complete.
func (f *FS) Finish(_ context.Context, runID string, rep *core.Report, u core.Usage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, p, err := f.load(runID)
	if err != nil {
		return err
	}
	rec.Report = rep
	rec.Usage = u
	rec.Status = core.RunCompleted
	rec.FinishedAt = nowUTC()
	return f.write(p, rec)
}

// Fail marks a run failed.
func (f *FS) Fail(_ context.Context, runID, code, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, p, err := f.load(runID)
	if err != nil {
		return err
	}
	rec.Status = core.RunFailed
	rec.FinishedAt = nowUTC()
	rec.Steps = append(rec.Steps, core.Step{
		ID: nextStepID(len(rec.Steps)), Kind: core.StepNote, At: nowUTC(),
		Actor: "service", Name: "run failed", Code: code, Err: message,
	})
	return f.write(p, rec)
}

// Get reads a run record, verifying its integrity.
func (f *FS) Get(_ context.Context, runID string) (*core.RunRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, _, err := f.load(runID)
	return rec, err
}

// List returns run summaries, newest first.
func (f *FS) List(_ context.Context, limit int) ([]core.RunSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-6004", err.Error())
	}
	var out []core.RunSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		rec, _, err := f.load(id)
		if err != nil {
			continue // a corrupt record must not hide the healthy ones
		}
		out = append(out, rec.Summarise())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Close is a no-op.
func (f *FS) Close() error { return nil }

func (f *FS) load(runID string) (*core.RunRecord, string, error) {
	p, err := f.path(runID)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(p) //nolint:gosec // path is validated above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, p, errs.New("MAS-6001", runID)
		}
		return nil, p, errs.Wrap(err, "MAS-6004", err.Error())
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, p, errs.Wrap(err, "MAS-6003", runID, "the record is not valid JSON")
	}
	if got := digest(env.Record); got != env.Digest {
		return nil, p, errs.New("MAS-6003", runID,
			"the integrity digest does not match; the record was modified or truncated")
	}
	var rec core.RunRecord
	if err := json.Unmarshal(env.Record, &rec); err != nil {
		return nil, p, errs.Wrap(err, "MAS-6003", runID, err.Error())
	}
	return &rec, p, nil
}

// write serialises atomically: a crash mid-write leaves the previous record
// intact rather than a half-written one.
func (f *FS) write(path string, rec *core.RunRecord) error {
	body, err := json.Marshal(rec)
	if err != nil {
		return errs.Wrap(err, "MAS-6002", rec.ID, err.Error())
	}
	env := envelope{Digest: digest(body), Record: body}
	// Compact, not indented: MarshalIndent re-indents the embedded RawMessage,
	// which would change the bytes the digest was computed over and make every
	// record read back as corrupt.
	blob, err := json.Marshal(env)
	if err != nil {
		return errs.Wrap(err, "MAS-6002", rec.ID, err.Error())
	}
	blob = append(blob, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o640); err != nil {
		return errs.Wrap(err, "MAS-6002", rec.ID, err.Error())
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return errs.Wrap(err, "MAS-6002", rec.ID, err.Error())
	}
	return nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func nextStepID(n int) string { return "s-" + itoa(n+1) }

func nowUTC() time.Time { return time.Now().UTC() }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Open builds the configured store.
func Open(kind, dir string) (RunStore, error) {
	switch kind {
	case "", "fs":
		return NewFS(dir)
	case "memory":
		return NewMemory(), nil
	default:
		return nil, errs.New("MAS-6004", "unknown store type "+kind)
	}
}
