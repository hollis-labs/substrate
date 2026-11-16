package genui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/bom"
	"github.com/hollis-labs/substrate/frontend"
	wire "github.com/hollis-labs/substrate/genui"
	"github.com/hollis-labs/substrate/genui/gateway"
	"github.com/hollis-labs/substrate/manifest"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/ui"
)

// Unavailable test ports prove module wiring without creating a fake authority.
type unavailableHost struct{}

func (unavailableHost) Materialize(context.Context, gateway.Scope, gateway.ShowRequest) (gateway.Surface, error) {
	return gateway.Surface{}, gateway.ErrUnavailable
}
func (unavailableHost) Put(context.Context, gateway.Surface) error { return gateway.ErrUnavailable }
func (unavailableHost) Get(context.Context, gateway.Scope, string) (gateway.Surface, error) {
	return gateway.Surface{}, gateway.ErrUnavailable
}
func (unavailableHost) Supersede(context.Context, gateway.Scope, string, string) error {
	return gateway.ErrUnavailable
}
func (unavailableHost) Begin(context.Context, gateway.Key, gateway.Record) (gateway.Record, bool, error) {
	return gateway.Record{}, false, gateway.ErrUnavailable
}
func (unavailableHost) Load(context.Context, gateway.Key) (gateway.Record, error) {
	return gateway.Record{}, gateway.ErrUnavailable
}
func (unavailableHost) CompareAndSwap(context.Context, gateway.Key, uint64, gateway.Record) (gateway.Record, bool, error) {
	return gateway.Record{}, false, gateway.ErrUnavailable
}
func (unavailableHost) WithFence(context.Context, gateway.Request, func(context.Context, gateway.Decision) error) error {
	return gateway.ErrUnavailable
}
func (unavailableHost) Execute(context.Context, gateway.Execution) (wire.Outcome, error) {
	return wire.Outcome{}, gateway.ErrUnavailable
}
func (unavailableHost) Resolve(context.Context, libauth.Principal) (gateway.Scope, error) {
	return gateway.Scope{}, gateway.ErrUnavailable
}

func TestModuleRequiresHostAndMountsScopedHandlersOnly(t *testing.T) {
	UseHost(nil)
	t.Cleanup(func() { UseHost(nil) })
	app := &module.App{Name: "sample", Config: map[string]any{}, Values: map[string]any{}}
	m := &mod{}
	if err := m.Start(t.Context(), app); err == nil {
		t.Fatal("module booted without authority ports")
	}
	if len(module.Routes(app)) != 0 {
		t.Fatal("partial routes after failed start")
	}
	UseHost(func(*module.App) (Host, error) { return Host{}, nil })
	if err := m.Start(t.Context(), app); !errors.Is(err, gateway.ErrUnavailable) {
		t.Fatal("missing ports did not fail closed", err)
	}
	UseHost(func(*module.App) (Host, error) {
		p := unavailableHost{}
		return Host{Ports: gateway.Ports{Catalog: p, Surfaces: p, Journal: p, Policy: p, Executor: p}, Resolver: p}, nil
	})
	if err := m.Start(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	if From(app) == nil {
		t.Fatal("gateway absent")
	}
	for _, route := range module.Routes(app) {
		if route.Public || !strings.HasPrefix(route.Scope, "sample.genui.") {
			t.Fatal("unscoped route", route)
		}
		w := httptest.NewRecorder()
		route.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/genui/actions", nil))
		if w.Code != 401 {
			t.Fatal("off-mode unauthenticated call accepted")
		}
	}
	if errs := ui.Validate(m.UI(), ui.Default); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestOptionalSelectionAndPublishedPackageFence(t *testing.T) {
	b, err := bom.Load("../../bom/bom.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{App: "sample", Variant: "service", Profiles: []string{"minimal"}, Modules: manifest.Modules{Add: []string{"genui"}}}
	mods, err := m.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	joined := "," + strings.Join(mods, ",") + ","
	for _, name := range []string{"genui", "auth", "httphost"} {
		if !strings.Contains(joined, ","+name+",") {
			t.Fatal("missing dependency", name)
		}
	}
	load := func(name string) ([]byte, bool, error) {
		if name == "genui" {
			return uiJSON, true, nil
		}
		return nil, false, nil
	}
	with, err := frontend.Build(m, b, ui.Default, load)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, kit := range with.Kits {
		if kit.Name == "@hollis-labs/genui" {
			found = kit.Version == b.Kits[kit.Name]
		}
	}
	if !found {
		t.Fatal("selected module did not use its BOM package version")
	}
	delete(b.Kits, "@hollis-labs/genui")
	_, err = frontend.Build(m, b, ui.Default, load)
	if err == nil || !strings.Contains(err.Error(), "@hollis-labs/genui is not pinned") {
		t.Fatal("missing package pin did not refuse", err)
	}
	m.Modules.Add = nil
	without, err := frontend.Build(m, b, ui.Default, load)
	if err != nil {
		t.Fatal(err)
	}
	if len(without.Routes) != 0 || len(without.Bindings) != 0 || len(without.Kits) != 0 {
		t.Fatal("unselected module leaked contributions")
	}
}
