package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
)

type mintReply struct {
	Token string `json:"token"`
	TTL   int    `json:"ttl_seconds"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// issuer is a stand-in for Tether: it serves a JWKS and a deny-list, signs
// tokens, and mints them on request.
type issuer struct {
	t      *testing.T
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	denied atomic.Value // []string of jti
	srv    *httptest.Server
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	is := &issuer{t: t, pub: pub, priv: priv}
	is.denied.Store([]string{})
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "OKP", "crv": "Ed25519", "kid": "k1", "x": b64(pub)}}})
	})
	mux.HandleFunc("/denylist", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jti": is.denied.Load(), "sub": []string{}, "sid": []string{}})
	})
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer parent-credential" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(mintReply{Token: "minted-" + "value", TTL: 900})
	})
	is.srv = httptest.NewServer(mux)
	t.Cleanup(is.srv.Close)
	return is
}

func (is *issuer) token(app, sub, jti, scope string) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": "k1", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"sub": sub, "aud": app, "scope": scope, "jti": jti, "kind": "service", "exp": time.Now().Add(time.Hour).Unix()})
	signing := b64(hdr) + "." + b64(claims)
	return signing + "." + b64(ed25519.Sign(is.priv, []byte(signing)))
}

func statusOf(h http.Handler, path, bearer string) int {
	r := httptest.NewRequest("GET", "http://localhost"+path, nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestJWTFromTheIssuerIsVerifiedAndARevokedOneStopsWorking(t *testing.T) {
	is := newIssuer(t)
	cfg := `{"auth":{"mode":"enforce","jwks_url":"` + is.srv.URL + `/jwks.json","denylist_url":"` + is.srv.URL + `/denylist","denylist_interval_seconds":0.05}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	h, _ := httphost.Handler(run.App, httphost.Options{})
	tok := is.token("t", "svc/worker", "jti-1", "t.read")

	if got := statusOf(h, "/api/me", ""); got != 401 {
		t.Errorf("no token: %d", got)
	}
	if got := statusOf(h, "/api/me", tok); got != 200 {
		t.Fatalf("a token signed by the issuer: %d", got)
	}
	if got := statusOf(h, "/api/me", is.token("other-app", "svc/worker", "jti-2", "t.read")); got != 401 {
		t.Errorf("a token for another audience: %d", got)
	}

	is.denied.Store([]string{"jti-1"})
	deadline := time.Now().Add(3 * time.Second)
	for statusOf(h, "/api/me", tok) != 401 {
		if time.Now().After(deadline) {
			t.Fatal("a revoked token still works after the deny-list was published")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := statusOf(h, "/api/me", is.token("t", "svc/worker", "jti-3", "t.read")); got != 200 {
		t.Errorf("revoking one token must not affect another: %d", got)
	}
}

func TestMisconfiguredPlumbingFailsStart(t *testing.T) {
	for name, cfg := range map[string]string{
		"denylist without jwt":    `{"auth":{"denylist_url":"http://x/denylist"}}`,
		"token_file and mint_url": `{"auth":{"token_file":"/x","mint_url":"http://x","mint_credential_file":"/y"}}`,
		"mint_url without parent": `{"auth":{"mint_url":"http://x"}}`,
	} {
		if _, err := boot.Start(context.Background(), "t", []string{"health", "auth"}, cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func startWith(t *testing.T, authCfg map[string]any) *boot.Running {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"auth": authCfg})
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth"}, string(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	return run
}

func TestServiceTokenFileIsAttachedAndRotationIsPickedUp(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(file, []byte("service-token-1\n"), 0o600)
	run := startWith(t, map[string]any{"token_file": file})

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = append(seen, r.Header.Get("Authorization")) }))
	defer srv.Close()
	c := auth.HTTPClient(run.App)
	do := func(h string) {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	do("")
	_ = os.WriteFile(file, []byte("service-token-2\n"), 0o600)
	do("")
	do("Bearer caller-set") // an explicit credential is never overwritten
	want := []string{"Bearer service-token-1", "Bearer service-token-2", "Bearer caller-set"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("authorization headers = %v, want %v", seen, want)
	}

	// With a host allowlist the token goes to those hosts only.
	seen = nil
	scoped := auth.HTTPClient(run.App, "elsewhere.example")
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := scoped.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(seen) != 1 || seen[0] != "" {
		t.Errorf("token leaked to a host outside the allowlist: %v", seen)
	}
}

func TestNoTokenConfiguredAttachesNothing(t *testing.T) {
	run := startWith(t, map[string]any{})
	if auth.Token(run.App) != nil {
		t.Fatal("no token source expected in the dev default")
	}
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("Authorization") }))
	defer srv.Close()
	resp, err := auth.HTTPClient(run.App).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got != "" {
		t.Errorf("authorization = %q", got)
	}
}

func TestMintedTokenComesFromTheIssuerUsingTheParentCredential(t *testing.T) {
	is := newIssuer(t)
	parent := filepath.Join(t.TempDir(), "parent")
	_ = os.WriteFile(parent, []byte("parent-credential\n"), 0o600)
	run := startWith(t, map[string]any{"mint_url": is.srv.URL + "/auth/token", "mint_credential_file": parent, "mint_body": map[string]any{"audience": "tether"}})
	ts := auth.Token(run.App)
	if ts == nil {
		t.Fatal("token source expected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tok, err := ts(ctx)
	if err != nil || tok != "minted-"+"value" {
		t.Fatalf("token %q, err %v", tok, err)
	}
}

func TestUnreadableTokenFileNeverLeaksItsPath(t *testing.T) {
	run := startWith(t, map[string]any{"token_file": "/nonexistent/secret-location/token"})
	_, err := auth.Token(run.App)(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-location") {
		t.Fatalf("err = %v", err)
	}
}
