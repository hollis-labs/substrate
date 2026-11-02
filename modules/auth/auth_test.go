package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	_ "github.com/hollis-labs/substrate/modules/capabilities"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
)

func start(t *testing.T, cfg string) *boot.Running {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	return run
}

func TestEnforceGuardsAPIButNotHealthz(t *testing.T) {
	key := "s3cret-key"
	run := start(t, `{"auth":{"mode":"enforce","api_keys":[{"id":"svc/a","hash":"`+libauth.HashKey(key)+`","scopes":["t.read"]}]}}`)
	auth.ScopeTable(run.App).Route("GET", "/api/notes", "t.read").Route("POST", "/api/notes", "t.write")

	api := http.NewServeMux()
	api.HandleFunc("GET /api/notes", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	api.HandleFunc("POST /api/notes", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	h, err := httphost.Handler(run.App, httphost.Options{Routes: api})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, bearer string) int {
		r := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct {
		name, method, path, bearer string
		want                       int
	}{
		{"no token", "GET", "/api/notes", "", 401},
		{"bad token", "GET", "/api/notes", "nope", 401},
		{"scoped read", "GET", "/api/notes", key, 200},
		{"missing scope", "POST", "/api/notes", key, 403},
		{"unmapped route fails closed", "GET", "/api/other", key, 403},
		{"healthz stays open", "GET", "/healthz", "", 200},
	} {
		if got := do(c.method, c.path, c.bearer); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestEnforceWithoutVerifierFailsStart(t *testing.T) {
	_, err := boot.Start(context.Background(), "t", []string{"health", "auth"}, `{"auth":{"mode":"enforce"}}`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOffModeAllowsAndBindIsValidated(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	run := start(t, `{}`)
	if auth.Guard(run.App).Mode() != libauth.ModeOff {
		t.Fatal("default mode should be off")
	}
	if err := auth.ValidateBind(run.App, ":8080"); err == nil {
		t.Error("off mode on a wildcard bind must be refused")
	}
	if err := auth.ValidateBind(run.App, "127.0.0.1:8080"); err != nil {
		t.Error(err)
	}
}

func TestCapabilitiesIsMappedInEnforceMode(t *testing.T) {
	read, other := "read-key", "other-key"
	cfg := `{"auth":{"mode":"enforce","api_keys":[` +
		`{"id":"svc/r","hash":"` + libauth.HashKey(read) + `","scopes":["t.read"]},` +
		`{"id":"svc/o","hash":"` + libauth.HashKey(other) + `","scopes":["t.write"]}]}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "capabilities", "auth"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	do := func(bearer string) int {
		r := httptest.NewRequest("GET", "/api/capabilities", nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// Default scope is "<app>.read" (app is "t").
	for _, c := range []struct {
		name, bearer string
		want         int
	}{
		{"no token", "", 401},
		{"wrong scope", other, 403},
		{"read scope", read, 200},
	} {
		if got := do(c.bearer); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestCapabilitiesScopeIsConfigurable(t *testing.T) {
	key := "k"
	cfg := `{"capabilities":{"scope":"ops.inspect"},"auth":{"mode":"enforce","api_keys":[` +
		`{"id":"svc/o","hash":"` + libauth.HashKey(key) + `","scopes":["ops.inspect"]}]}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "capabilities", "auth"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	h, _ := httphost.Handler(run.App, httphost.Options{})
	r := httptest.NewRequest("GET", "/api/capabilities", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("got %d want 200", w.Code)
	}
}
