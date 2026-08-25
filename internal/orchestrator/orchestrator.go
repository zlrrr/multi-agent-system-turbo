// Package orchestrator holds the interchangeable agent topologies.
//
// The topology is the *only* thing that varies between runs of the same case:
// identical state, identical tools, identical deterministic findings. That is
// what makes a topology comparison an experiment rather than an anecdote
// (project goal G7.3).
//
// Governs: specs/001-mvp-core/design-lld.md §2.15, design-hld.md §4.4
package orchestrator

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Description is how a topology explains itself to an operator choosing between
// five of them.
//
// Avoid is required, not decorative. A tool that ships five architectures and
// recommends all of them has told the operator nothing; making each topology
// state what it is bad at is the only thing that turns a list into a choice
// (design-lld.md §2).
type Description struct {
	Summary core.Text // the control flow, in one or two sentences
	Cost    core.Text // the cost profile, stated plainly
	Choose  core.Text // when to prefer it
	Avoid   core.Text // when not to
}

// In renders the description in the operator's language.
func (d Description) In(lang string) string {
	var b strings.Builder
	b.WriteString(d.Summary.In(lang))
	for _, part := range []struct {
		label core.Text
		body  core.Text
	}{
		{core.Text{EN: "Cost", ZH: "成本"}, d.Cost},
		{core.Text{EN: "Choose it when", ZH: "何时选它"}, d.Choose},
		{core.Text{EN: "Avoid it when", ZH: "何时不选"}, d.Avoid},
	} {
		if strings.TrimSpace(part.body.In(lang)) == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(part.label.In(lang))
		b.WriteString(": ")
		b.WriteString(part.body.In(lang))
	}
	return b.String()
}

// Complete reports whether every field carries both languages. The conformance
// contract requires it (FR-010).
func (d Description) Complete() bool {
	return d.Summary.Complete() && d.Cost.Complete() && d.Choose.Complete() && d.Avoid.Complete()
}

// Orchestrator runs one topology over a prepared state.
type Orchestrator interface {
	Name() string
	Describe(lang string) string
	Run(ctx context.Context, s *agent.State) error
}

// Factory builds a topology.
type Factory func() (Orchestrator, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
	describe  = map[string]Description{}
)

// Register adds a topology. Duplicate registration is a programming error and
// panics at init, where it is impossible to miss.
func Register(name string, d Description, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[name]; exists {
		panic("orchestrator: duplicate topology " + name)
	}
	factories[name] = f
	describe[name] = d
}

// Open builds a topology by name.
func Open(name string) (Orchestrator, error) {
	mu.RLock()
	f, ok := factories[name]
	mu.RUnlock()
	if !ok {
		return nil, errs.New("MAS-3001", name)
	}
	return f()
}

// Names lists the registered topologies, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Descriptions renders each topology's description in one language, for
// `mas topologies`.
func Descriptions(lang string) map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]string, len(describe))
	for k, v := range describe {
		out[k] = v.In(lang)
	}
	return out
}

// Details returns the structured descriptions, for the API and for the
// conformance contract.
func Details() map[string]Description {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]Description, len(describe))
	for k, v := range describe {
		out[k] = v
	}
	return out
}
