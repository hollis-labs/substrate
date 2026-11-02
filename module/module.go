// Package module defines the Substrate capability-module contract and registry.
// Modules register themselves from init(); build tags decide what is linked and
// the manifest decides what is started.
package module

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Module is one optional capability.
type Module interface {
	// Name is the catalogue name, e.g. "config".
	Name() string
	// Start wires the module into the app. Modules never own identity, policy,
	// data or listeners; they receive them through App.
	Start(ctx context.Context, app *App) error
	// Stop releases anything Start acquired.
	Stop(ctx context.Context) error
}

// App is the shared surface modules wire into.
type App struct {
	Name    string
	Config  map[string]any // per-module config, keyed by module name
	Values  map[string]any // inter-module values, keyed by "<module>.<key>"
	Started []string
}

// Value returns an inter-module value.
func (a *App) Value(key string) (any, bool) { v, ok := a.Values[key]; return v, ok }

// Set publishes an inter-module value.
func (a *App) Set(key string, v any) { a.Values[key] = v }

var (
	mu       sync.RWMutex
	registry = map[string]Module{}
)

// Register adds a module; duplicate names panic (programmer error).
func Register(m Module) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[m.Name()]; dup {
		panic("substrate: duplicate module " + m.Name())
	}
	registry[m.Name()] = m
}

// Linked returns the sorted names of modules compiled into this binary.
func Linked() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Runtime starts the named modules in order and stops them in reverse.
type Runtime struct{ started []Module }

// Start starts names in order. A name that is not linked is an error.
func (r *Runtime) Start(ctx context.Context, app *App, names []string) error {
	mu.RLock()
	defer mu.RUnlock()
	for _, n := range names {
		m, ok := registry[n]
		if !ok {
			r.stopLocked(ctx)
			return fmt.Errorf("substrate: module %q is not linked into this binary", n)
		}
		if err := m.Start(ctx, app); err != nil {
			r.stopLocked(ctx)
			return fmt.Errorf("substrate: start %s: %w", n, err)
		}
		r.started = append(r.started, m)
		app.Started = append(app.Started, n)
	}
	return nil
}

// Stop stops started modules in reverse order.
func (r *Runtime) Stop(ctx context.Context) error {
	mu.RLock()
	defer mu.RUnlock()
	return r.stopLocked(ctx)
}

func (r *Runtime) stopLocked(ctx context.Context) error {
	var first error
	for i := len(r.started) - 1; i >= 0; i-- {
		if err := r.started[i].Stop(ctx); err != nil && first == nil {
			first = err
		}
	}
	r.started = nil
	return first
}
