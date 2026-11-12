package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/auth"
	_ "github.com/hollis-labs/substrate/modules/database"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
	_ "github.com/hollis-labs/substrate/modules/logging"
)

const password = "correct horse battery"

// app is a started test app with the identity module in front of httphost.
type app struct {
	t       *testing.T
	h       http.Handler
	dir     string
	svc     *identity.Service
	running *boot.Running
}

func start(t *testing.T, mode string, extra string) *app { return startIn(t, t.TempDir(), mode, extra) }

func startIn(t *testing.T, dir, mode, extra string) *app {
	t.Helper()
	cfg := fmt.Sprintf(`{
		"database": {"sqlite": {"path": %q}},
		"auth": {"mode": %q},
		"identity": {"key_file": %q, "bcrypt_cost": 4%s}
	}`, filepath.Join(dir, "t.db"), mode, filepath.Join(dir, "id.key"), extra)
	run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	inner, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	svc := identity.From(run.App)
	return &app{t: t, h: svc.Middleware(inner), dir: dir, svc: svc, running: run}
}

type reply struct {
	*httptest.ResponseRecorder
}

func (r reply) json(v any) {
	if err := json.Unmarshal(r.Body.Bytes(), v); err != nil {
		panic(fmt.Sprintf("not JSON (%d): %s", r.Code, r.Body))
	}
}

func (r reply) cookie(name string) *http.Cookie {
	for _, c := range r.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

type opt func(*http.Request)

func bearer(tok string) opt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}
func cookie(c *http.Cookie) opt {
	return func(r *http.Request) { r.AddCookie(c); r.Header.Set("X-Requested-With", "test") }
}
func header(k, v string) opt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (a *app) do(method, path string, body any, opts ...opt) reply {
	a.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "192.0.2.10:1234"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return reply{w}
}

// owner runs first-time setup and returns the owner's session cookie.
func (a *app) owner() *http.Cookie {
	a.t.Helper()
	w := a.do("POST", "/api/setup", map[string]string{"display_name": "Owner", "username": "owner", "password": password})
	if w.Code != 200 {
		a.t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	c := w.cookie("demo_session")
	if c == nil {
		a.t.Fatal("setup set no session cookie")
	}
	return c
}

func (a *app) login(user, pw string) reply {
	return a.do("POST", "/api/login", map[string]string{"username": user, "password": pw})
}

func code(t *testing.T, w reply, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body)
	}
}

// ---- people ----

func TestSetupRunsOnceThenSignInSessionAndSignOut(t *testing.T) {
	a := start(t, "enforce", "")
	var st struct {
		SetupRequired bool `json:"setup_required"`
	}
	r := a.do("GET", "/api/setup", nil)
	code(t, r, 200)
	r.json(&st)
	if !st.SetupRequired {
		t.Fatal("a fresh app must ask for setup")
	}
	// Before setup nobody is signed in: a protected route is refused.
	code(t, a.do("GET", "/api/me", nil), 401)

	c := a.owner()
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie must be HttpOnly and SameSite=Strict: %+v", c)
	}
	code(t, a.do("POST", "/api/setup", map[string]string{"username": "x2", "password": password}), 409)
	r = a.do("GET", "/api/setup", nil)
	r.json(&st)
	if st.SetupRequired {
		t.Error("setup stays open after the owner exists")
	}

	var me struct {
		Authenticated bool
		Principal     libauth.Principal
	}
	r = a.do("GET", "/api/me", nil, cookie(c))
	code(t, r, 200)
	r.json(&me)
	if !me.Principal.Verified || !strings.HasPrefix(me.Principal.ID, "user:") || me.Principal.Display != "Owner" || !me.Principal.HasScope("anything") {
		t.Errorf("principal from the session = %+v", me.Principal)
	}

	code(t, a.do("POST", "/api/logout", nil, cookie(c)), 204)
	code(t, a.do("GET", "/api/me", nil, cookie(c)), 401)

	// Signing in again works, with the right password only.
	code(t, a.login("owner", "wrong password here"), 401)
	code(t, a.login("nobody", password), 401)
	w := a.login("owner", password)
	code(t, w, 200)
	code(t, a.do("GET", "/api/me", nil, cookie(w.cookie("demo_session"))), 200)
}

