// Package capabilities reports which modules this binary linked and started.
// It is part of the agentic profile, not minimal.
package capabilities

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/substrate/module"
)

// Key under which the report http.Handler is published (mounted by httphost).
const Key = "capabilities.handler"

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "capabilities" }

func (mod) Start(_ context.Context, app *module.App) error {
	app.Set(Key, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app": app.Name, "started": app.Started, "linked": module.Linked(),
		})
	}))
	return nil
}

func (mod) Stop(context.Context) error { return nil }
