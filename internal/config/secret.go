package config

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Secret is a configuration value that must never be printed, logged or
// serialised (Constitution Art. IV.4). The plaintext is only reachable through
// Reveal, which resolves indirection references late.
//
// Supported forms:
//
//	plain-value            used as-is
//	${env:VAR_NAME}        read from the environment at Reveal time
//	${file:/path/to/file}  read from disk at Reveal time, trailing newline trimmed
type Secret string

const redacted = "***"

// String satisfies fmt.Stringer so %s and %v never leak the value.
func (Secret) String() string { return redacted }

// GoString satisfies %#v.
func (Secret) GoString() string { return `"` + redacted + `"` }

// MarshalJSON keeps secrets out of API responses and run records.
func (Secret) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// MarshalYAML keeps secrets out of `mas config dump`.
func (Secret) MarshalYAML() (any, error) { return redacted, nil }

// IsZero reports whether the secret is unset.
func (s Secret) IsZero() bool { return strings.TrimSpace(string(s)) == "" }

// IsReference reports whether the secret indirects to the environment or a file.
func (s Secret) IsReference() bool {
	v := string(s)
	return strings.HasPrefix(v, "${env:") || strings.HasPrefix(v, "${file:")
}

// Reveal resolves the secret to its plaintext. Callers must pass the result
// straight to the consuming client and never store, log or embed it.
func (s Secret) Reveal() (string, error) {
	v := strings.TrimSpace(string(s))
	switch {
	case strings.HasPrefix(v, "${env:") && strings.HasSuffix(v, "}"):
		name := v[len("${env:") : len(v)-1]
		got, ok := os.LookupEnv(name)
		if !ok {
			return "", errs.New("MAS-1006", v)
		}
		return got, nil
	case strings.HasPrefix(v, "${file:") && strings.HasSuffix(v, "}"):
		path := v[len("${file:") : len(v)-1]
		b, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
		if err != nil {
			return "", errs.Wrap(err, "MAS-1006", v)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	default:
		return v, nil
	}
}
