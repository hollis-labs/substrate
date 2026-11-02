// Package health is the health module: one /healthz convention with a registry
// of named checks. Other modules add checks; httphost serves the handler.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"

	"github.com/hollis-labs/substrate/module"
)

// Key under which the *Registry is published.
const Key = "health.registry"

// Check reports nil when healthy.
type Check func(context.Context) error

type Registry struct {
	mu     sync.RWMutex
	checks map[string]Check
}

func (r *Registry) Add(name string, c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

// Handler serves {"status":"ok"|"degraded","checks":{name:"ok"|error}}; 503 when degraded.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.RLock()
		names := make([]string, 0, len(r.checks))
		for n := range r.checks {
			names = append(names, n)
		}
		r.mu.RUnlock()
		sort.Strings(names)
		status, code := "ok", http.StatusOK
		res := map[string]string{}
		for _, n := range names {
			r.mu.RLock()
			c := r.checks[n]
			r.mu.RUnlock()
			if err := c(req.Context()); err != nil {
				res[n], status, code = err.Error(), "degraded", http.StatusServiceUnavailable
			} else {
				res[n] = "ok"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "checks": res})
	})
}

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "health" }

func (mod) Start(_ context.Context, app *module.App) error {
	app.Set(Key, &Registry{checks: map[string]Check{}})
	return nil
}

func (mod) Stop(context.Context) error { return nil }

// From returns the registry published by the health module.
func From(app *module.App) *Registry {
	v, _ := app.Value(Key)
	r, _ := v.(*Registry)
	return r
}
