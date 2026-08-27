// Package errs implements the MAS-NNNN error-code registry required by
// Constitution Article V.2: every error that crosses a boundary carries a stable
// code with a bilingual message and a remediation hint.
//
// Governs: specs/001-mvp-core/design-lld.md §2.1
package errs

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Severity classifies how an operator should react to a code.
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
	SeverityInfo  Severity = "info"
)

// Definition is one entry in the registry. MessageEN/MessageZH are fmt templates
// sharing the same verb sequence, so a single argument list renders both.
type Definition struct {
	Code      string   `json:"code"`
	Symbol    string   `json:"symbol"`
	Severity  Severity `json:"severity"`
	MessageEN string   `json:"message_en"`
	MessageZH string   `json:"message_zh"`
	RemedyEN  string   `json:"remedy_en"`
	RemedyZH  string   `json:"remedy_zh"`
}

var codePattern = regexp.MustCompile(`^MAS-[1-9][0-9]{3}$`)

// Error is a coded error. It is the only error type this project surfaces at a
// boundary; CodeOf reports "" for anything else, which the boundary audit test
// treats as a defect.
type Error struct {
	def    Definition
	args   []any
	cause  error
	fields map[string]any
}

// New builds a coded error. It panics on an unregistered code: an unknown code
// is a programming error caught by TestAllCodesRegistered, never a runtime
// condition an operator can act on.
func New(code string, args ...any) *Error {
	def, ok := Lookup(code)
	if !ok {
		panic("errs: unregistered code " + code)
	}
	return &Error{def: def, args: args}
}

// Wrap attaches a code to an existing error, preserving it for errors.Is/As.
func Wrap(err error, code string, args ...any) *Error {
	e := New(code, args...)
	e.cause = err
	return e
}

func (e *Error) Error() string {
	msg := fmt.Sprintf(e.def.MessageEN, e.args...)
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.def.Code, msg, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.def.Code, msg)
}

// Code returns the MAS-NNNN identifier.
func (e *Error) Code() string { return e.def.Code }

// Definition returns the registry entry behind this error.
func (e *Error) Definition() Definition { return e.def }

// Severity reports the registered severity.
func (e *Error) Severity() Severity { return e.def.Severity }

// Message renders the message in the requested language ("zh" or anything else
// for English).
func (e *Error) Message(lang string) string {
	if lang == "zh" {
		return fmt.Sprintf(e.def.MessageZH, e.args...)
	}
	return fmt.Sprintf(e.def.MessageEN, e.args...)
}

// Remedy returns the registered remediation hint in the requested language.
func (e *Error) Remedy(lang string) string {
	if lang == "zh" {
		return e.def.RemedyZH
	}
	return e.def.RemedyEN
}

func (e *Error) Unwrap() error { return e.cause }

// With attaches a structured field carried into logs.
func (e *Error) With(k string, v any) *Error {
	if e.fields == nil {
		e.fields = map[string]any{}
	}
	e.fields[k] = v
	return e
}

// Fields returns the attached structured fields.
func (e *Error) Fields() map[string]any { return e.fields }

// LogAttrs flattens the error into key/value pairs for slog.
func (e *Error) LogAttrs() []any {
	attrs := []any{"code", e.def.Code, "code_symbol", e.def.Symbol}
	for k, v := range e.fields {
		attrs = append(attrs, k, v)
	}
	if e.cause != nil {
		attrs = append(attrs, "cause", e.cause.Error())
	}
	return attrs
}

// CodeOf extracts the code from anywhere in an error chain, or "" if the chain
// carries none.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code()
	}
	return ""
}

// Is reports whether err carries the given code anywhere in its chain.
func Is(err error, code string) bool { return CodeOf(err) == code }

// AsError extracts the coded error from a chain.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Lookup returns the definition for a code.
func Lookup(code string) (Definition, bool) {
	d, ok := registry[code]
	return d, ok
}

// All returns every definition, sorted by code. It backs `mas errcodes` and the
// generated docs/*/error-codes.md.
func All() []Definition {
	out := make([]Definition, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Domain names the allocation block a code belongs to (HLD §7.1).
func Domain(code string) string {
	if len(code) < 5 {
		return "unknown"
	}
	switch code[4] {
	case '1':
		return "config"
	case '2':
		return "llm"
	case '3':
		return "orchestration"
	case '4':
		return "collector"
	case '5':
		return "knowledge"
	case '6':
		return "storage"
	case '7':
		return "interface"
	case '8':
		return "safety"
	case '9':
		return "internal"
	}
	return "unknown"
}

func init() {
	registry = make(map[string]Definition, len(definitions))
	var dup []string
	for _, d := range definitions {
		if !codePattern.MatchString(d.Code) {
			panic("errs: malformed code " + d.Code)
		}
		if _, exists := registry[d.Code]; exists {
			dup = append(dup, d.Code)
		}
		registry[d.Code] = d
	}
	if len(dup) > 0 {
		panic("errs: duplicate codes " + strings.Join(dup, ", "))
	}
}

var registry map[string]Definition
