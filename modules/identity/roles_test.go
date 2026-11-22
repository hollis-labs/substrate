package identity_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"
)

func TestMigrationsAreInParity(t *testing.T) {
	if err := migrate.CheckParity(identity.Migrations); err != nil {
		t.Fatal(err)
	}
}

func profileOf(t *testing.T, a *app, opts ...opt) (role string, scopes []string) {
	t.Helper()
	w := a.do("GET", "/api/identity/profile", nil, opts...)
	code(t, w, 200)
	var p struct {
		Role   string   `json:"role"`
		Scopes []string `json:"scopes"`
	}
	w.json(&p)
	return p.Role, p.Scopes
}

func TestSetupLinkCreatesTheOwnerUnderOfflineSetup(t *testing.T) {
	a := start(t, "enforce", `, "setup": "offline"`)
	ctx := context.Background()
	body := map[string]string{"display_name": "Owner", "username": "owner", "password": password}

	// The open form stays refused.
	w := a.do("POST", "/api/setup", body)
	code(t, w, 403)
	var st struct {
		SetupLink bool `json:"setup_link"`
	}
	a.do("GET", "/api/setup", nil).json(&st)
	if !st.SetupLink {
		t.Fatal("GET /api/setup does not offer setup links")
	}

	tok, exp, err := a.svc.SetupLink(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) < 29*time.Minute {
		t.Fatalf("expires %s", exp)
	}
	withTok := map[string]string{"display_name": "Owner", "username": "owner", "password": password, "setup_token": tok}
	// A cross-site form cannot set X-Requested-With.
	code(t, a.do("POST", "/api/setup", withTok), 403)
	// A wrong token is refused.
	bad := map[string]string{"username": "owner", "password": password, "setup_token": "setup_nope"}
	code(t, a.do("POST", "/api/setup", bad, header("X-Requested-With", "x")), 403)

	w = a.do("POST", "/api/setup", withTok, header("X-Requested-With", "x"))
	code(t, w, 200)
	c := w.cookie("demo_session")
	if c == nil {
		t.Fatal("no session cookie")
	}
	role, scopes := profileOf(t, a, cookie(c))
	if role != identity.RoleOwner || !slices.Contains(scopes, "*") {
		t.Fatalf("role %q scopes %v", role, scopes)
	}
	// Single use, and no new links once there is an owner.
	withTok["username"] = "second"
	code(t, a.do("POST", "/api/setup", withTok, header("X-Requested-With", "x")), 403)
	if _, _, err := a.svc.SetupLink(ctx, 0); !errors.Is(err, identity.ErrSetupClosed) {
		t.Fatalf("setup link after owner: %v", err)
	}
	if got := identity.SetupURL("http://hub:8470/", tok); got != "http://hub:8470/setup#t="+tok {
		t.Fatalf("SetupURL %s", got)
	}
}

