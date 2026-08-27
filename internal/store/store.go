// Package store persists run records so a diagnosis can be audited and replayed
// without re-running it (Constitution Art. V.3).
//
// Governs: specs/001-mvp-core/design-lld.md §2.16
package store

import (
	"context"
	"sort"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// RunStore is the persistence seam. Within a run it is append-only: a step, once
// recorded, is never rewritten, which is what makes a stored run trustworthy as
// an audit trail.
type RunStore interface {
	Create(ctx context.Context, rec *core.RunRecord) error
	Append(ctx context.Context, runID string, step core.Step) error
	Finish(ctx context.Context, runID string, rep *core.Report, u core.Usage) error
	Fail(ctx context.Context, runID string, code, message string) error
	Get(ctx context.Context, runID string) (*core.RunRecord, error)
	List(ctx context.Context, limit int) ([]core.RunSummary, error)
	Close() error
}

// Memory is an in-process store, used by tests and by `--store memory`.
type Memory struct {
	mu   sync.RWMutex
	runs map[string]*core.RunRecord
	seq  []string
}

// NewMemory creates an empty in-memory store.
func NewMemory() *Memory { return &Memory{runs: map[string]*core.RunRecord{}} }

// Create records a new run.
func (m *Memory) Create(_ context.Context, rec *core.RunRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.runs[rec.ID]; exists {
		return errs.New("MAS-6002", rec.ID, "a run with this id already exists")
	}
	clone := *rec
	m.runs[rec.ID] = &clone
	m.seq = append(m.seq, rec.ID)
	return nil
}

// Append adds a step to a run.
func (m *Memory) Append(_ context.Context, runID string, step core.Step) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.runs[runID]
	if !ok {
		return errs.New("MAS-6001", runID)
	}
	if step.ID == "" {
		step.ID = nextStepID(len(rec.Steps))
	}
	rec.Steps = append(rec.Steps, step)
	return nil
}

// Finish attaches the report and marks the run complete.
func (m *Memory) Finish(_ context.Context, runID string, rep *core.Report, u core.Usage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.runs[runID]
	if !ok {
		return errs.New("MAS-6001", runID)
	}
	rec.Report = rep
	rec.Usage = u
	rec.Status = core.RunCompleted
	rec.FinishedAt = nowUTC()
	return nil
}

// Fail marks a run failed with the code that ended it.
func (m *Memory) Fail(_ context.Context, runID, code, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.runs[runID]
	if !ok {
		return errs.New("MAS-6001", runID)
	}
	rec.Status = core.RunFailed
	rec.FinishedAt = nowUTC()
	rec.Steps = append(rec.Steps, core.Step{
		ID: nextStepID(len(rec.Steps)), Kind: core.StepNote, At: nowUTC(),
		Actor: "service", Name: "run failed", Code: code, Err: message,
	})
	return nil
}

// Get returns a run record.
func (m *Memory) Get(_ context.Context, runID string) (*core.RunRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.runs[runID]
	if !ok {
		return nil, errs.New("MAS-6001", runID)
	}
	clone := *rec
	clone.Steps = append([]core.Step(nil), rec.Steps...)
	return &clone, nil
}

// List returns run summaries, newest first.
func (m *Memory) List(_ context.Context, limit int) ([]core.RunSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]core.RunSummary, 0, len(m.seq))
	for _, id := range m.seq {
		out = append(out, m.runs[id].Summarise())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Close is a no-op.
func (m *Memory) Close() error { return nil }
