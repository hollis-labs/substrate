package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/genui"
)

type resolverFunc func(context.Context, libauth.Principal) (Scope, error)

func (f resolverFunc) Resolve(ctx context.Context, p libauth.Principal) (Scope, error) {
	return f(ctx, p)
}

func TestHTTPActualBearerGuardCanonicalAliasesAndRecovery(t *testing.T) {
	db := newFileStore(t)
	service := serviceFixture(t, db, fixtureSurface(t), allowPolicy(), effectExecutor(db, false))
	h, err := NewHTTP(service, resolverFunc(func(_ context.Context, p libauth.Principal) (Scope, error) {
		switch p.ID {
		case "key-primary", "key-rotated":
			return testScope, nil
		case "other":
			return Scope{testScope.Namespace, "other"}, nil
		default:
			return Scope{}, ErrForbidden
		}
	}), "sample.genui")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	scopeTable := libauth.NewScopeTable()
	for pattern, handler := range h.Handlers() {
		mux.Handle(pattern, handler)
		method, path, _ := strings.Cut(pattern, " ")
		scopeTable.Route(method, path, "")
	}
	v := libauth.NewAPIKeyVerifier(
		libauth.APIKey{ID: "key-primary", Hash: libauth.HashKey("fixture-primary"), Scopes: []string{"sample.genui.*"}},
		libauth.APIKey{ID: "key-rotated", Hash: libauth.HashKey("fixture-rotated"), Scopes: []string{"sample.genui.*"}},
		libauth.APIKey{ID: "other", Hash: libauth.HashKey("fixture-other"), Scopes: []string{"sample.genui.read"}},
		libauth.APIKey{ID: "narrow", Hash: libauth.HashKey("fixture-narrow"), Scopes: []string{"sample.other"}},
	)
	guard, err := libauth.New(libauth.Config{Mode: libauth.ModePermissive, Verifier: v, Scopes: scopeTable})
	if err != nil {
		t.Fatal(err)
	}
	server := guard.Middleware(mux)
	do := func(method, path, token string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", "invalid"} {
		if w := do("POST", "/api/genui/actions", token, encode(fixtureCall())); w.Code != 401 {
			t.Fatalf("unverified %q got %d", token, w.Code)
		}
	}
	if w := do("POST", "/api/genui/actions", "fixture-narrow", encode(fixtureCall())); w.Code != 403 {
		t.Fatal("scope bypass", w.Code)
	}
	if w := do("POST", "/api/genui/actions", "fixture-primary", []byte(`{"version":"1.0","version":"1.0"}`)); w.Code != 400 {
		t.Fatal("duplicate JSON accepted")
	}
	call := fixtureCall()
	call.CallAgentFunction.CallFunction.Args.Input["choice"] = "deny"
	w := do("POST", "/api/genui/actions", "fixture-primary", encode(call))
	if w.Code != 200 {
		t.Fatalf("submit %d %s", w.Code, w.Body.String())
	}
	changed := fixtureCall()
	w = do("POST", "/api/genui/actions", "fixture-rotated", encode(changed))
	if w.Code != 200 {
		t.Fatal("alias status", w.Code)
	}
	r, err := genui.ReadResponse(w.Body.Bytes())
	if err != nil || r.Response.AgentFunctionResponse.Value.Status != genui.StatusConflict {
		t.Fatal("rotated alias replayed", err)
	}
	w = do("GET", "/api/genui/actions/action_1?functionCallId=reconnect_1", "fixture-rotated", nil)
	if w.Code != 200 {
		t.Fatalf("recover %d %s", w.Code, w.Body.String())
	}
	var recovery Recovery
	if err = json.Unmarshal(w.Body.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.Request.CallAgentFunction.FunctionCallID != "reconnect_1" || recovery.Response.AgentFunctionResponse.FunctionCallID != "reconnect_1" || recovery.Request.CallAgentFunction.CallFunction.Args.Input["choice"] != "deny" {
		t.Fatal("recovery guessed choice")
	}
	if w = do("GET", "/api/genui/actions/action_1?functionCallId=reconnect_1", "fixture-other", nil); w.Code != 404 {
		t.Fatal("cross actor leak", w.Code)
	}
	if w = do("GET", "/api/genui/actions/action_1?functionCallId=a&functionCallId=b", "fixture-primary", nil); w.Code != 400 {
		t.Fatal("duplicate query")
	}
	if w = do("POST", "/api/genui/surfaces", "fixture-primary", []byte(`{"pattern":"approval","params":{},"url":"https://example.invalid"}`)); w.Code != 400 {
		t.Fatal("unreviewed show parameters")
	}
	if db.effectCount(t) != 1 {
		t.Fatal("duplicate HTTP execution")
	}
}
