package httphost_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// fallApp builds a handler with method-specific mounts, a method-less subtree mount and
// app routes under /api/, with auth off so the mounts answer.
func fallApp(t *testing.T, notFound http.Handler) http.Handler {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, `{"auth":{"mode":"off"}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	api := http.NewServeMux()
	api.Handle("GET /api/things", ok("things"))
	h, err := httphost.Handler(run.App, httphost.Options{
		Routes:   api,
		NotFound: notFound,
		Mounts: []httphost.Mount{
			{Pattern: "GET /.well-known/jwks.json", Handler: ok("jwks"), Open: true},
			{Pattern: "POST /hook", Handler: ok("hook"), Open: true},
			{Pattern: "GET /items/{id}", Handler: ok("item"), Open: true},
			{Pattern: "/a2a/", Handler: ok("rpc"), Open: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func flatCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct{ Code, Message string }
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.Message == "" {
		t.Fatalf("body is not the flat error: %q", w.Body.String())
	}
	return b.Code
}

func isHTML(w *httptest.ResponseRecorder) bool {
	return strings.Contains(strings.ToLower(w.Body.String()), "<!doctype html")
}

func TestWrongMethodOnAMethodSpecificPathIs405NotTheSPA(t *testing.T) {
	h := fallApp(t, nil)
	for _, c := range []struct{ method, path, allow string }{
		{"POST", "/.well-known/jwks.json", "GET, HEAD"},
		{"DELETE", "/items/7", "GET, HEAD"},
		{"POST", "/healthz", "GET, HEAD"},
		{"PUT", "/hook", "POST"},
	} {
		w := do(h, c.method, c.path)
		if w.Code != http.StatusMethodNotAllowed || isHTML(w) {
			t.Errorf("%s %s: %d %q, want 405 and no HTML", c.method, c.path, w.Code, w.Body.String())
			continue
		}
		if got := w.Header().Get("Allow"); got != c.allow {
			t.Errorf("%s %s: Allow %q, want %q", c.method, c.path, got, c.allow)
		}
		if code := flatCode(t, w); code != "method_not_allowed" {
			t.Errorf("%s %s: code %q", c.method, c.path, code)
		}
	}
	// The right method still reaches the mount, and a method-less mount takes every method.
	if w := do(h, "GET", "/.well-known/jwks.json"); w.Code != 200 || w.Body.String() != "jwks" {
		t.Errorf("GET jwks: %d %q", w.Code, w.Body.String())
	}
	if w := do(h, "DELETE", "/a2a/x"); w.Code != 200 || w.Body.String() != "rpc" {
		t.Errorf("DELETE on a method-less mount: %d %q", w.Code, w.Body.String())
	}
}

func TestDefaultSPAFallbackIsUnchanged(t *testing.T) {
	h := fallApp(t, nil)
	// An unknown path, and a browser GET to a path only a POST mount serves, still load the SPA
	// (the placeholder here, Assets being nil), so client routes keep working.
	for _, c := range []struct{ method, path string }{{"GET", "/nope"}, {"GET", "/hook"}, {"HEAD", "/hook"}, {"POST", "/nope"}} {
		if w := do(h, c.method, c.path); w.Code != 200 {
			t.Errorf("%s %s: %d, want the SPA's 200", c.method, c.path, w.Code)
		}
	}
	if w := do(h, "GET", "/nope"); !isHTML(w) {
		t.Errorf("GET /nope is not the SPA: %q", w.Body.String())
	}
}

func TestNotFoundFlatReplacesTheSPA(t *testing.T) {
	h := fallApp(t, httphost.NotFoundFlat)
	for _, c := range []struct {
		method, path string
		status       int
		code, allow  string
	}{
		{"GET", "/nope", 404, "not_found", ""},
		{"POST", "/nope", 404, "not_found", ""},
		{"GET", "/", 404, "not_found", ""},
		{"GET", "/plugins/x", 404, "not_found", ""},
		{"POST", "/.well-known/jwks.json", 405, "method_not_allowed", "GET, HEAD"},
		{"GET", "/hook", 405, "method_not_allowed", "POST"},
	} {
		w := do(h, c.method, c.path)
		if w.Code != c.status || isHTML(w) {
			t.Errorf("%s %s: %d %q, want %d", c.method, c.path, w.Code, w.Body.String(), c.status)
			continue
		}
		if code := flatCode(t, w); code != c.code {
			t.Errorf("%s %s: code %q, want %q", c.method, c.path, code, c.code)
		}
		if got := w.Header().Get("Allow"); got != c.allow {
			t.Errorf("%s %s: Allow %q, want %q", c.method, c.path, got, c.allow)
		}
	}
	// Routes, mounts and /healthz are unaffected.
	if w := do(h, "GET", "/api/things"); w.Code != 200 || w.Body.String() != "things" {
		t.Errorf("GET /api/things: %d %q", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/hook"); w.Code != 200 || w.Body.String() != "hook" {
		t.Errorf("POST /hook: %d %q", w.Code, w.Body.String())
	}
	if w := do(h, "GET", "/healthz"); w.Code != 200 {
		t.Errorf("GET /healthz: %d", w.Code)
	}
	// An unknown path under /api/ is the app's Routes' to answer, not NotFound's.
	if w := do(h, "GET", "/api/nope"); w.Code != 404 || strings.Contains(w.Body.String(), "not_found") {
		t.Errorf("GET /api/nope: %d %q, want the Routes mux's own 404", w.Code, w.Body.String())
	}
}

func TestNotFoundIsRawAndSeesTheAllowHeader(t *testing.T) {
	// An app's own envelope, nested like libs' (which apierr.Normalize would flatten).
	own := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, code := 404, "not_found"
		if w.Header().Get("Allow") != "" {
			status, code = 405, "method_not_allowed"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"m"}}`))
	})
	h := fallApp(t, own)
	if w := do(h, "GET", "/nope"); w.Code != 404 || w.Body.String() != `{"error":{"code":"not_found","message":"m"}}` {
		t.Errorf("GET /nope: %d %q, want the app's own body untouched", w.Code, w.Body.String())
	}
	w := do(h, "POST", "/.well-known/jwks.json")
	if w.Code != 405 || w.Body.String() != `{"error":{"code":"method_not_allowed","message":"m"}}` || w.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST jwks: %d %q Allow %q", w.Code, w.Body.String(), w.Header().Get("Allow"))
	}
}

