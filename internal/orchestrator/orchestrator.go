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
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/agent"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Orchestrator runs one topology over a prepared state.
type Orchestrator interface {
	Name() string
	Describe() string
	Run(ctx context.Context, s *agent.State) error
}

// Factory builds a topology.
type Factory func() (Orchestrator, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
	describe  = map[string]string{}
)

// Register adds a topology. Duplicate registration is a programming error and
// panics at init, where it is impossible to miss.
func Register(name, description string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[name]; exists {
		panic("orchestrator: duplicate topology " + name)
	}
	factories[name] = f
	describe[name] = description
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

// Descriptions returns each topology with its description, for `mas topologies`.
func Descriptions() map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]string, len(describe))
	for k, v := range describe {
		out[k] = v
	}
	return out
}