func TestSetupLinkExpires(t *testing.T) {
	a := start(t, "enforce", `, "setup": "offline"`)
	tok, _, err := a.svc.SetupLink(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	w := a.do("POST", "/api/setup", map[string]string{"username": "owner", "password": password, "setup_token": tok}, header("X-Requested-With", "x"))
	code(t, w, 403)
}

func TestRolesGrantScopes(t *testing.T) {
	a := start(t, "enforce", "")
	own := a.owner()

	// A new user is a member unless told otherwise.
	w := a.do("POST", "/api/identity/users", map[string]any{"username": "kid", "password": password}, cookie(own))
	code(t, w, 201)
	var u struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	w.json(&u)
	if u.Role != identity.RoleMember {
		t.Fatalf("role %q", u.Role)
	}
	lw := a.login("kid", password)
	code(t, lw, 200)
	kid := lw.cookie("demo_session")
	role, scopes := profileOf(t, a, cookie(kid))
	if role != identity.RoleMember || !slices.Contains(scopes, "demo.use") || !slices.Contains(scopes, "demo.read") || slices.Contains(scopes, "demo.admin") {
		t.Fatalf("member: %q %v", role, scopes)
	}
	code(t, a.do("GET", "/api/identity/users", nil, cookie(kid)), 403)

	// Promoted, the same session is an admin on its next request.
	code(t, a.do("PUT", "/api/identity/users/"+u.ID, map[string]any{"role": "admin"}, cookie(own)), 200)
	code(t, a.do("GET", "/api/identity/users", nil, cookie(kid)), 200)
	var list struct {
		Users []struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"users"`
	}
	a.do("GET", "/api/identity/users", nil, cookie(own)).json(&list)
	roles := map[string]string{}
	for _, x := range list.Users {
		roles[x.Username] = x.Role
	}
	if roles["owner"] != "owner" || roles["kid"] != "admin" {
		t.Fatalf("roles %v", roles)
	}

	// An admin cannot make an owner (it does not hold "*").
	w = a.do("POST", "/api/identity/users", map[string]any{"username": "boss", "password": password, "role": "owner"}, cookie(kid))
	code(t, w, 400)
	// Nonsense roles are refused.
	code(t, a.do("PUT", "/api/identity/users/"+u.ID, map[string]any{"role": "emperor"}, cookie(own)), 400)
	// The last owner keeps the role.
	var me struct {
		ID string `json:"id"`
	}
	a.do("GET", "/api/identity/profile", nil, cookie(own)).json(&me)
	code(t, a.do("PUT", "/api/identity/users/"+me.ID, map[string]any{"role": "member"}, cookie(own)), 409)

	if r, err := a.svc.Role(context.Background(), u.ID); err != nil || r != identity.RoleAdmin {
		t.Fatalf("Role: %q %v", r, err)
	}
	if pu, err := a.svc.User(context.Background(), u.ID); err != nil || pu.Role != identity.RoleAdmin {
		t.Fatalf("User: %+v %v", pu, err)
	}
}

func TestUpgradeWithTheCookieNeedsThisOrigin(t *testing.T) {
	a := start(t, "enforce", "")
	own := a.owner()
	ws := func(origin string) reply {
		opts := []opt{cookie(own), header("Upgrade", "websocket"), header("Connection", "Upgrade")}
		if origin != "" {
			opts = append(opts, header("Origin", origin))
		}
		return a.do("GET", "/api/identity/profile", nil, opts...)
	}
	code(t, ws(""), 403)
	code(t, ws("https://evil.example"), 403)
	code(t, ws("http://example.com"), 200) // httptest's host
	// A plain GET with the cookie still needs no Origin.
	code(t, a.do("GET", "/api/identity/profile", nil, cookie(own)), 200)
}

// A database made before numbered migrations (identity tables, no version table, no
// memberships) upgrades in place: users keep their accounts and get roles from scopes.
func TestUnversionedDatabaseUpgrades(t *testing.T) {
	dir := t.TempDir()
	a := startIn(t, dir, "enforce", "")
	own := a.owner()
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "ops", "password": password, "scopes": []string{"demo.admin"}}, cookie(own)), 201)
	code(t, a.do("POST", "/api/identity/users", map[string]any{"username": "viewer", "password": password, "scopes": []string{"demo.read"}}, cookie(own)), 201)
	st := database.From(a.running.App)
	for _, q := range []string{`DROP TABLE identity_schema`, `DROP TABLE identity_memberships`, `DROP TABLE identity_spaces`, `DROP TABLE identity_setup_tokens`,
		`DROP TABLE identity_passkeys`, `DROP TABLE identity_webauthn_challenges`,
		`ALTER TABLE identity_users DROP COLUMN passkey_only`, `ALTER TABLE identity_sessions DROP COLUMN method`} {
		if _, err := st.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.running.Stop(context.Background())

	b := startIn(t, dir, "enforce", "")
	var list struct {
		Users []struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"users"`
	}
	lw := b.login("owner", password)
	code(t, lw, 200)
	b.do("GET", "/api/identity/users", nil, cookie(lw.cookie("demo_session"))).json(&list)
	roles := map[string]string{}
	for _, x := range list.Users {
		roles[x.Username] = x.Role
	}
	if roles["owner"] != "owner" || roles["ops"] != "admin" || roles["viewer"] != "member" {
		t.Fatalf("roles after upgrade %v", roles)
	}
}

// Clearing a user's scopes clears them; it must not hand over the caller's.
func TestEmptyScopesClearInsteadOfCopyingTheCallers(t *testing.T) {
	a := start(t, "enforce", "")
	own := a.owner()
	w := a.do("POST", "/api/identity/users", map[string]any{"username": "kid", "password": password, "scopes": []string{"demo.read"}, "role": "guest"}, cookie(own))
	code(t, w, 201)
	var u struct {
		ID string `json:"id"`
	}
	w.json(&u)
	w = a.do("PUT", "/api/identity/users/"+u.ID, map[string]any{"scopes": []string{}}, cookie(own))
	code(t, w, 200)
	var got struct {
		Scopes []string `json:"scopes"`
	}
	w.json(&got)
	if len(got.Scopes) != 0 {
		t.Fatalf("scopes after clearing: %v", got.Scopes)
	}
	lw := a.login("kid", password)
	code(t, lw, 200)
	_, scopes := profileOf(t, a, cookie(lw.cookie("demo_session")))
	if slices.Contains(scopes, "*") || !slices.Contains(scopes, "demo.read") {
		t.Fatalf("effective scopes %v", scopes)
	}
}
