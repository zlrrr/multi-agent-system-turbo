package service

import (
	"context"

	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Replay reproduces a stored run's report from its record alone.
//
// It performs no collection and makes no model call: everything a report needs
// was persisted when the run happened, which is precisely what makes a stored
// run auditable rather than merely archived (Constitution Art. V.3).
func (s *Service) Replay(ctx context.Context, runID string) (*core.Report, error) {
	rec, err := s.store.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if rec.Report == nil {
		return nil, errs.New("MAS-6003", runID,
			"the run has no report; it did not complete (status "+string(rec.Status)+")")
	}
	return rec.Report, nil
}

// Run returns a stored run record, including every recorded step.
func (s *Service) Run(ctx context.Context, runID string) (*core.RunRecord, error) {
	return s.store.Get(ctx, runID)
}

// Runs lists stored runs, newest first.
func (s *Service) Runs(ctx context.Context, limit int) ([]core.RunSummary, error) {
	return s.store.List(ctx, limit)
}
