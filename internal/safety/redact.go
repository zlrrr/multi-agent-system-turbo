// Package safety enforces the read-only invariant (Constitution Art. IV) and
// keeps credentials out of every output.
//
// Governs: specs/001-mvp-core/design-lld.md §2.5, design-hld.md §7.3
package safety

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Mask replaces any redacted span.
const Mask = "«redacted»"

// DefaultRedactPatterns catch the shapes credentials usually take when they
// escape into a message: header values, query parameters, connection strings
// and assignment expressions.
var DefaultRedactPatterns = []string{
	`(?i)\b(authorization|proxy-authorization)\s*[:=]\s*\S+`,
	`(?i)\bbearer\s+[A-Za-z0-9._\-]{8,}`,
	`(?i)\b(api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|secret[_-]?key|private[_-]?key|password|passwd|pwd|token|credential)\b\s*[:=]\s*("[^"]*"|'[^']*'|\S+)`,
	`(?i)[?&](api[_-]?key|token|password|passwd|secret)=[^&\s]+`,
	`(?i)\b[a-z][a-z0-9+.\-]*://[^/\s:@]+:[^/\s@]+@`,                     // scheme://user:pass@host
	`\bsk-[A-Za-z0-9_\-]{16,}`,                                           // OpenAI-style keys
	`\bsk-ant-[A-Za-z0-9_\-]{16,}`,                                       // Anthropic-style keys
	`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`, // JWT
	`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`,
}

// Redactor removes credentials from any string or structure on its way to a
// log, a report, a run record or a model prompt (FR-016).
//
// Redaction lives at the boundary handler rather than at each call site, so a
// new call site cannot leak by forgetting to redact (HLD §7.3.5).
type Redactor struct {
	mu       sync.RWMutex
	patterns []*regexp.Regexp
	literals []string // sorted longest-first so nested secrets mask fully
}

// NewRedactor builds a redactor from the default patterns, any extra patterns
// supplied by configuration, and known literal secret values.
//
// An extra pattern that does not compile is skipped rather than fatal: a typo in
// an operator's redaction rule must never take the tool down, and `mas doctor`
// reports it.
func NewRedactor(extraPatterns []string, literals []string) *Redactor {
	r := &Redactor{}
	for _, p := range append(append([]string{}, DefaultRedactPatterns...), extraPatterns...) {
		if re, err := regexp.Compile(p); err == nil {
			r.patterns = append(r.patterns, re)
		}
	}
	r.AddLiterals(literals...)
	return r
}

// AddLiterals registers exact secret values discovered at runtime, such as a
// resolved API key. Short values are ignored: masking every occurrence of a
// two-character string would destroy the output without protecting anything.
func (r *Redactor) AddLiterals(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < 6 {
			continue
		}
		if !containsString(r.literals, v) {
			r.literals = append(r.literals, v)
		}
	}
	sort.Slice(r.literals, func(i, j int) bool { return len(r.literals[i]) > len(r.literals[j]) })
}

// Redact returns s with every known secret masked.
func (r *Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, lit := range r.literals {
		s = strings.ReplaceAll(s, lit, Mask)
	}
	for _, re := range r.patterns {
		s = re.ReplaceAllString(s, Mask)
	}
	return s
}

// RedactAny walks a JSON-like value and redacts every string within it. Map keys
// whose name suggests a credential have their value masked outright, regardless
// of the value's shape.
func (r *Redactor) RedactAny(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return r.Redact(t)
	case []string:
		out := make([]string, len(t))
		for i, s := range t {
			out[i] = r.Redact(s)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if isSensitiveKey(k) {
				out[k] = Mask
				continue
			}
			out[k] = r.RedactAny(val)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, val := range t {
			if isSensitiveKey(k) {
				out[k] = Mask
				continue
			}
			out[k] = r.Redact(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = r.RedactAny(val)
		}
		return out
	case error:
		return r.Redact(t.Error())
	case fmt.Stringer:
		return r.Redact(t.String())
	default:
		return v
	}
}

var sensitiveKeys = regexp.MustCompile(`(?i)^(authorization|api[_-]?key|apikey|token|access[_-]?token|refresh[_-]?token|password|passwd|pwd|secret|secret[_-]?key|private[_-]?key|credential|credentials|bearer|x-api-key|session|cookie)$`)

func isSensitiveKey(k string) bool { return sensitiveKeys.MatchString(strings.TrimSpace(k)) }

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