func TestCookieWritesNeedTheAppsOwnOriginAndAHeader(t *testing.T) {
	a := start(t, "enforce", "")
	c := a.owner()
	post := func(opts ...opt) int {
		r := httptest.NewRequest("POST", "/api/logout", nil)
		r.Host = "demo.example"
		r.AddCookie(c)
		for _, o := range opts {
			o(r)
		}
		w := httptest.NewRecorder()
		a.h.ServeHTTP(w, r)
		return w.Code
	}
	if got := post(); got != 403 {
		t.Errorf("a cookie write with no X-Requested-With: %d, want 403", got)
	}
	if got := post(header("X-Requested-With", "x"), header("Origin", "https://evil.example")); got != 403 {
		t.Errorf("a cookie write from another origin: %d, want 403", got)
	}
	if got := post(header("X-Requested-With", "x"), header("Origin", "https://demo.example")); got != 204 {
		t.Errorf("a cookie write from the app's own origin: %d, want 204", got)
	}
}

func TestRepeatedWrongPasswordsLockOutBriefly(t *testing.T) {
	a := start(t, "enforce", "")
	a.owner()
	for i := 0; i < 5; i++ {
		code(t, a.login("owner", "wrong password "+fmt.Sprint(i)), 401)
	}
	code(t, a.login("owner", password), 429) // even the right one, for now
}

func TestPasswordChangeEndsOtherSessionsButNotTheCurrentOne(t *testing.T) {
	a := start(t, "enforce", "")
	first := a.owner()
	second := a.login("owner", password).cookie("demo_session")
	code(t, a.do("GET", "/api/me", nil, cookie(second)), 200)

	code(t, a.do("PUT", "/api/password", map[string]string{"current_password": "nope nope nope nope", "new_password": "a brand new password"}, cookie(first)), 401)
	code(t, a.do("PUT", "/api/password", map[string]string{"current_password": password, "new_password": "short"}, cookie(first)), 400)
	code(t, a.do("PUT", "/api/password", map[string]string{"current_password": password, "new_password": "a brand new password"}, cookie(first)), 204)

	code(t, a.do("GET", "/api/me", nil, cookie(first)), 200)
	code(t, a.do("GET", "/api/me", nil, cookie(second)), 401)
	code(t, a.login("owner", password), 401)
	code(t, a.login("owner", "a brand new password"), 200)
}

func TestSessionsAreListedAndRevocable(t *testing.T) {
	a := start(t, "enforce", "")
	first := a.owner()
	second := a.login("owner", password).cookie("demo_session")
	var list struct {
		Sessions []struct {
			ID      string
			Current bool
		}
	}
	r := a.do("GET", "/api/identity/sessions", nil, cookie(first))
	code(t, r, 200)
	r.json(&list)
	if len(list.Sessions) != 2 {
		t.Fatalf("sessions = %+v", list.Sessions)
	}
	var other string
	for _, s := range list.Sessions {
		if !s.Current {
			other = s.ID
		}
	}
	code(t, a.do("DELETE", "/api/identity/sessions/"+other, nil, cookie(first)), 204)
	code(t, a.do("GET", "/api/me", nil, cookie(second)), 401)
	code(t, a.do("DELETE", "/api/identity/sessions/"+other, nil, cookie(first)), 404)
}

func TestUsersHaveOnlyTheScopesTheyAreGiven(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "ann", "password": password, "scopes": []string{"demo.read"}}, cookie(owner)), 201)
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "ann", "password": password, "scopes": []string{"demo.read"}}, cookie(owner)), 409)
	ann := a.login("ann", password).cookie("demo_session")

	code(t, a.do("GET", "/api/me", nil, cookie(ann)), 200)
	code(t, a.do("GET", "/api/identity/users", nil, cookie(ann)), 403) // not an administrator
	code(t, a.do("GET", "/api/identity/users", nil, cookie(owner)), 200)

	// Ann cannot hand out what she does not hold, even if she were allowed to ask.
	var users struct {
		Users []struct{ ID, Username string }
	}
	r := a.do("GET", "/api/identity/users", nil, cookie(owner))
	r.json(&users)
	var annID string
	for _, u := range users.Users {
		if u.Username == "ann" {
			annID = u.ID
		}
	}
	code(t, a.do("PUT", "/api/identity/users/"+annID, map[string]any{"disabled": true}, cookie(owner)), 200)
	code(t, a.do("GET", "/api/me", nil, cookie(ann)), 401) // disabled ends the session at once
	code(t, a.login("ann", password), 401)
	code(t, a.do("PUT", "/api/identity/users/"+annID, map[string]any{"disabled": false}, cookie(owner)), 200)
	code(t, a.login("ann", password), 200)
}

