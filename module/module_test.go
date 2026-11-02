package module

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/ui"
)

type probe struct {
	name    string
	log     *[]string
	failOn  bool
	stopErr error
}

func (p probe) Name() string { return p.name }
func (p probe) Start(context.Context, *App) error {
	*p.log = append(*p.log, "start:"+p.name)
	if p.failOn {
		return errors.New("boom")
	}
	return nil
}
func (p probe) Stop(context.Context) error {
	*p.log = append(*p.log, "stop:"+p.name)
	return p.stopErr
}

func newApp() *App { return &App{Config: map[string]any{}, Values: map[string]any{}} }

// withRegistry swaps in a private registry for one test.
func withRegistry(t *testing.T, ms ...Module) {
	t.Helper()
	mu.Lock()
	saved := registry
	registry = map[string]Module{}
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); registry = saved; mu.Unlock() })
	for _, m := range ms {
		Register(m)
	}
}

func TestStartsInOrderStopsInReverse(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "a", log: &log}, probe{name: "b", log: &log}, probe{name: "c", log: &log})
	var rt Runtime
	app := newApp()
	if err := rt.Start(context.Background(), app, []string{"b", "a", "c"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(log, ","), "start:b,start:a,start:c,stop:c,stop:a,stop:b"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
	if got := strings.Join(app.Started, ","); got != "b,a,c" {
		t.Fatalf("Started = %s", got)
	}
}

func TestFailedStartStopsWhatStarted(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "a", log: &log}, probe{name: "b", log: &log, failOn: true})
	var rt Runtime
	if err := rt.Start(context.Background(), newApp(), []string{"a", "b"}); err == nil {
		t.Fatal("want start error")
	}
	if got, want := strings.Join(log, ","), "start:a,start:b,stop:a"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
}

func TestUnlinkedModuleIsAnError(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "a", log: &log})
	var rt Runtime
	if err := rt.Start(context.Background(), newApp(), []string{"a", "nope"}); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(log, ","); got != "start:a,stop:a" {
		t.Fatalf("a not unwound: %s", got)
	}
}

func TestStopReportsFirstErrorAndStopsAll(t *testing.T) {
	var log []string
	first := errors.New("first")
	withRegistry(t, probe{name: "a", log: &log, stopErr: errors.New("later")}, probe{name: "b", log: &log, stopErr: first})
	var rt Runtime
	if err := rt.Start(context.Background(), newApp(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(context.Background()); !errors.Is(err, first) {
		t.Fatalf("err = %v, want first error (b stops first)", err)
	}
	if got := strings.Join(log, ","); got != "start:a,start:b,stop:b,stop:a" {
		t.Fatalf("a not stopped after b failed: %s", got)
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "a", log: &log})
	defer func() {
		if recover() == nil {
			t.Fatal("want panic")
		}
	}()
	Register(probe{name: "a", log: &log})
}

func contribution(routes ...ui.Route) ui.Contribution {
	return ui.Contribution{Module: "demo", Routes: routes}
}

func h(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(name)) })
}

func TestAddRoutesExpandsAndRegisters(t *testing.T) {
	app := newApp()
	app.Name = "hello"
	c := contribution(
		ui.Route{Method: "GET", Path: "/api/a", Scope: "{app}.read"},
		ui.Route{Method: "GET", Path: "/api/b", Scope: ui.Authenticated},
	)
	if err := AddRoutes(app, c, map[string]http.Handler{"GET /api/a": h("a"), "GET /api/b": h("b")}); err != nil {
		t.Fatal(err)
	}
	rs := Routes(app)
	if len(rs) != 2 || rs[0].Scope != "hello.read" || rs[1].Scope != "" {
		t.Fatalf("routes = %+v", rs)
	}
	if rs[0].Pattern() != "GET /api/a" {
		t.Errorf("pattern %q", rs[0].Pattern())
	}
}

func TestAddRoutesConfigOverridesScope(t *testing.T) {
	app := newApp()
	app.Config["demo"] = map[string]any{"scope": "ops.inspect"}
	c := contribution(ui.Route{Method: "GET", Path: "/api/a", Scope: "{app}.read"})
	if err := AddRoutes(app, c, map[string]http.Handler{"GET /api/a": h("a")}); err != nil {
		t.Fatal(err)
	}
	if got := Routes(app)[0].Scope; got != "ops.inspect" {
		t.Fatalf("scope %q", got)
	}
}

func TestAddRoutesRefusesAMismatchBetweenDeclarationAndHandlers(t *testing.T) {
	c := contribution(ui.Route{Method: "GET", Path: "/api/a", Scope: "x"})
	if err := AddRoutes(newApp(), c, map[string]http.Handler{}); err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Errorf("declared route without a handler: %v", err)
	}
	if err := AddRoutes(newApp(), c, map[string]http.Handler{"GET /api/a": h("a"), "GET /api/z": h("z")}); err == nil || !strings.Contains(err.Error(), "does not declare") {
		t.Errorf("handler without a declaration: %v", err)
	}
	app := newApp()
	if err := AddRoutes(app, c, map[string]http.Handler{"GET /api/a": h("a")}); err != nil {
		t.Fatal(err)
	}
	if err := AddRoutes(app, c, map[string]http.Handler{"GET /api/a": h("a")}); err == nil || !strings.Contains(err.Error(), "already served") {
		t.Errorf("duplicate route across modules: %v", err)
	}
}
