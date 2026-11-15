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