func TestProfileCanBeEdited(t *testing.T) {
	a := start(t, "enforce", "")
	c := a.owner()
	var out struct {
		DisplayName string `json:"display_name"`
		Email       string
	}
	r := a.do("PUT", "/api/identity/profile", map[string]string{"display_name": "Chris", "email": "c@example.com"}, cookie(c))
	code(t, r, 200)
	r.json(&out)
	if out.DisplayName != "Chris" || out.Email != "c@example.com" {
		t.Errorf("profile = %+v", out)
	}
	code(t, a.do("PUT", "/api/identity/profile", map[string]string{"email": "not-an-email"}, cookie(c)), 400)
}

// ---- machines ----

func (a *app) key(owner *http.Cookie, body map[string]any) (raw, id string) {
	a.t.Helper()
	var out struct {
		Key        string
		Credential struct{ ID string }
	}
	r := a.do("POST", "/api/identity/keys", body, cookie(owner))
	code(a.t, r, 201)
	r.json(&out)
	return out.Key, out.Credential.ID
}

func TestAPIKeysAuthenticateAndCanBeRevoked(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, id := a.key(owner, map[string]any{"name": "ci", "scopes": []string{"demo.read"}})
	if !strings.HasPrefix(raw, "key_") {
		t.Fatalf("key = %q", raw)
	}
	var me struct{ Principal libauth.Principal }
	r := a.do("GET", "/api/me", nil, bearer(raw))
	code(t, r, 200)
	r.json(&me)
	if me.Principal.Kind != "service" || me.Principal.Display != "ci" || me.Principal.HasScope("demo.write") || !me.Principal.HasScope("demo.read") {
		t.Errorf("principal from the key = %+v", me.Principal)
	}
	code(t, a.do("GET", "/api/identity/keys", nil, bearer(raw)), 403) // the key is not an administrator
	code(t, a.do("POST", "/api/identity/keys", map[string]any{"name": "x", "scopes": []string{"demo.write"}}, cookie(owner)), 201)
	code(t, a.do("DELETE", "/api/identity/keys/"+id, nil, cookie(owner)), 204)
	code(t, a.do("GET", "/api/me", nil, bearer(raw)), 401)
}

func TestKeysCannotBeCreatedWithScopesTheCallerLacks(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	admin, _ := a.key(owner, map[string]any{"name": "admin-ish", "scopes": []string{"demo.admin", "demo.read"}})
	code(t, a.do("POST", "/api/identity/keys", map[string]any{"name": "more", "scopes": []string{"demo.read"}}, bearer(admin)), 201)
	code(t, a.do("POST", "/api/identity/keys", map[string]any{"name": "more", "scopes": []string{"demo.write"}}, bearer(admin)), 400)
	code(t, a.do("POST", "/api/identity/keys", map[string]any{"name": "more"}, bearer(admin)), 400)
}

func TestTokenMintingNarrowsScopesAndIsSelfVerifying(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, _ := a.key(owner, map[string]any{"name": "svc", "scopes": []string{"demo.read", "demo.write"}})

	var tok struct {
		Token     string
		ExpiresAt time.Time `json:"expires_at"`
		Scopes    []string
	}
	r := a.do("POST", "/api/token", map[string]any{"scopes": []string{"demo.read"}}, bearer(raw))
	code(t, r, 200)
	r.json(&tok)
	if strings.Count(tok.Token, ".") != 2 || time.Until(tok.ExpiresAt) > 16*time.Minute || len(tok.Scopes) != 1 {
		t.Fatalf("token reply = %+v", tok)
	}
	var me struct{ Principal libauth.Principal }
	r = a.do("GET", "/api/me", nil, bearer(tok.Token))
	code(t, r, 200)
	r.json(&me)
	if me.Principal.HasScope("demo.write") || !me.Principal.HasScope("demo.read") || me.Principal.TokenID == "" {
		t.Errorf("principal from the token = %+v", me.Principal)
	}
	code(t, a.do("POST", "/api/token", map[string]any{"scopes": []string{"demo.admin"}}, bearer(raw)), 403)
	code(t, a.do("POST", "/api/token", map[string]any{"audience": "other"}, bearer(raw)), 403)
	code(t, a.do("POST", "/api/token", nil), 401)
	// The reply has the shape auth.mint_url expects of an issuer.
	r = a.do("POST", "/api/token", map[string]any{}, cookie(owner))
	code(t, r, 200)
	var shape map[string]any
	r.json(&shape)
	for _, k := range []string{"token", "expires_at", "ttl_seconds"} {
		if shape[k] == nil {
			t.Errorf("token reply lacks %q: %v", k, shape)
		}
	}
}

