package httphost_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
)

func ok(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
}

func handler(t *testing.T, key string, mounts ...httphost.Mount) http.Handler {
	t.Helper()
	cfg := `{"auth":{"mode":"enforce","api_keys":[{"id":"svc/a","hash":"` + libauth.HashKey(key) + `","scopes":["t.a2a"]}]}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	auth.ScopeTable(run.App).Route("*", "/a2a/*", "t.a2a")
	h, err := httphost.Handler(run.App, httphost.Options{Mounts: mounts})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func status(h http.Handler, method, path, bearer string) (int, string) {
	r := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func TestMountsOutsideAPIAreGuardedUnlessOpen(t *testing.T) {
	key := "mount-key"
	h := handler(t, key,
		httphost.Mount{Pattern: "/a2a/", Handler: ok("rpc")},
		httphost.Mount{Pattern: "GET /.well-known/agent.json", Handler: ok("card"), Open: true},
	)
	if code, _ := status(h, "POST", "/a2a/rpc", ""); code != 401 {
		t.Errorf("a guarded mount without a token: %d", code)
	}
	if code, body := status(h, "POST", "/a2a/rpc", key); code != 200 || body != "rpc" {
		t.Errorf("a guarded mount with a token holding its scope: %d %q", code, body)
	}
	if code, body := status(h, "GET", "/.well-known/agent.json", ""); code != 200 || body != "card" {
		t.Errorf("an open mount: %d %q", code, body)
	}
	// The guard's denial arrives in the flat error body.
	_, body := status(h, "POST", "/a2a/rpc", "")
	var b struct{ Code, Message string }
	if json.Unmarshal([]byte(body), &b) != nil || b.Code == "" || b.Message == "" {
		t.Errorf("denial body is not flat: %s", body)
	}
}

func TestMountsThatShadowTheHostAreRefused(t *testing.T) {
	cfg := `{"auth":{"mode":"off"}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	for name, pat := range map[string]string{
		"under api": "/api/v1/", "healthz": "/healthz", "mcp": "/mcp", "the spa": "/", "relative": "a2a/",
		"api with method": "GET /api/x",
	} {
		_, err := httphost.Handler(run.App, httphost.Options{Mounts: []httphost.Mount{{Pattern: pat, Handler: ok("x")}}})
		if err == nil {
			t.Errorf("%s: %q was accepted", name, pat)
		}
	}
	_, err = httphost.Handler(run.App, httphost.Options{Mounts: []httphost.Mount{
		{Pattern: "/a2a/", Handler: ok("1")}, {Pattern: "/a2a/", Handler: ok("2")},
	}})
	if err == nil || !strings.Contains(err.Error(), "a2a") {
		t.Errorf("a conflicting pattern must be an error, not a panic: %v", err)
	}
}
