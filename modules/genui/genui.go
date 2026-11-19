// Package genui mounts the optional reviewed-pattern gateway on the host's
// existing HTTP server. All authority and persistence are supplied by the app.
package genui

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	"github.com/hollis-labs/substrate/genui/gateway"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/ui"
)

const Key = "genui.gateway"

//go:embed ui.json
var uiJSON []byte

type Host struct {
	Ports    gateway.Ports
	Resolver gateway.ScopeResolver
}
type Factory func(*module.App) (Host, error)

var hooks struct {
	sync.Mutex
	factory Factory
}

// UseHost registers host-owned ports before boot. Passing nil clears the hook.
// No memory journal, permissive policy or confirmation service is defaulted.
func UseHost(factory Factory) { hooks.Lock(); defer hooks.Unlock(); hooks.factory = factory }
func init()                   { module.Register(&mod{}) }

type mod struct{}

func (*mod) Name() string        { return "genui" }
func (*mod) UI() ui.Contribution { return ui.MustParse(uiJSON) }
func (m *mod) Start(_ context.Context, app *module.App) error {
	hooks.Lock()
	factory := hooks.factory
	hooks.Unlock()
	if factory == nil {
		return fmt.Errorf("genui requires host-owned ports: call genui.UseHost before boot")
	}
	host, err := factory(app)
	if err != nil {
		return err
	}
	service, err := gateway.New(host.Ports)
	if err != nil {
		return err
	}
	http, err := gateway.NewHTTP(service, host.Resolver, app.Name+".genui")
	if err != nil {
		return err
	}
	if err = module.AddRoutes(app, m.UI(), http.Handlers()); err != nil {
		return err
	}
	app.Set(Key, service)
	return nil
}
func (*mod) Stop(context.Context) error { return nil }
func From(app *module.App) *gateway.Service {
	v, _ := app.Value(Key)
	s, _ := v.(*gateway.Service)
	return s
}
