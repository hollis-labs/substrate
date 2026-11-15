// Package pluginhost is the plugin host module: libs app-host's plugin
// Inventory over plugin-host subprocess plugins. The app owns what libs leaves
// to it (discovery, the exact reviewed Specs, credentials, grants, restart
// policy); this module starts them all-or-nothing, stops them in reverse, adds
// a health check, and exposes the forwarding routes.
//
// Register the app's reviewed Specs before boot.Start:
//
//	pluginhost.UseSpecs(spec1, spec2)
//
// The forwarding routes (/api/plugins/{id}/..., any method) are declared one per
// registered Spec and mounted by httphost under /api/, so the auth guard covers
// them. Other paths under /api/plugins/ are not claimed: an app may serve its
// own handlers there through httphost.Options.Routes.
// Their scope is "<app>.plugins" (config pluginhost.scope replaces it). An app
// that wants finer scopes per plugin maps its own routes on auth.ScopeTable
// after boot.Start; those are consulted first.
package pluginhost

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/hollis-labs/libs/app/app-host/plugins"
	libhost "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-host"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/ui"
)

// UI declares one forwarding route per registered Spec, "* /api/plugins/<id>/".
// The module does not claim the rest of /api/plugins/, so an app can serve its
// own handlers under the ids it does not register as plugins.
func (*mod) UI() ui.Contribution { return routesFor(registered()) }

func registered() []libhost.Spec {
	mu.Lock()
	defer mu.Unlock()
	return append([]libhost.Spec(nil), specs...)
}

func routesFor(list []libhost.Spec) ui.Contribution {
	c := ui.Contribution{Module: "pluginhost"}
	seen := map[string]bool{}
	for _, s := range list {
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		c.Routes = append(c.Routes, ui.Route{Method: "*", Path: routePath(s.ID), Scope: "{app}.plugins"})
	}
	return c
}

func routePath(id string) string { return "/api/plugins/" + id + "/" }

// InventoryKey is where the *plugins.Inventory is published.
const InventoryKey = "pluginhost.inventory"

var (
	mu    sync.Mutex
	specs []libhost.Spec
)

// UseSpecs registers the app's reviewed plugin Specs. Call it before
// boot.Start. Calling it again replaces the earlier list.
func UseSpecs(s ...libhost.Spec) {
	mu.Lock()
	defer mu.Unlock()
	specs = append([]libhost.Spec(nil), s...)
}

func init() { module.Register(&mod{}) }

type mod struct{ inv *plugins.Inventory }

func (*mod) Name() string { return "pluginhost" }

func (m *mod) Start(ctx context.Context, app *module.App) error {
	list := registered()
	inv := plugins.NewInventory(list)
	if err := inv.Start(ctx); err != nil {
		return fmt.Errorf("start plugins: %w", err)
	}
	m.inv = inv
	app.Set(InventoryKey, inv)
	handlers := map[string]http.Handler{}
	for _, sp := range list {
		handlers["* "+routePath(sp.ID)] = inv.APIRoutes()
	}
	if err := module.AddRoutes(app, routesFor(list), handlers); err != nil {
		_ = inv.Stop(ctx)
		m.inv = nil
		return err
	}
	if reg := health.From(app); reg != nil {
		reg.Add("plugins", func(context.Context) error {
			for _, s := range list {
				if _, ok := inv.Client(s.ID); !ok {
					return fmt.Errorf("plugin %q is not running", s.ID)
				}
			}
			return nil
		})
	}
	return nil
}

func (m *mod) Stop(ctx context.Context) error {
	if m.inv == nil {
		return nil
	}
	err := m.inv.Stop(ctx)
	m.inv = nil
	return err
}

// Inventory returns the running inventory, or nil.
func Inventory(app *module.App) *plugins.Inventory {
	v, _ := app.Value(InventoryKey)
	i, _ := v.(*plugins.Inventory)
	return i
}
