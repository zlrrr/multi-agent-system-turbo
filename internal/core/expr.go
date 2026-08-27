// Playbook-expression scanning, shared by the rule engine that evaluates
// expressions and the pack resolver that has to know which slots one reads.
//
// It lives here, in the domain package, because there must be exactly one of
// it. A second copy would drift, and the first defect it would reintroduce is
// already on record: before feature 002 the scanner read words inside regex
// literals as slot names, so every log-pattern check in every pack was silently
// skipped and recorded as a harmless-looking gap.
//
// Governs: specs/001-mvp-core/design-lld.md §2.12,
// specs/007-version-scoped-rules/design-lld.md §5.3
package core

import "strings"

// Identifiers returns the bare identifiers an expression reads, in order,
// skipping quoted string literals and the field half of a `a.b` selector.
//
// `up.latest < 1` yields ["up"]. `countMatching(logs.lines, 'etcd|latest')`
// yields ["countMatching", "logs"] — never "etcd" or "latest", which are text
// inside a literal and not slots at all.
func Identifiers(s string) []string {
	var out []string
	var cur strings.Builder
	prevWasDot := false
	flush := func() {
		if cur.Len() > 0 {
			if !prevWasDot {
				out = append(out, cur.String())
			}
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' || c == '"' || c == '`' {
			flush()
			prevWasDot = false
			// Skip to the closing quote. An unterminated literal cannot compile,
			// so consuming the remainder costs nothing.
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' && c != '`' {
					i++
				}
			}
			continue
		}
		switch {
		case c == '_' || c == '.' && cur.Len() > 0:
			if c == '.' {
				flush()
				prevWasDot = true
				continue
			}
			cur.WriteByte(c)
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			cur.WriteByte(c)
		case c >= '0' && c <= '9':
			if cur.Len() > 0 {
				cur.WriteByte(c)
			}
		default:
			flush()
			prevWasDot = false
		}
	}
	flush()
	return out
}

// exprBuiltins are the names an expression may use that are not slots.
var exprBuiltins = map[string]bool{
	"true": true, "false": true, "nil": true, "and": true, "or": true, "not": true,
	"in": true, "matches": true, "contains": true, "startsWith": true, "endsWith": true,
	"len": true, "all": true, "any": true, "none": true, "one": true, "filter": true,
	"map": true, "count": true, "sum": true, "avg": true, "min": true, "max": true,
	"abs": true, "int": true, "float": true, "string": true, "lower": true, "upper": true,
	"countMatching": true, "ratio": true, "pct": true, "isNaN": true, "finite": true,
}

// IsExprBuiltin reports whether an identifier is a language or helper name
// rather than a slot the playbook bound.
func IsExprBuiltin(ident string) bool { return exprBuiltins[ident] }
