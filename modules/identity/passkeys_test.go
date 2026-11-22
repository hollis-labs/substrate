package identity_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/descope/virtualwebauthn"
)

const publicOrigin = "https://app.example.test"

// begun is a ceremony's start: its id and the options for the browser (navigator.credentials).
type begun struct {
	ChallengeID string          `json:"challenge_id"`
	Options     json.RawMessage `json:"options"`
}

func TestPasskeyRegisterSignInAndPasskeyOnly(t *testing.T) {
	a := start(t, "enforce", `, "public_url": "`+publicOrigin+`"`)
	own := a.owner()
	origin := header("Origin", publicOrigin)

	var st struct {
		Passkeys bool `json:"passkeys"`
	}
	a.do("GET", "/api/setup", nil).json(&st)
	if !st.Passkeys {
		t.Fatal("GET /api/setup does not offer passkeys with an https public_url")
	}

	// Register a passkey (a software authenticator stands in for the browser's).
	var reg begun
	w := a.do("POST", "/api/identity/passkeys/register/begin", map[string]any{}, cookie(own), origin)
	code(t, w, 200)
	w.json(&reg)
	attOpts, err := virtualwebauthn.ParseAttestationOptions(string(reg.Options))
	if err != nil {
		t.Fatal(err)
	}
	if attOpts.RelyingPartyID != "app.example.test" {
		t.Fatalf("rp id %q", attOpts.RelyingPartyID)
	}
	rp := virtualwebauthn.RelyingParty{ID: "app.example.test", Name: "demo", Origin: publicOrigin}
	authr := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{UserHandle: []byte(attOpts.UserID)})
	cred := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	att := virtualwebauthn.CreateAttestationResponse(rp, authr, cred, *attOpts)
	w = a.do("POST", "/api/identity/passkeys/register/finish", map[string]any{"challenge_id": reg.ChallengeID, "credential": json.RawMessage(att), "name": "Laptop"}, cookie(own), origin)
	code(t, w, 201)
	authr.AddCredential(cred)
	// The same challenge cannot register twice.
	code(t, a.do("POST", "/api/identity/passkeys/register/finish", map[string]any{"challenge_id": reg.ChallengeID, "credential": json.RawMessage(att)}, cookie(own), origin), 400)

	var list struct {
		Passkeys []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"passkeys"`
		Enabled bool `json:"enabled"`
	}
	a.do("GET", "/api/identity/passkeys", nil, cookie(own)).json(&list)
	if !list.Enabled || len(list.Passkeys) != 1 || list.Passkeys[0].Name != "Laptop" {
		t.Fatalf("passkeys %+v", list)
	}

	// Sign in with it: no username, no password.
	signIn := func() reply {
		var lg begun
		w := a.do("POST", "/api/passkey/begin", map[string]any{}, origin)
		code(t, w, 200)
		w.json(&lg)
		asOpts, err := virtualwebauthn.ParseAssertionOptions(string(lg.Options))
		if err != nil {
			t.Fatal(err)
		}
		as := virtualwebauthn.CreateAssertionResponse(rp, authr, cred, *asOpts)
		w = a.do("POST", "/api/passkey/finish", map[string]any{"challenge_id": lg.ChallengeID, "credential": json.RawMessage(as)}, origin)
		// A challenge is good for one attempt.
		code(t, a.do("POST", "/api/passkey/finish", map[string]any{"challenge_id": lg.ChallengeID, "credential": json.RawMessage(as)}, origin), 401)
		return w
	}
	w = signIn()
	code(t, w, 200)
	pk := w.cookie("demo_session")
	if pk == nil {
		t.Fatal("passkey sign-in set no cookie")
	}
	var prof struct {
		Username string `json:"username"`
		SignIn   string `json:"sign_in"`
	}
	a.do("GET", "/api/identity/profile", nil, cookie(pk)).json(&prof)
	if prof.Username != "owner" || prof.SignIn != "passkey" {
		t.Fatalf("profile %+v", prof)
	}

	// Passkey-only: the password stops working; the last passkey cannot be removed.
	code(t, a.do("PUT", "/api/identity/passkey-only", map[string]bool{"enabled": true}, cookie(pk)), 200)
	code(t, a.login("owner", password), 403)
	code(t, a.do("DELETE", "/api/identity/passkeys/"+list.Passkeys[0].ID, nil, cookie(pk)), 409)
	code(t, signIn(), 200)
	code(t, a.do("PUT", "/api/identity/passkey-only", map[string]bool{"enabled": false}, cookie(pk)), 200)
	code(t, a.login("owner", password), 200)
	code(t, a.do("PUT", "/api/identity/passkeys/"+list.Passkeys[0].ID, map[string]string{"name": "Old laptop"}, cookie(pk)), 204)
	code(t, a.do("DELETE", "/api/identity/passkeys/"+list.Passkeys[0].ID, nil, cookie(pk)), 204)
	// Gone, it no longer signs in, and the page is told it is not registered here.
	w = signIn()
	code(t, w, 401)
	if !strings.Contains(w.Body.String(), "passkey_unknown") {
		t.Fatalf("a removed passkey: %s", w.Body)
	}
	// A passkey (and user) this app never had: the same.
	stranger := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{UserHandle: []byte("usr_nobody")})
	other := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	stranger.AddCredential(other)
	var lg begun
	a.do("POST", "/api/passkey/begin", map[string]any{}, origin).json(&lg)
	asOpts, _ := virtualwebauthn.ParseAssertionOptions(string(lg.Options))
	as := virtualwebauthn.CreateAssertionResponse(rp, stranger, other, *asOpts)
	w = a.do("POST", "/api/passkey/finish", map[string]any{"challenge_id": lg.ChallengeID, "credential": json.RawMessage(as)}, origin)
	code(t, w, 401)
	if !strings.Contains(w.Body.String(), "passkey_unknown") {
		t.Fatalf("an unknown passkey: %s", w.Body)
	}
}

func TestPasskeysOffWithoutASecurePublicURL(t *testing.T) {
	for _, extra := range []string{"", `, "public_url": "http://hub.example.test:8470"`, `, "public_url": "https://app.example.test", "passkeys": false`} {
		a := start(t, "enforce", extra)
		code(t, a.do("POST", "/api/passkey/begin", map[string]any{}), 404)
		var st struct {
			Passkeys bool `json:"passkeys"`
		}
		a.do("GET", "/api/setup", nil).json(&st)
		if st.Passkeys {
			t.Fatalf("passkeys on with %q", extra)
		}
	}
	// http on localhost is a secure context too.
	a := start(t, "enforce", `, "public_url": "http://localhost:8470"`)
	code(t, a.do("POST", "/api/passkey/begin", map[string]any{}, header("Origin", "http://localhost:8470")), 200)
}

func TestPublicURLMakesCookiesSecureAndItsOriginAccepted(t *testing.T) {
	a := start(t, "enforce", `, "public_url": "`+publicOrigin+`"`)
	w := a.do("POST", "/api/setup", map[string]string{"username": "owner", "password": password}, header("Origin", publicOrigin))
	code(t, w, 200)
	if c := w.cookie("demo_session"); c == nil || !c.Secure {
		t.Fatalf("cookie %+v", c)
	}
	// Another origin still cannot write with the cookie.
	code(t, a.do("PUT", "/api/identity/profile", map[string]string{"display_name": "x"}, cookie(w.cookie("demo_session")), header("Origin", "https://evil.example")), 403)
}

func TestTrustedProxiesSetSecureCookies(t *testing.T) {
	a := start(t, "enforce", `, "trusted_proxies": ["192.0.2.0/24"]`)
	// do() sends from 192.0.2.10, inside the trusted range.
	w := a.do("POST", "/api/setup", map[string]string{"username": "owner", "password": password}, header("X-Forwarded-Proto", "https"))
	code(t, w, 200)
	if c := w.cookie("demo_session"); c == nil || !c.Secure {
		t.Fatalf("cookie from a trusted proxy %+v", c)
	}
	b := start(t, "enforce", `, "trusted_proxies": ["198.51.100.0/24"]`)
	w = b.do("POST", "/api/setup", map[string]string{"username": "owner", "password": password}, header("X-Forwarded-Proto", "https"))
	code(t, w, 200)
	if c := w.cookie("demo_session"); c == nil || c.Secure {
		t.Fatalf("an untrusted peer's X-Forwarded-Proto made the cookie Secure: %+v", c)
	}
}
