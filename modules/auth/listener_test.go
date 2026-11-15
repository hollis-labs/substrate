package auth_test

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
	"github.com/hollis-labs/substrate/modules/httphost"
)

func meMode(t *testing.T, h http.Handler, ctx context.Context, bearer string) (int, string, bool) {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/me", nil).WithContext(ctx)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out struct {
		Mode          string `json:"mode"`
		Authenticated bool   `json:"authenticated"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Mode, out.Authenticated
}

// One handler, two listeners: the request context decides the mode. The app's own mode
// (permissive) is unchanged for a request with no listener guard.
func TestWithModeGuardsOneHandlerPerListener(t *testing.T) {
	key := "lm-key"
	run := start(t, `{"auth":{"mode":"permissive","api_keys":[{"id":"svc/l","hash":"`+libauth.HashKey(key)+`","scopes":["t.read"]}]}}`)
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	enf, err := auth.WithMode(context.Background(), run.App, libauth.ModeEnforce)
	if err != nil {
		t.Fatal(err)
	}
	if code, mode, _ := meMode(t, h, context.Background(), ""); code != 200 || mode != "permissive" {
		t.Fatalf("app listener, no token: %d %q, want 200 permissive", code, mode)
	}
	if code, _, _ := meMode(t, h, enf, ""); code != 401 {
		t.Fatalf("enforce listener, no token: %d, want 401", code)
	}
	if code, mode, ok := meMode(t, h, enf, key); code != 200 || mode != "enforce" || !ok {
		t.Fatalf("enforce listener, token: %d %q %v", code, mode, ok)
	}
	if g := auth.GuardFor(enf, run.App); g == nil || g.Mode() != libauth.ModeEnforce {
		t.Fatalf("GuardFor(enforce ctx) = %v", g)
	}
	if g := auth.GuardFor(context.Background(), run.App); g != auth.Guard(run.App) {
		t.Fatal("GuardFor(plain ctx) is not the app guard")
	}
	// auth.Middleware on a custom handler follows the listener too.
	mw := auth.Middleware(run.App, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("GET", "/custom", nil).WithContext(enf)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("Middleware on the enforce listener, no token: %d, want 401", w.Code)
	}
}

// The reverse: an enforcing app keeps a permissive local listener.
func TestWithModePermissiveListenerInEnforcingApp(t *testing.T) {
	key := "lm-key2"
	run := start(t, `{"auth":{"mode":"enforce","api_keys":[{"id":"svc/l","hash":"`+libauth.HashKey(key)+`","scopes":["t.read"]}]}}`)
	h, _ := httphost.Handler(run.App, httphost.Options{})
	perm, err := auth.WithMode(context.Background(), run.App, libauth.ModePermissive)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := meMode(t, h, context.Background(), ""); code != 401 {
		t.Fatalf("app listener, no token: %d, want 401", code)
	}
	if code, mode, ok := meMode(t, h, perm, ""); code != 200 || mode != "permissive" || ok {
		t.Fatalf("permissive listener, no token: %d %q %v", code, mode, ok)
	}
}

func TestMiddlewareModeGuardsAndCarriesTheMode(t *testing.T) {
	key := "mm-key"
	run := start(t, `{"auth":{"mode":"permissive","api_keys":[{"id":"svc/m","hash":"`+libauth.HashKey(key)+`","scopes":["t.read"]}]}}`)
	h, _ := httphost.Handler(run.App, httphost.Options{})
	remote, err := auth.MiddlewareMode(run.App, libauth.ModeEnforce, h)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := meMode(t, remote, context.Background(), ""); code != 401 {
		t.Fatalf("no token: %d, want 401", code)
	}
	if code, mode, ok := meMode(t, remote, context.Background(), key); code != 200 || mode != "enforce" || !ok {
		t.Fatalf("token: %d %q %v", code, mode, ok)
	}
	// It guards on its own, not only through a guard inside next.
	bare, _ := auth.MiddlewareMode(run.App, libauth.ModeEnforce, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	if code, _, _ := meMode(t, bare, context.Background(), ""); code != 401 {
		t.Fatalf("bare handler, no token: %d, want 401", code)
	}
	if _, err := auth.MiddlewareMode(run.App, "loose", h); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

// A verifier added after a listener guard exists reaches it, and enforce needs one.
func TestListenerGuardFollowsAddVerifier(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	run := start(t, `{"auth":{"mode":"permissive"}}`)
	if _, err := auth.WithMode(context.Background(), run.App, libauth.ModeEnforce); err == nil || !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("enforce listener without a verifier: %v", err)
	}
	perm, err := auth.WithMode(context.Background(), run.App, libauth.ModePermissive)
	if err != nil {
		t.Fatal(err)
	}
	key := "late-key"
	if err := auth.AddVerifier(run.App, libauth.NewAPIKeyVerifier(libauth.APIKey{ID: "svc/late", Hash: libauth.HashKey(key), Scopes: []string{"t.read"}})); err != nil {
		t.Fatal(err)
	}
	h, _ := httphost.Handler(run.App, httphost.Options{})
	if _, _, ok := meMode(t, h, perm, key); !ok {
		t.Fatal("permissive listener guard created before AddVerifier did not verify the new key")
	}
	enf, err := auth.WithMode(context.Background(), run.App, libauth.ModeEnforce)
	if err != nil {
		t.Fatalf("enforce listener after AddVerifier: %v", err)
	}
	if code, _, ok := meMode(t, h, enf, key); code != 200 || !ok {
		t.Fatalf("enforce listener, late key: %d %v", code, ok)
	}
}

func TestWithModeNeedsTheAuthModule(t *testing.T) {
	run, err := boot.Start(context.Background(), "t", []string{"health"}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	if _, err := auth.WithMode(context.Background(), run.App, libauth.ModeEnforce); err == nil {
		t.Fatal("WithMode without listeners accepted")
	}
}
