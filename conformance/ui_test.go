package conformance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/catalog"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/httphost"
	_ "github.com/hollis-labs/substrate/modules/pluginhost"
	"github.com/hollis-labs/substrate/ui"
)

// uiModules returns the catalogue modules that contribute UI, via the module
// registry's own instances.
func uiModules(t *testing.T) map[string]module.UIContributor {
	t.Helper()
	out := map[string]module.UIContributor{}
	for _, cm := range catalog.Modules {
		if m, ok := module.Lookup(cm.Name); ok {
			if c, ok := m.(module.UIContributor); ok {
				out[cm.Name] = c
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no module contributes UI; the check below would pass vacuously")
	}
	return out
}

// Every contribution validates against the vocabulary and names its own module.
func TestEveryContributionIsValid(t *testing.T) {
	for name, c := range uiModules(t) {
		t.Run(name, func(t *testing.T) {
			got := c.UI()
			if got.Module != name {
				t.Errorf("ui.json says module %q, registered as %q", got.Module, name)
			}
			for _, err := range ui.Validate(got, ui.Default) {
				t.Error(err)
			}
		})
	}
}

// Every declared route is served and mapped to a scope: in enforce mode a caller
// holding every scope reaches it, and a caller with no token is refused with 401
// (never 403 route_not_mapped, never 404). This is the property that broke
// /api/capabilities once.
func TestEveryDeclaredRouteIsMappedAndServed(t *testing.T) {
	selected := []string{"config", "logging", "health", "httphost", "capabilities", "auth", "events", "pluginhost"}
	key := "conformance-key"
	cfg := `{"auth":{"mode":"enforce","api_keys":[{"id":"svc/c","hash":"` + libauth.HashKey(key) + `","scopes":["*"]}]}}`
	run, err := boot.Start(context.Background(), "conformance", selected, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	declared := 0
	for name, c := range uiModules(t) {
		if !contains(run.App.Started, name) {
			continue // not started in this selection
		}
		for _, r := range c.UI().Routes {
			declared++
			t.Run(name+" "+r.Method+" "+r.Path, func(t *testing.T) {
				path, method := r.Path, r.Method
				if strings.HasSuffix(path, "/") { // a subtree: probe something under it
					path += "probe"
				}
				if method == "*" {
					method = "GET"
				}
				req := func(bearer string) int {
					w := httptest.NewRecorder()
					rq := httptest.NewRequest(method, path, nil)
					if bearer != "" {
						rq.Header.Set("Authorization", "Bearer "+bearer)
					}
					h.ServeHTTP(w, rq)
					return w.Code
				}
				if got := req(""); got != http.StatusUnauthorized {
					t.Errorf("no token: %d, want 401", got)
				}
				// A subtree's handler may legitimately answer 404 for the unknown
				// thing under it; 403 is what an unmapped route looks like.
				if got := req(key); got == http.StatusForbidden || (got == http.StatusNotFound && !strings.HasSuffix(r.Path, "/")) || got >= 500 {
					t.Errorf("wildcard-scope caller: %d, want the handler to answer", got)
				}
			})
		}
	}
	if declared == 0 {
		t.Fatal("no declared routes were exercised")
	}
}
