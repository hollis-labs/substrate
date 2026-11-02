package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	_ "github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/mcp"
)

func setup(t *testing.T, mods []string, cfg string) (http.Handler, *module.App) {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", mods, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	mcp.Server(run.App).RegisterChecked(gmcp.Tool{
		Name: "ping", Description: "returns pong", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(context.Context, map[string]any) (any, error) { return map[string]any{"msg": "pong"}, nil },
	}, gmcp.Reads("returns a constant"))
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return h, run.App
}

func call(h http.Handler, bearer string) *httptest.ResponseRecorder {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","arguments":{}}}`
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestToolCallOverHTTP(t *testing.T) {
	h, _ := setup(t, []string{"health", "httphost", "mcp"}, "")
	w := call(h, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "pong") {
		t.Fatalf("tools/call: %d %s", w.Code, w.Body)
	}
}

func TestAuthGuardsMCPRegardlessOfModuleOrder(t *testing.T) {
	key := "mcp-test-key-1"
	cfg := `{"auth":{"mode":"enforce","api_keys":[{"id":"svc/m","hash":"` + libauth.HashKey(key) + `","scopes":["t.mcp"]}]}}`
	// mcp listed BEFORE auth on purpose.
	h, _ := setup(t, []string{"health", "httphost", "mcp", "auth"}, cfg)
	if w := call(h, ""); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
	if w := call(h, key); w.Code != 200 || !strings.Contains(w.Body.String(), "pong") {
		t.Errorf("scoped token: %d %s", w.Code, w.Body)
	}
}

func TestAuthorizeAllowsWithoutAuthModule(t *testing.T) {
	_, app := setup(t, []string{"health", "httphost", "mcp"}, "")
	if err := mcp.Authorize(context.Background(), app, "ping"); err != nil {
		t.Fatal(err)
	}
}
