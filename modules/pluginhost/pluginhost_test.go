package pluginhost_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	libhost "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/pluginhost"
)

func helloSpec(t *testing.T) libhost.Spec {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hello")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/hello").CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}
	dir := t.TempDir()
	return libhost.Spec{
		ID: "hello", ExpectedID: "hello", Command: bin, Dir: dir, Env: []string{},
		Init: subprocess.InitParams{
			PluginDir: dir, DataDir: dir, CacheDir: dir, Config: map[string]string{}, LogLevel: "info",
			HostInfo:           subprocess.HostInfo{Protocol: 2, Version: "1.0.0"},
			CapabilityContract: 1,
			Incarnation:        capability.RuntimeIdentity{HostInstance: "test", OwnerID: "hello", OwnerGeneration: 1},
			Grants:             capability.GrantSet{},
		},
	}
}

func TestStartsPluginReportsHealthAndStopsIt(t *testing.T) {
	pluginhost.UseSpecs(helloSpec(t))
	defer pluginhost.UseSpecs()
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "pluginhost"}, "")
	if err != nil {
		t.Fatal(err)
	}
	inv := pluginhost.Inventory(run.App)
	if _, ok := inv.Client("hello"); !ok {
		t.Fatal("plugin not running after Start")
	}
	if rep := health.From(run.App).Report(ctx); rep.Status != "ok" {
		t.Fatalf("health: %+v", rep)
	}
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := inv.Client("hello"); ok {
		t.Fatal("plugin still running after Stop")
	}
}

func TestRoutesMountedUnderAPI(t *testing.T) {
	pluginhost.UseSpecs(helloSpec(t))
	defer pluginhost.UseSpecs()
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost", "pluginhost"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	app := http.NewServeMux()
	app.HandleFunc("/api/plugins/console/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app-console")) })
	h, err := httphost.Handler(run.App, httphost.Options{Routes: app})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	// A registered id is forwarded to its plugin (the hello plugin serves no
	// such route, so it answers, but not with the app's body).
	if w := get("/api/plugins/hello/x"); w.Body.String() == "app-console" {
		t.Fatalf("registered plugin id reached the app handler: %d %q", w.Code, w.Body)
	}
	// An id no plugin registered belongs to the app.
	if w := get("/api/plugins/console/v1/x"); w.Code != 200 || w.Body.String() != "app-console" {
		t.Fatalf("app route under /api/plugins/: %d %q", w.Code, w.Body)
	}
}

func TestBadSpecFailsStart(t *testing.T) {
	pluginhost.UseSpecs(libhost.Spec{ID: "ghost", Command: filepath.Join(os.TempDir(), "does-not-exist-xyz")})
	defer pluginhost.UseSpecs()
	if _, err := boot.Start(context.Background(), "t", []string{"health", "pluginhost"}, ""); err == nil {
		t.Fatal("expected start failure for a plugin that cannot spawn")
	}
}

func TestPluginRoutesAreScopedAndTheAppCanBeFiner(t *testing.T) {
	pluginhost.UseSpecs(helloSpec(t))
	defer pluginhost.UseSpecs()
	broad, narrow := "broad-key", "narrow-key"
	cfg := `{"auth":{"mode":"enforce","api_keys":[` +
		`{"id":"svc/b","hash":"` + libauth.HashKey(broad) + `","scopes":["t.plugins"]},` +
		`{"id":"svc/n","hash":"` + libauth.HashKey(narrow) + `","scopes":["t.hello"]}]}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost", "pluginhost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	// The app maps a finer scope for one plugin before the host builds its
	// handler; it is consulted before the module's broad "<app>.plugins".
	auth.ScopeTable(run.App).Route("*", "/api/plugins/hello/*", "t.hello")
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	status := func(path, key string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", path, nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct {
		name, path, key string
		want            int
	}{
		{"no token", "/api/plugins/hello/x", "", 401},
		{"finer app scope reaches its plugin", "/api/plugins/hello/x", narrow, 502}, // admitted and forwarded; the plugin has no such route,
		{"broad scope no longer reaches the finer plugin", "/api/plugins/hello/x", broad, 403},
	} {
		if got := status(c.path, c.key); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}
