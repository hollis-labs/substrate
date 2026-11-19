package pluginhost_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	libhost "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/pluginhost"
)

func helloSpec(t *testing.T) libhost.Spec {
	t.Helper()
	return helloSpecs(t, "hello")[0]
}

// helloSpecs builds the test plugin once and returns one Spec per id; the
// plugin answers Init with the id it is given.
func helloSpecs(t *testing.T, ids ...string) []libhost.Spec {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hello")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/hello").CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}
	var out []libhost.Spec
	for _, id := range ids {
		dir := t.TempDir()
		out = append(out, libhost.Spec{
			ID: id, ExpectedID: id, Command: bin, Dir: dir, Env: []string{},
			Init: subprocess.InitParams{
				PluginDir: dir, DataDir: dir, CacheDir: dir, Config: map[string]string{"id": id}, LogLevel: "info",
				HostInfo:           subprocess.HostInfo{Protocol: 2, Version: "1.0.0"},
				CapabilityContract: 1,
				Incarnation:        capability.RuntimeIdentity{HostInstance: "test", OwnerID: id, OwnerGeneration: 1},
				Grants:             capability.GrantSet{},
			},
		})
	}
	return out
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
	// A registered id is forwarded to its plugin; the hello plugin serves no
	// such route, so the forwarder answers 502, not the app.
	if w := get("/api/plugins/hello/x"); w.Code != 502 || w.Body.String() == "app-console" {
		t.Fatalf("registered plugin id: %d %q (want 502 from the forwarder)", w.Code, w.Body)
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

// In enforce mode the guard covers the plugin routes and the app's own routes
// under /api/plugins/ alike; a broad plugins scope reaches neither an app
// route nor an id no plugin registered, and a finer scope stays on its plugin.
func TestPluginRoutesAreScopedAndTheAppCanBeFiner(t *testing.T) {
	pluginhost.UseSpecs(helloSpecs(t, "hello", "other")...)
	defer pluginhost.UseSpecs()
	broad, narrow, console := "broad-key", "narrow-key", "console-key"
	cfg := `{"auth":{"mode":"enforce","api_keys":[` +
		`{"id":"svc/b","hash":"` + libauth.HashKey(broad) + `","scopes":["t.plugins"]},` +
		`{"id":"svc/n","hash":"` + libauth.HashKey(narrow) + `","scopes":["t.hello"]},` +
		`{"id":"svc/c","hash":"` + libauth.HashKey(console) + `","scopes":["t.console"]}]}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost", "pluginhost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	// The app maps a finer scope for one plugin before the host builds its
	// handler; it is consulted before the module's broad "<app>.plugins".
	auth.ScopeTable(run.App).Route("*", "/api/plugins/hello/*", "t.hello")
	// The app serves its own handlers under /api/plugins/: console is mapped,
	// unmapped is not (the guard must fail it closed).
	auth.ScopeTable(run.App).Route("*", "/api/plugins/console/*", "t.console")
	app := http.NewServeMux()
	app.HandleFunc("/api/plugins/console/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app-console")) })
	app.HandleFunc("/api/plugins/unmapped/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app-unmapped")) })
	h, err := httphost.Handler(run.App, httphost.Options{Routes: app})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, path, key string
		want            int
		code            string // flat error code for a refusal
	}{
		{"plugin: no token", "/api/plugins/hello/x", "", 401, "unauthorized"},
		{"plugin: finer app scope reaches its plugin", "/api/plugins/hello/x", narrow, 502, ""}, // admitted and forwarded; the plugin has no such route
		{"plugin: broad scope no longer reaches the finer plugin", "/api/plugins/hello/x", broad, 403, "scope_required"},
		{"plugin: broad scope reaches the other plugin", "/api/plugins/other/x", broad, 502, ""},
		{"plugin: finer scope does not reach the other plugin", "/api/plugins/other/x", narrow, 403, "scope_required"},
		{"unknown id: broad scope is not admitted", "/api/plugins/nope/x", broad, 403, "route_not_mapped"},
		{"app route: no token", "/api/plugins/console/v1/x", "", 401, "unauthorized"},
		{"app route: its scope reaches it", "/api/plugins/console/v1/x", console, 200, ""},
		{"app route: broad plugins scope does not reach it", "/api/plugins/console/v1/x", broad, 403, "scope_required"},
		{"app route: plugin scope does not reach it", "/api/plugins/console/v1/x", narrow, 403, "scope_required"},
		{"app route: unmapped fails closed", "/api/plugins/unmapped/x", broad, 403, "route_not_mapped"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", c.path, nil)
		if c.key != "" {
			r.Header.Set("Authorization", "Bearer "+c.key)
		}
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: got %d %q want %d", c.name, w.Code, w.Body, c.want)
			continue
		}
		if c.code != "" {
			var b struct{ Code string }
			if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.Code != c.code {
				t.Errorf("%s: body %q, want code %q", c.name, w.Body, c.code)
			}
		}
		if strings.HasPrefix(w.Body.String(), "app-") && c.want != 200 {
			t.Errorf("%s: reached the app handler", c.name)
		}
	}
}

func TestStartRejectsIDsThatAreNotOnePlainSegment(t *testing.T) {
	defer pluginhost.UseSpecs()
	bad := []string{"", "a b", "{x}", "a/b", "..", ".", "-x", "a\tb", "a?b", "a%2fb"}
	// The test plugin answers Init with whatever id it is given, so without the
	// check each of these would start and be mounted: "a b" panics in
	// httphost.Handler, "{x}" claims all of /api/plugins/ again.
	for i, sp := range helloSpecs(t, bad...) {
		id := bad[i]
		pluginhost.UseSpecs(sp)
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("id %q: panic %v", id, p)
				}
			}()
			run, err := boot.Start(context.Background(), "t", []string{"health", "httphost", "pluginhost"}, "")
			if err == nil {
				defer func() { _ = run.Stop(context.Background()) }()
				_, _ = httphost.Handler(run.App, httphost.Options{})
			}
			if err == nil || !strings.Contains(err.Error(), "not a single plain path segment") {
				t.Errorf("id %q: got %v, want an id error", id, err)
			}
		}()
	}
}

func TestUIReportsTheMountedList(t *testing.T) {
	m, ok := module.Lookup("pluginhost")
	if !ok {
		t.Fatal("pluginhost not registered")
	}
	ui := m.(module.UIContributor)
	paths := func() []string {
		var out []string
		for _, r := range ui.UI().Routes {
			out = append(out, r.Path)
		}
		return out
	}
	pluginhost.UseSpecs(helloSpec(t))
	defer pluginhost.UseSpecs()
	run, err := boot.Start(context.Background(), "t", []string{"health", "pluginhost"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	// A later UseSpecs does not change what is mounted, so UI must not show it.
	pluginhost.UseSpecs(libhost.Spec{ID: "later"})
	if got := strings.Join(paths(), ","); got != "/api/plugins/hello/" {
		t.Fatalf("UI after Start: %s, want the mounted /api/plugins/hello/", got)
	}
	if err := run.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Not started: UI reports the registered list (what the builder reads).
	if got := strings.Join(paths(), ","); got != "/api/plugins/later/" {
		t.Fatalf("UI when stopped: %s, want the registered /api/plugins/later/", got)
	}
}
