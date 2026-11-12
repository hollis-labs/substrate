package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/ui"
)

// TestAuthenticatedIsALiteralScopeOnADirectRoute pins what libs security/auth does
// with the string "authenticated" on a route registered directly on the
// ScopeTable: it is an ordinary scope name, so only a principal holding it (or "*")
// passes, and a key with other scopes gets 403. The empty string means "any verified
// caller". "authenticated" is a ui.json word that module.AddRoutes translates to "";
// the same route declared that way passes for the same key. The docs say so
// (docs/adopting-substrate.md 5.3, docs/writing-a-module.md); if libs changes this,
// this test fails and the docs change with it.
func TestAuthenticatedIsALiteralScopeOnADirectRoute(t *testing.T) {
	scoped, star := "scoped-key", "star-key"
	run := start(t, `{"auth":{"mode":"enforce","api_keys":[`+
		`{"id":"svc/a","hash":"`+libauth.HashKey(scoped)+`","scopes":["t.read","t.write"]},`+
		`{"id":"svc/s","hash":"`+libauth.HashKey(star)+`","scopes":["*"]}]}}`)

	// Direct registration on the table: the empty string, the word, and a real scope.
	auth.ScopeTable(run.App).
		Route("GET", "/api/direct-empty", "").
		Route("GET", "/api/direct-word", "authenticated").
		Route("GET", "/api/direct-scope", "t.read").
		Route("GET", "/api/direct-other", "t.other")
	// The same intent declared the module way: ui.json's "authenticated".
	err := module.AddRoutes(run.App, ui.Contribution{Module: "m", Routes: []ui.Route{{Method: "GET", Path: "/api/via-ui", Scope: "authenticated"}}},
		map[string]http.Handler{"GET /api/via-ui": http.HandlerFunc(ok)})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	for _, p := range []string{"direct-empty", "direct-word", "direct-scope", "direct-other"} {
		mux.HandleFunc("GET /api/"+p, ok)
	}
	h, err := httphost.Handler(run.App, httphost.Options{Routes: mux})
	if err != nil {
		t.Fatal(err)
	}
	do := func(path, bearer string) (int, string) {
		r := httptest.NewRequest("GET", path, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	for _, c := range []struct {
		name, path, bearer string
		want               int
	}{
		{`"" passes a scoped key`, "/api/direct-empty", scoped, 200},
		{`"" still needs a credential`, "/api/direct-empty", "", 401},
		{`"" refuses a bad credential`, "/api/direct-empty", "nope", 401},
		{`"authenticated" refuses a scoped key`, "/api/direct-word", scoped, 403},
		{`"authenticated" passes only "*"`, "/api/direct-word", star, 200},
		{`"authenticated" without a credential`, "/api/direct-word", "", 401},
		{"a held scope passes", "/api/direct-scope", scoped, 200},
		{"a scope not held is refused", "/api/direct-other", scoped, 403},
		{`ui.json "authenticated" passes the scoped key (translated to "")`, "/api/via-ui", scoped, 200},
		{`ui.json "authenticated" still needs a credential`, "/api/via-ui", "", 401},
	} {
		if got, body := do(c.path, c.bearer); got != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, got, c.want, body)
		}
	}
	if code, body := do("/api/direct-word", scoped); code != 403 || !strings.Contains(body, "required scope: authenticated") {
		t.Errorf("the 403 for the literal word = %d %s, want the message to name the scope", code, body)
	}
}

func ok(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }
