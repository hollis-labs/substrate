package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// ring is an in-process issuer key ring, as an app that signs its own tokens keeps one.
// Keys are generated per test; nothing is a fixture.
type ring struct {
	mu    sync.Mutex
	keys  map[string]ed25519.PrivateKey
	calls atomic.Int64
	fail  atomic.Bool
}

func newRing() *ring { return &ring{keys: map[string]ed25519.PrivateKey{}} }

func (r *ring) add(t *testing.T, kid string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.keys[kid] = priv
	r.mu.Unlock()
}

func (r *ring) retire(kid string) {
	r.mu.Lock()
	delete(r.keys, kid)
	r.mu.Unlock()
}

// source is the auth.KeySource: the public half of every key in the ring as a JWKS.
func (r *ring) source(context.Context) ([]byte, error) {
	r.calls.Add(1)
	if r.fail.Load() {
		return nil, errors.New("key ring not loaded")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	for kid, k := range r.keys {
		pub := k.Public().(ed25519.PublicKey)
		doc.Keys = append(doc.Keys, map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": kid, "x": base64.RawURLEncoding.EncodeToString(pub), "use": "sig"})
	}
	return json.Marshal(doc)
}

// sign mints an EdDSA JWT for the app "t" with the ring's key kid. A retired key can
// still sign (the attacker's view); the verifier must refuse it.
func (r *ring) sign(t *testing.T, kid, jti string, priv ed25519.PrivateKey) string {
	t.Helper()
	if priv == nil {
		r.mu.Lock()
		priv = r.keys[kid]
		r.mu.Unlock()
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]string{"alg": "EdDSA", "kid": kid, "typ": "JWT"})
	body := enc(map[string]any{"sub": "agent/" + jti, "aud": "t", "iss": "t-issuer", "scope": "t.read", "jti": jti, "exp": time.Now().Add(time.Hour).Unix()})
	sig := ed25519.Sign(priv, []byte(head+"."+body))
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (r *ring) key(kid string) ed25519.PrivateKey {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keys[kid]
}

func useHooks(t *testing.T, src auth.KeySource, d *libauth.DenyList) {
	t.Helper()
	auth.UseKeySource(src)
	auth.UseDenyList(d)
	t.Cleanup(func() { auth.UseKeySource(nil); auth.UseDenyList(nil) })
}

const issuerCfg = `{"auth":{"mode":"enforce","issuer":"t-issuer"}}`

func TestKeySourceVerifiesAndFollowsRotation(t *testing.T) {
	r := newRing()
	r.add(t, "k1")
	useHooks(t, r.source, nil)
	run := start(t, issuerCfg)
	h, _ := httphost.Handler(run.App, httphost.Options{})

	if c := get(t, h, "/api/me", r.sign(t, "k1", "a", nil)).Code; c != 200 {
		t.Fatalf("k1 token: %d", c)
	}
	if r.calls.Load() == 0 {
		t.Fatal("key source never called")
	}
	// A token from another key is refused.
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	if c := get(t, h, "/api/me", r.sign(t, "k1", "b", stranger)).Code; c != 401 {
		t.Fatalf("token signed by a key not in the ring: %d, want 401", c)
	}

	// Rotation, no restart: a new key verifies after RefreshKeys...
	r.add(t, "k2")
	if err := auth.RefreshKeys(context.Background(), run.App); err != nil {
		t.Fatal(err)
	}
	if c := get(t, h, "/api/me", r.sign(t, "k2", "c", nil)).Code; c != 200 {
		t.Fatalf("k2 token after RefreshKeys: %d", c)
	}
	// ...and without it, once the unknown kid triggers a re-read (at most once a second).
	r.add(t, "k3")
	time.Sleep(1100 * time.Millisecond)
	if c := get(t, h, "/api/me", r.sign(t, "k3", "d", nil)).Code; c != 200 {
		t.Fatalf("k3 token without RefreshKeys: %d", c)
	}

	// A retired key stops verifying once the app refreshes.
	k1 := r.key("k1")
	r.retire("k1")
	if err := auth.RefreshKeys(context.Background(), run.App); err != nil {
		t.Fatal(err)
	}
	if c := get(t, h, "/api/me", r.sign(t, "k1", "e", k1)).Code; c != 401 {
		t.Fatalf("token from a retired key: %d, want 401", c)
	}
}

func TestKeySourceFailureIsUnavailableNotAccepted(t *testing.T) {
	r := newRing()
	r.add(t, "k1")
	r.fail.Store(true)
	useHooks(t, r.source, nil)
	run := start(t, issuerCfg)
	h, _ := httphost.Handler(run.App, httphost.Options{})
	if c := get(t, h, "/api/me", r.sign(t, "k1", "a", nil)).Code; c != 503 {
		t.Fatalf("key source failing: %d, want 503", c)
	}
	// Once it answers, the same token verifies (no restart).
	r.fail.Store(false)
	if err := auth.RefreshKeys(context.Background(), run.App); err != nil {
		t.Fatal(err)
	}
	if c := get(t, h, "/api/me", r.sign(t, "k1", "a", nil)).Code; c != 200 {
		t.Fatalf("after the source recovered: %d", c)
	}
}

func TestInProcessDenyListRefusesRevokedTokens(t *testing.T) {
	r := newRing()
	r.add(t, "k1")
	d := libauth.NewDenyList()
	useHooks(t, r.source, d)
	run := start(t, issuerCfg)
	h, _ := httphost.Handler(run.App, httphost.Options{})
	revoked, kept := r.sign(t, "k1", "jti-revoked", nil), r.sign(t, "k1", "jti-kept", nil)
	if c := get(t, h, "/api/me", revoked).Code; c != 200 {
		t.Fatalf("before revocation: %d", c)
	}
	d.Replace(libauth.DenyListDoc{JTI: []string{"jti-revoked"}})
	if c := get(t, h, "/api/me", revoked).Code; c != 401 {
		t.Fatalf("revoked jti: %d, want 401", c)
	}
	if c := get(t, h, "/api/me", kept).Code; c != 200 {
		t.Fatalf("other jti: %d", c)
	}
	// Revoking the subject works the same way.
	d.Replace(libauth.DenyListDoc{Sub: []string{"agent/jti-kept"}})
	if c := get(t, h, "/api/me", kept).Code; c != 401 {
		t.Fatalf("revoked sub: %d, want 401", c)
	}
}

func TestKeySourceStartRefusals(t *testing.T) {
	r := newRing()
	r.add(t, "k1")
	for _, tc := range []struct {
		name string
		src  auth.KeySource
		deny *libauth.DenyList
		cfg  string
		want string
	}{
		{"key source and jwks_url", r.source, nil, `{"auth":{"mode":"enforce","jwks_url":"http://127.0.0.1:1/jwks.json"}}`, "jwks_url"},
		{"deny-list and denylist_url", r.source, libauth.NewDenyList(), `{"auth":{"mode":"enforce","denylist_url":"http://127.0.0.1:1/deny"}}`, "denylist_url"},
		{"deny-list without a JWT verifier", nil, libauth.NewDenyList(), `{"auth":{"mode":"permissive"}}`, "needs the JWT verifier"},
		{"enforce with no source", nil, nil, `{"auth":{"mode":"enforce"}}`, "needs a verifier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useHooks(t, tc.src, tc.deny)
			run, err := boot.Start(context.Background(), "t", []string{"health", "auth"}, tc.cfg)
			if err == nil {
				_ = run.Stop(context.Background())
				t.Fatalf("started; want an error naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

// Without the hooks nothing changes: RefreshKeys is a no-op and no JWT verifier exists.
func TestNoKeySourceIsUnchanged(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	run := start(t, `{}`)
	if err := auth.RefreshKeys(context.Background(), run.App); err != nil {
		t.Fatal(err)
	}
	if _, ok := run.App.Value(auth.KeysKey); ok {
		t.Fatal("a JWT verifier was published with no key source configured")
	}
}
