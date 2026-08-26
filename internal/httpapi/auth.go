package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Scope is what a credential may do.
type Scope string

const (
	// ScopeRead covers everything already computed: stored diagnoses, the
	// target list, topologies, packs and self-metrics.
	ScopeRead Scope = "read"
	// ScopeDiagnose covers starting a diagnosis, which spends model tokens and
	// reads production telemetry. A status page needs the first and must not
	// have this one.
	ScopeDiagnose Scope = "diagnose"
)

// Principal is who a request is from.
type Principal struct {
	Name   string
	Scopes map[Scope]bool
}

// Anonymous is the principal of an unauthenticated request on a server with no
// tokens configured — which admission permits only on a loopback bind.
const Anonymous = "anonymous"

// Can reports whether the principal holds a scope.
func (p Principal) Can(s Scope) bool { return p.Scopes[s] }

type principalKey struct{}

// PrincipalFrom returns the authenticated principal for a request.
func PrincipalFrom(ctx context.Context) Principal {
	if p, ok := ctx.Value(principalKey{}).(Principal); ok {
		return p
	}
	return Principal{Name: Anonymous}
}

// Authorizer is the single place a request is allowed through.
//
// One middleware rather than a check in each handler, for the same reason the
// safety guard is one function: a handler that authorises for itself is a
// handler that will one day be copied without the check
// (specs/009-api-authentication/design-hld.md §1).
type Authorizer struct {
	tokens map[[32]byte]Principal
	log    *slog.Logger
	lang   string
	on     bool
}

// anonymousRoutes answer without a credential. A liveness probe that needs one
// is a liveness probe that fails during a credential problem, and neither
// endpoint reveals anything about the estate.
var anonymousRoutes = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
}

// routeScopes is exhaustive and deny-by-default: a path with no entry here is
// refused rather than allowed, so adding a handler without wiring its scope
// fails closed (plan.md RSK-1).
var routeScopes = map[string]Scope{
	"/api/v1/diagnoses":  ScopeRead, // POST is raised to diagnose below
	"/api/v1/diagnoses/": ScopeRead,
	"/api/v1/targets":    ScopeRead,
	"/api/v1/topologies": ScopeRead,
	"/api/v1/packs":      ScopeRead,
	"/metrics":           ScopeRead,
	"/":                  ScopeRead,
}

// NewAuthorizer builds the authorizer for a server configuration.
func NewAuthorizer(cfg config.ServerConfig, lang string, log *slog.Logger) (*Authorizer, error) {
	a := &Authorizer{tokens: map[[32]byte]Principal{}, log: log, lang: lang}
	if log == nil {
		a.log = slog.Default()
	}
	for _, t := range cfg.Auth.Tokens {
		plain, err := t.Token.Reveal()
		if err != nil {
			return nil, err
		}
		scopes := map[Scope]bool{}
		for _, s := range t.Scopes {
			scopes[Scope(s)] = true
		}
		a.tokens[sha256.Sum256([]byte(plain))] = Principal{Name: t.Name, Scopes: scopes}
		a.on = true
	}
	return a, nil
}

// Enabled reports whether any credential is required.
func (a *Authorizer) Enabled() bool { return a.on }

// ScopeFor returns the scope a request needs, and whether the route is known.
func ScopeFor(r *http.Request) (Scope, bool) {
	if anonymousRoutes[r.URL.Path] {
		return "", false
	}
	// Starting a diagnosis is the one write-shaped operation: it spends model
	// tokens and reads production. Everything else on the same path reads what
	// has already been computed.
	if r.URL.Path == "/api/v1/diagnoses" && r.Method == http.MethodPost {
		return ScopeDiagnose, true
	}
	if s, ok := routeScopes[r.URL.Path]; ok {
		return s, true
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/diagnoses/") {
		return ScopeRead, true
	}
	return "", true // known to need authorisation, with no scope that grants it
}

// Wrap puts every non-anonymous route behind the authorizer.
func (a *Authorizer) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if anonymousRoutes[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		if !a.on {
			next.ServeHTTP(w, r.WithContext(
				context.WithValue(r.Context(), principalKey{}, Principal{Name: Anonymous})))
			return
		}

		scope, known := ScopeFor(r)
		principal, ok := a.identify(r)
		if !ok {
			// The same code and body for "no credential" and "unknown
			// credential": telling an attacker which half is wrong tells them
			// which half to work on.
			a.audit(r, Anonymous, "denied", "no usable credential")
			writeCodedError(w, http.StatusUnauthorized, errs.New("MAS-7012"), a.lang)
			return
		}
		if !known || scope == "" || !principal.Can(scope) {
			a.audit(r, principal.Name, "denied", "missing scope "+string(scope))
			writeCodedError(w, http.StatusForbidden, errs.New("MAS-7014", string(scope)), a.lang)
			return
		}

		// Grants are audited too, not only refusals: a log that shows only
		// denials cannot answer "who ran this", which is the question actually
		// asked afterwards (design-hld.md §4).
		a.audit(r, principal.Name, "allowed", string(scope))
		next.ServeHTTP(w, r.WithContext(
			context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

// identify resolves the bearer token to a principal.
//
// Both sides are reduced to a fixed-size digest before comparison, so
// subtle.ConstantTimeCompare runs over equal lengths and length never branches
// (plan.md RSK-4). The map lookup is not constant-time in the digest, which
// reveals nothing: a digest is not reversible, and anyone holding one already
// holds the token.
func (a *Authorizer) identify(r *http.Request) (Principal, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return Principal{}, false
	}
	presented := sha256.Sum256([]byte(strings.TrimSpace(header[len(prefix):])))
	for digest, p := range a.tokens {
		if subtle.ConstantTimeCompare(digest[:], presented[:]) == 1 {
			return p, true
		}
	}
	return Principal{}, false
}

// audit records the decision. The credential is never an argument here, and the
// Authorization header is never read into a log field.
func (a *Authorizer) audit(r *http.Request, principal, outcome, detail string) {
	a.log.Info("api authorisation",
		"principal", principal,
		"outcome", outcome,
		"method", r.Method,
		"path", r.URL.Path,
		"detail", detail)
}

// KnownScopes lists the scopes this build recognises, for `mas doctor` and the
// configuration reference.
func KnownScopes() []string {
	out := make([]string, 0, len(config.APIScopes))
	for s := range config.APIScopes {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
