// Package capabilities reports which modules this binary linked and started.
// It is part of the agentic profile, not minimal.
package capabilities

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/ui"
)

//go:embed ui.json
var uiJSON []byte

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "capabilities" }

// UI declares GET /api/capabilities, scope "<app>.read" unless the app sets
// capabilities.scope in config. httphost mounts it and maps the scope when auth
// is started, so enforce mode does not fail it closed as unmapped.
func (mod) UI() ui.Contribution { return ui.MustParse(uiJSON) }

func (m mod) Start(_ context.Context, app *module.App) error {
	return module.AddRoutes(app, m.UI(), map[string]http.Handler{
		"GET /api/capabilities": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"app": app.Name, "started": app.Started, "linked": module.Linked(),
			})
		}),
	})
}

func (mod) Stop(context.Context) error { return nil }