// Another app trusts this one by pointing its verifier at the published key set.
func TestAnotherAppVerifiesTokensFromThePublishedKeys(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, _ := a.key(owner, map[string]any{"name": "to-hadron", "scopes": []string{"hadron.run"}, "audiences": []string{"hadron"}})

	var tok struct{ Token string }
	r := a.do("POST", "/api/token", map[string]any{"audience": "hadron"}, bearer(raw))
	code(t, r, 200)
	r.json(&tok)

	jwks := a.do("GET", "/api/identity/jwks", nil) // public: no credentials
	code(t, jwks, 200)
	keyFile := filepath.Join(t.TempDir(), "demo.jwks.json")
	if err := os.WriteFile(keyFile, jwks.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	verify := func(aud string) (libauth.Principal, error) {
		v, err := libauth.NewJWTVerifier(libauth.JWTConfig{Issuer: "demo", Audience: aud, BootstrapKeyFile: keyFile})
		if err != nil {
			t.Fatal(err)
		}
		return v.Verify(context.Background(), tok.Token)
	}
	p, err := verify("hadron")
	if err != nil || !p.Verified || !p.HasScope("hadron.run") {
		t.Fatalf("the audience app rejected the token: %+v, %v", p, err)
	}
	if _, err := verify("someone-else"); err == nil {
		t.Error("a token for one audience was accepted by another")
	}
	// And this app does not accept a token minted for another audience.
	code(t, a.do("GET", "/api/me", nil, bearer(tok.Token)), 401)
}

func TestPairingCodeIsRedeemedOnceForAKey(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	var pc struct{ Code string }
	r := a.do("POST", "/api/identity/pairing-codes", map[string]any{"name": "nanite", "scopes": []string{"demo.read"}, "ttl_hours": 24}, cookie(owner))
	code(t, r, 201)
	r.json(&pc)

	// Redeeming needs no credentials: the code is the credential.
	var paired struct {
		Key        string
		Credential struct {
			ExpiresAt time.Time `json:"expires_at"`
		}
	}
	r = a.do("POST", "/api/pair", map[string]string{"code": pc.Code})
	code(t, r, 201)
	r.json(&paired)
	code(t, a.do("GET", "/api/me", nil, bearer(paired.Key)), 200)
	if paired.Credential.ExpiresAt.IsZero() {
		t.Error("the key must carry the lifetime the code asked for")
	}
	code(t, a.do("POST", "/api/pair", map[string]string{"code": pc.Code}), 401)
	code(t, a.do("POST", "/api/pair", map[string]string{"code": "pair_nonsense"}), 401)
	code(t, a.do("POST", "/api/identity/pairing-codes", map[string]any{"name": "x"}, bearer(paired.Key)), 403)
}

func TestExpiredPairingCodeIsRefused(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	var pc struct{ Code string }
	r := a.do("POST", "/api/identity/pairing-codes", map[string]any{"name": "late", "scopes": []string{"demo.read"}}, cookie(owner))
	code(t, r, 201)
	r.json(&pc)
	if _, err := a.svc.DBForTest().ExecContext(context.Background(), `UPDATE identity_pairing_codes SET expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	code(t, a.do("POST", "/api/pair", map[string]string{"code": pc.Code}), 401)
}

// ---- modes and restarts ----

func TestOffModeStillRecognisesSessionsAndLeavesStrangersAsTheOperator(t *testing.T) {
	a := start(t, "off", "")
	var me struct{ Principal libauth.Principal }
	r := a.do("GET", "/api/me", nil)
	code(t, r, 200)
	r.json(&me)
	if me.Principal.Verified || me.Principal.ID != libauth.OffModePrincipalID {
		t.Errorf("a stranger in off mode = %+v", me.Principal)
	}
	c := a.owner()
	r = a.do("GET", "/api/me", nil, cookie(c))
	code(t, r, 200)
	r.json(&me)
	if !me.Principal.Verified || !strings.HasPrefix(me.Principal.ID, "user:") {
		t.Errorf("a signed-in user in off mode = %+v", me.Principal)
	}
}

func TestStateAndKeysSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	a := startIn(t, dir, "enforce", "")
	owner := a.owner()
	raw, _ := a.key(owner, map[string]any{"name": "svc", "scopes": []string{"demo.read"}})
	var tok struct{ Token string }
	r := a.do("POST", "/api/token", map[string]any{}, bearer(raw))
	r.json(&tok)
	_ = a.running.Stop(context.Background())

	b := startIn(t, dir, "enforce", "")
	code(t, b.do("GET", "/api/me", nil, cookie(owner)), 200)     // the session
	code(t, b.do("GET", "/api/me", nil, bearer(raw)), 200)       // the key
	code(t, b.do("GET", "/api/me", nil, bearer(tok.Token)), 200) // a token minted before: the same signing key
	code(t, b.do("POST", "/api/setup", map[string]string{"username": "again", "password": password}), 409)
}

func TestConfigErrorsAreRefused(t *testing.T) {
	for name, extra := range map[string]string{
		"bad ttl":     `, "token_ttl": "forever"`,
		"long ttl":    `, "token_ttl": "3h"`,
		"bad cookies": `, "secure_cookies": "sometimes"`,
		"bad cost":    `, "bcrypt_cost": 2`,
	} {
		dir := t.TempDir()
		cfg := fmt.Sprintf(`{"database": {"sqlite": {"path": %q}}, "auth": {"mode": "off"}, "identity": {"key_file": %q%s}}`, filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"), extra)
		if strings.Contains(extra, "bcrypt_cost") {
			cfg = strings.Replace(cfg, `"key_file"`, `"x": 1, "key_file"`, 1)
			cfg = strings.Replace(cfg, `, "bcrypt_cost": 2`, `, "bcrypt_cost": 2`, 1)
		}
		run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
		if err == nil {
			_ = run.Stop(context.Background())
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, `{"database":{"sqlite":{"path":":memory:"}}}`); err == nil {
		t.Error("selecting identity with no identity block was accepted")
	}
}

func TestRateLimitedAnswersCarryRetryAfter(t *testing.T) {
	a := start(t, "enforce", "")
	a.owner()
	for i := 0; i < 5; i++ {
		code(t, a.login("owner", "wrong password "+fmt.Sprint(i)), 401)
	}
	r := a.login("owner", password)
	code(t, r, 429)
	secs, err := strconv.Atoi(r.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 300 {
		t.Errorf("Retry-After = %q (%v), want 1..300 seconds", r.Header().Get("Retry-After"), err)
	}
	// An ordinary refusal carries none.
	b := start(t, "enforce", "")
	b.owner()
	w := b.login("owner", "wrong")
	code(t, w, 401)
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After on a 401 = %q", got)
	}
}

func TestPairingRedeemRateLimitCarriesRetryAfter(t *testing.T) {
	a := start(t, "enforce", "")
	a.owner()
	for i := 0; i < 5; i++ {
		code(t, a.do("POST", "/api/pair", map[string]string{"code": "pair_nonsense"}), 401)
	}
	r := a.do("POST", "/api/pair", map[string]string{"code": "pair_nonsense"})
	code(t, r, 429)
	if r.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on the pairing 429")
	}
}

func TestEmptyListsEncodeAsEmptyArraysNotNull(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	r := a.do("GET", "/api/identity/keys", nil, cookie(owner))
	code(t, r, 200)
	if got := strings.TrimSpace(r.Body.String()); got != `{"keys":[]}` {
		t.Errorf("empty keys = %s", got)
	}
	// With a key present the list is still a populated array.
	a.key(owner, map[string]any{"name": "ci", "scopes": []string{"demo.read"}})
	r = a.do("GET", "/api/identity/keys", nil, cookie(owner))
	if strings.Contains(r.Body.String(), `"keys":[]`) || strings.Contains(r.Body.String(), "null") {
		t.Errorf("keys = %s", r.Body)
	}
	// Users and sessions are never empty for a signed-in owner, so check the
	// helper every list route goes through.
	b, _ := json.Marshal(map[string]any{"a": identity.OrEmptyForTest[int](nil), "b": identity.OrEmptyForTest([]string{"x"})})
	if string(b) != `{"a":[],"b":["x"]}` {
		t.Errorf("orEmpty = %s", b)
	}
}