func TestNotFoundWithAssetsIsRefused(t *testing.T) {
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	assets := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	if _, err := httphost.Handler(run.App, httphost.Options{Assets: assets, NotFound: httphost.NotFoundFlat}); err == nil || !strings.Contains(err.Error(), "NotFound") {
		t.Fatalf("NotFound with Assets: %v, want a refusal naming NotFound", err)
	}
}

// variantApp builds a handler under the config the builder writes for a variant: a service
// gets httphost.fallback: not_found, an app-host nothing. It has a Raw mount whose handler
// answers its own 404.
func variantApp(t *testing.T, cfg string, o httphost.Options) (http.Handler, error) {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	o.Mounts = append(o.Mounts,
		httphost.Mount{Pattern: "GET /.well-known/jwks.json", Handler: ok("jwks"), Open: true},
		httphost.Mount{Pattern: "/proto/", Open: true, Raw: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"no_such_thing","message":"m"}}`))
		})},
	)
	return httphost.Handler(run.App, o)
}

const (
	serviceCfg = `{"auth":{"mode":"off"},"httphost":{"fallback":"not_found"}}`
	appHostCfg = `{"auth":{"mode":"off"}}`
)

func TestServiceVariantAnswers404And405Flat(t *testing.T) {
	h, err := variantApp(t, serviceCfg, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	w := do(h, "GET", "/nope")
	if w.Code != 404 || isHTML(w) || flatCode(t, w) != "not_found" {
		t.Errorf("service GET /nope: %d %q", w.Code, w.Body.String())
	}
	w = do(h, "POST", "/.well-known/jwks.json")
	if w.Code != 405 || flatCode(t, w) != "method_not_allowed" || w.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("service POST jwks: %d %q Allow %q", w.Code, w.Body.String(), w.Header().Get("Allow"))
	}
}

func TestAppHostVariantKeepsThePlaceholder(t *testing.T) {
	for _, cfg := range []string{appHostCfg, `{"auth":{"mode":"off"},"httphost":{"fallback":"spa"}}`} {
		h, err := variantApp(t, cfg, httphost.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if w := do(h, "GET", "/nope"); w.Code != 200 || !isHTML(w) {
			t.Errorf("%s GET /nope: %d %q, want the placeholder", cfg, w.Code, w.Body.String())
		}
		w := do(h, "POST", "/.well-known/jwks.json")
		if w.Code != 405 || flatCode(t, w) != "method_not_allowed" {
			t.Errorf("%s POST jwks: %d %q", cfg, w.Code, w.Body.String())
		}
	}
}

func TestARawMountsOwn404PassesThroughUntouched(t *testing.T) {
	for _, cfg := range []string{serviceCfg, appHostCfg} {
		h, err := variantApp(t, cfg, httphost.Options{})
		if err != nil {
			t.Fatal(err)
		}
		w := do(h, "GET", "/proto/missing")
		if w.Code != 404 || w.Body.String() != `{"error":{"code":"no_such_thing","message":"m"}}` {
			t.Errorf("%s raw mount 404: %d %q", cfg, w.Code, w.Body.String())
		}
	}
}

func TestFallbackConfigYieldsToAssetsAndOptions(t *testing.T) {
	// Assets mean a UI: the SPA serves unknown paths even under fallback: not_found.
	assets := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><p>ui</p>")}}
	h, err := variantApp(t, serviceCfg, httphost.Options{Assets: assets})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(h, "GET", "/client/route"); w.Code != 200 || !isHTML(w) {
		t.Errorf("Assets under not_found: %d %q", w.Code, w.Body.String())
	}
	// Options.NotFound wins over the config.
	h, err = variantApp(t, `{"auth":{"mode":"off"},"httphost":{"fallback":"spa"}}`, httphost.Options{NotFound: httphost.NotFoundFlat})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(h, "GET", "/nope"); w.Code != 404 {
		t.Errorf("Options.NotFound over fallback: spa: %d", w.Code)
	}
	// An unknown value is refused.
	if _, err := variantApp(t, `{"auth":{"mode":"off"},"httphost":{"fallback":"html"}}`, httphost.Options{}); err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Errorf("fallback: html: %v", err)
	}
}
