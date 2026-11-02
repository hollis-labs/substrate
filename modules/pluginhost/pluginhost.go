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
// The routes (/api/plugins/{id}/...) are mounted by httphost under /api/, so
// the auth guard covers them. They are unmapped in the scope table, so in
// enforce mode the app maps the scopes it wants and everything else is denied.
package pluginhost

import (
	"context"
	"fmt"
	"sync"

	"github.com/hollis-labs/libs/app/app-host/plugins"
	libhost "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-host"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/health"
)

// Keys under which the inventory and routes handler are published. httphost
// reads RoutesKey by name, so it does not import this package.
const (
	InventoryKey = "pluginhost.inventory"
	RoutesKey    = "pluginhost.routes"
)

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
	mu.Lock()
	list := append([]libhost.Spec(nil), specs...)
	mu.Unlock()

	inv := plugins.NewInventory(list)
	if err := inv.Start(ctx); err != nil {
		return fmt.Errorf("start plugins: %w", err)
	}
	m.inv = inv
	app.Set(InventoryKey, inv)
	app.Set(RoutesKey, inv.APIRoutes())
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
