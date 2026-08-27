// Package tool defines the guarded capability abstraction: the seam between
// something that wants evidence (an agent, a playbook) and something that can
// obtain it (a collector, an environment adapter).
//
// Governs: specs/001-mvp-core/design-lld.md §2.6, design-hld.md §4.1
package tool

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Type enumerates the JSON-schema types a tool argument may take. The subset is
// deliberate: it is what a model can reliably produce and what a guard can
// reliably check.
type Type string

const (
	TypeString  Type = "string"
	TypeInteger Type = "integer"
	TypeNumber  Type = "number"
	TypeBoolean Type = "boolean"
	TypeArray   Type = "array"
	TypeObject  Type = "object"
)

// Property describes one argument.
type Property struct {
	Type        Type      `json:"type"`
	Description string    `json:"description"`
	Enum        []string  `json:"enum,omitempty"`
	Default     any       `json:"default,omitempty"`
	Minimum     *float64  `json:"minimum,omitempty"`
	Maximum     *float64  `json:"maximum,omitempty"`
	Items       *Property `json:"items,omitempty"`
	Pattern     string    `json:"pattern,omitempty"`
}

// Schema is a tool's argument contract. It is emitted verbatim to model
// providers as a JSON schema and enforced locally before any effect is planned.
type Schema struct {
	Type       Type                `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// NewSchema builds an object schema.
func NewSchema(props map[string]Property, required ...string) Schema {
	return Schema{Type: TypeObject, Properties: props, Required: required}
}

// Float is a helper for bounded numeric properties.
func Float(v float64) *float64 { return &v }

// Validate checks args against the schema and returns a normalised copy with
// defaults applied and numeric types coerced. Models frequently emit "5" where
// an integer is wanted; coercing here is kinder than refusing, and the coercion
// is total and explicit rather than reflective.
func (s Schema) Validate(args map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(args))

	known := map[string]bool{}
	for name := range s.Properties {
		known[name] = true
	}
	unknown := make([]string, 0)
	for k := range args {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, errs.New("MAS-8005", strings.Join(unknown, ", "), "unknown argument")
	}

	for _, name := range s.Required {
		v, ok := args[name]
		if !ok || v == nil || v == "" {
			return nil, errs.New("MAS-8005", name, "required argument is missing")
		}
	}

	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		p := s.Properties[name]
		v, present := args[name]
		if !present || v == nil {
			if p.Default != nil {
				out[name] = p.Default
			}
			continue
		}
		coerced, err := coerce(name, p, v)
		if err != nil {
			return nil, err
		}
		out[name] = coerced
	}
	return out, nil
}

func coerce(name string, p Property, v any) (any, error) {
	reject := func(reason string) error { return errs.New("MAS-8005", name, reason) }

	switch p.Type {
	case TypeString:
		s, ok := v.(string)
		if !ok {
			s = fmt.Sprint(v)
		}
		if len(p.Enum) > 0 && !containsFold(p.Enum, s) {
			return nil, reject("must be one of " + strings.Join(p.Enum, ", "))
		}
		return s, nil

	case TypeInteger:
		n, err := toFloat(v)
		if err != nil {
			return nil, reject("must be an integer")
		}
		if n != float64(int64(n)) {
			return nil, reject("must be a whole number")
		}
		if err := checkBounds(n, p, reject); err != nil {
			return nil, err
		}
		return int(n), nil

	case TypeNumber:
		n, err := toFloat(v)
		if err != nil {
			return nil, reject("must be a number")
		}
		if err := checkBounds(n, p, reject); err != nil {
			return nil, err
		}
		return n, nil

	case TypeBoolean:
		switch t := v.(type) {
		case bool:
			return t, nil
		case string:
			b, err := strconv.ParseBool(t)
			if err != nil {
				return nil, reject("must be a boolean")
			}
			return b, nil
		default:
			return nil, reject("must be a boolean")
		}

	case TypeArray:
		items, ok := v.([]any)
		if !ok {
			if s, isString := v.(string); isString {
				items = []any{s} // a single value where a list was expected
			} else {
				return nil, reject("must be an array")
			}
		}
		out := make([]any, 0, len(items))
		for i, item := range items {
			if p.Items == nil {
				out = append(out, item)
				continue
			}
			c, err := coerce(fmt.Sprintf("%s[%d]", name, i), *p.Items, item)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, nil

	case TypeObject:
		m, ok := v.(map[string]any)
		if !ok {
			return nil, reject("must be an object")
		}
		return m, nil

	default:
		return v, nil
	}
}

func checkBounds(n float64, p Property, reject func(string) error) error {
	if p.Minimum != nil && n < *p.Minimum {
		return reject(fmt.Sprintf("must be at least %g", *p.Minimum))
	}
	if p.Maximum != nil && n > *p.Maximum {
		return reject(fmt.Sprintf("must be at most %g", *p.Maximum))
	}
	return nil
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case int:
		return float64(t), nil
	case int32:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case json_Number:
		return t.Float64()
	case string:
		return strconv.ParseFloat(strings.TrimSpace(t), 64)
	default:
		return 0, fmt.Errorf("not numeric")
	}
}

// json_Number mirrors encoding/json.Number without importing it into the
// signature, so callers may pass either.
type json_Number interface{ Float64() (float64, error) }

func containsFold(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}

// String helpers for tool implementations reading validated arguments.

// Str reads a string argument, returning def when absent.
func Str(args map[string]any, key, def string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

// Int reads an integer argument, returning def when absent.
func Int(args map[string]any, key string, def int) int {
	if v, ok := args[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case float64:
			return int(t)
		}
	}
	return def
}

// Bool reads a boolean argument, returning def when absent.
func Bool(args map[string]any, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// Strings reads a string-array argument.
func Strings(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, fmt.Sprint(item))
		}
		return out
	case string:
		return []string{t}
	}
	return nil
}
