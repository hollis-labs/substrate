package identity_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/modules/auth"
)

// An app that issues its own agent tokens (auth.UseKeySource) and also selects the
// identity module accepts both: two trust roots, one verifying path.
func TestKeySourceAndIdentityTokensBothVerify(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader) // generated per run; not a fixture
	if err != nil {
		t.Fatal(err)
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": "agent-1", "use": "sig",
		"x": base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)),
	}}})
	auth.UseKeySource(func(context.Context) ([]byte, error) { return jwks, nil })
	t.Cleanup(func() { auth.UseKeySource(nil) })

	a := start(t, "enforce", "")
	owner := a.owner()
	if c := a.me(a.token(owner)); c != 200 {
		t.Fatalf("identity token: %d", c)
	}
	head := enc(map[string]string{"alg": "EdDSA", "kid": "agent-1", "typ": "JWT"})
	body := enc(map[string]any{"sub": "agent/x", "aud": "demo", "jti": "j1", "exp": time.Now().Add(time.Hour).Unix()})
	agentTok := head + "." + body + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(head+"."+body)))
	if c := a.me(agentTok); c != 200 {
		t.Fatalf("key-source token: %d", c)
	}
	if c := a.me(agentTok + "x"); c != 401 {
		t.Fatalf("tampered key-source token: %d, want 401", c)
	}
}
