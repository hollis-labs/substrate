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

// Report is the health convention: status "ok" or "degraded", one entry per check.
type Report struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Report runs every check and returns the result.
func (r *Registry) Report(ctx context.Context) Report {
	r.mu.RLock()
	names := make([]string, 0, len(r.checks))
	for n := range r.checks {
		names = append(names, n)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	rep := Report{Status: "ok", Checks: map[string]string{}}
	for _, n := range names {
		r.mu.RLock()
		c := r.checks[n]
		r.mu.RUnlock()
		if err := c(ctx); err != nil {
			rep.Checks[n], rep.Status = err.Error(), "degraded"
		} else {
			rep.Checks[n] = "ok"
		}
	}
	return rep
}

// Handler serves the Report as JSON; 503 when degraded.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rep := r.Report(req.Context())
		code := http.StatusOK
		if rep.Status != "ok" {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(rep)
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
