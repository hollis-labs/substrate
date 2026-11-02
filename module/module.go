// Package module defines the Substrate capability-module contract and registry.
// Modules register themselves from init(); build tags decide what is linked and
// the manifest decides what is started.
package module

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/hollis-labs/substrate/ui"
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

// UIContributor is optionally implemented by a module that contributes UI. The
// builder reads it to generate the frontend manifest; modules that do not
// implement it contribute nothing, and minimal builds are unaffected.
type UIContributor interface {
	UI() ui.Contribution
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

// Lookup returns the registered module with name.
func Lookup(name string) (Module, bool) {
	mu.RLock()
	defer mu.RUnlock()
	m, ok := registry[name]
	return m, ok
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
			_ = r.stopLocked(ctx)
			return fmt.Errorf("substrate: module %q is not linked into this binary", n)
		}
		if err := m.Start(ctx, app); err != nil {
			_ = r.stopLocked(ctx)
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

// RoutesKey is where AddRoutes accumulates the routes modules serve; httphost
// mounts them after every module has started.
const RoutesKey = "http.routes"

// Route is a declared endpoint with the scope it requires and its handler.
// Scope "" means authentication only.
type Route struct {
	Method, Path, Scope string
	Handler             http.Handler
}

// Pattern is the net/http mux pattern for the route.
func (r Route) Pattern() string { return r.Method + " " + r.Path }

// AddRoutes publishes the routes c declares, binding each to its handler by
// "METHOD /path". Every declared route needs a handler and every handler a
// declared route, so a route cannot exist without a scope. "{app}" in a scope
// expands to the app name, "authenticated" means authentication only, and a
// string config value "scope" under the module's name overrides every scope.
func AddRoutes(app *App, c ui.Contribution, handlers map[string]http.Handler) error {
	override := ""
	if cfg, ok := app.Config[c.Module].(map[string]any); ok {
		override, _ = cfg["scope"].(string)
	}
	have := map[string]Route{}
	for _, r := range Routes(app) {
		have[r.Pattern()] = r
	}
	used := map[string]bool{}
	for _, d := range c.Routes {
		key := d.Method + " " + d.Path
		h, ok := handlers[key]
		if !ok {
			return fmt.Errorf("module %s declares route %q but gave no handler", c.Module, key)
		}
		if _, dup := have[key]; dup {
			return fmt.Errorf("module %s: route %q is already served by another module", c.Module, key)
		}
		used[key] = true
		scope := d.Scope
		if override != "" {
			scope = override
		}
		scope = strings.ReplaceAll(scope, "{app}", app.Name)
		if scope == ui.Authenticated {
			scope = ""
		}
		app.Values[RoutesKey] = append(Routes(app), Route{Method: d.Method, Path: d.Path, Scope: scope, Handler: h})
	}
	for k := range handlers {
		if !used[k] {
			return fmt.Errorf("module %s gave a handler for %q, which ui.json does not declare", c.Module, k)
		}
	}
	return nil
}

// Routes returns the routes modules have published so far.
func Routes(app *App) []Route {
	rs, _ := app.Values[RoutesKey].([]Route)
	return rs
}
