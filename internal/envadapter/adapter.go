// Package envadapter binds a logical target ("redis-prod") to the concrete place
// it runs, so agents never encode environment specifics.
//
// Governs: specs/001-mvp-core/design-lld.md §2.9, design-hld.md §4.6
package envadapter

import (
	"context"
	"sort"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/internal/tool"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Binding is the resolved location of a target.
type Binding struct {
	Kind      string            `json:"kind"` // "kubernetes" | "local"
	Namespace string            `json:"namespace,omitempty"`
	Instances []core.Instance   `json:"instances,omitempty"`
	Version   string            `json:"version,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Notes     []string          `json:"notes,omitempty"`
}

// Adapter reads one class of environment.
type Adapter interface {
	Name() string
	Resolve(ctx context.Context, t config.TargetConfig) (Binding, error)
	Tools() []tool.Tool
	Probe(ctx context.Context) error
}

// Factory builds an adapter for a configured environment.
type Factory func(name string, cfg config.EnvConfig) (Adapter, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register adds an environment type. Called from adapter package init.
func Register(envType string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	factories[envType] = f
}

// Open builds the adapter for a configured environment.
func Open(name string, cfg config.EnvConfig) (Adapter, error) {
	mu.RLock()
	f, ok := factories[cfg.Type]
	mu.RUnlock()
	if !ok {
		return nil, errs.New("MAS-1003", "envs."+name+".type", "no adapter registered for type "+cfg.Type)
	}
	return f(name, cfg)
}

// Types lists the registered environment types.
func Types() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
