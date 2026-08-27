package httpapi

import (
	"net/http"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/obs"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// MayReach reports whether a principal may act on a target.
//
// This is the only place tenancy is decided. A handler that compared tenants
// for itself would be a handler that gets copied without the comparison — the
// defect this project has now fixed four times in other guises, and the reason
// the safety guard and the authorizer are each one function
// (specs/011-tenant-registry/design-hld.md §1).
//
// With tenancy off — no target names a tenant — every principal reaches
// everything, which is today's behaviour exactly.
func (s *Server) MayReach(p Principal, targetID string) bool {
	cfg := s.svc.Config()
	if !cfg.MultiTenant() {
		return true
	}
	tenant, known := cfg.TenantOf(targetID)
	if !known {
		return false
	}
	return p.Tenants[tenant]
}

// tenantFor returns the tenant a target belongs to, for recording on the run.
func (s *Server) tenantFor(targetID string) string {
	tenant, _ := s.svc.Config().TenantOf(targetID)
	return tenant
}

// reachableTargets filters a target list to what a principal may see.
func (s *Server) reachableTargets(p Principal, targets []config.TargetConfig) []config.TargetConfig {
	if !s.svc.Config().MultiTenant() {
		return targets
	}
	out := make([]config.TargetConfig, 0, len(targets))
	for _, t := range targets {
		if s.MayReach(p, t.ID) {
			out = append(out, t)
		}
	}
	return out
}

// reachableRuns filters a run listing by the tenant each run recorded.
//
// The recorded tenant, not the target's current one: configuration changes, and
// a listing that re-derived it would show the estate as it is now rather than
// as it was when the run happened.
func (s *Server) reachableRuns(p Principal, runs []core.RunSummary) []core.RunSummary {
	if !s.svc.Config().MultiTenant() {
		return runs
	}
	out := make([]core.RunSummary, 0, len(runs))
	for _, r := range runs {
		if p.Tenants[r.Tenant] {
			out = append(out, r)
		}
	}
	return out
}

// mayReadRun reports whether a principal may see a stored run.
func (s *Server) mayReadRun(p Principal, rec *core.RunRecord) bool {
	if !s.svc.Config().MultiTenant() {
		return true
	}
	return rec != nil && p.Tenants[rec.Tenant]
}

// refuseAsUnknown refuses a cross-tenant target or run identically to one that
// was never configured.
//
// A 403 naming the target would confirm it exists, which is the neighbour's
// information rather than the caller's, and it leaks once per guessed id. The
// distinction stays inside the process, in the audit log, where it is both real
// and safe (design-hld.md §3).
func (s *Server) refuseAsUnknown(w http.ResponseWriter, r *http.Request, principal, what string) {
	// The audit log is where the distinction is safe to keep: an operator
	// debugging a filtered listing needs it, and it never reaches the wire.
	obs.Log(r.Context()).Info("tenancy",
		"principal", principal, "outcome", "denied", "path", r.URL.Path,
		"code", "MAS-7015")
	s.writeError(w, r, http.StatusNotFound, errs.New("MAS-7404", what))
}
