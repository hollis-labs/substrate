// Package capabilities reports which modules this binary linked and started.
// It is part of the agentic profile, not minimal.
package capabilities

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/substrate/module"
)

// Keys under which the report http.Handler and its auth scope are published.
// httphost mounts the handler at GET /api/capabilities and, when the auth module
// is started, maps that route to the scope so enforce mode does not fail it
// closed as unmapped.
const (
	Key      = "capabilities.handler"
	ScopeKey = "capabilities.scope"
)

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "capabilities" }

func (mod) Start(_ context.Context, app *module.App) error {
	// Config under "capabilities": scope, the scope required in enforce mode
	// (default "<app>.read").
	scope := app.Name + ".read"
	if cfg, ok := app.Config["capabilities"].(map[string]any); ok {
		if s, ok := cfg["scope"].(string); ok && s != "" {
			scope = s
		}
	}
	app.Set(ScopeKey, scope)
	app.Set(Key, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app": app.Name, "started": app.Started, "linked": module.Linked(),
		})
	}))
	return nil
}

func (mod) Stop(context.Context) error { return nil }
