package identity_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/identity"
)

// RFC 6238 appendix B, SHA1, truncated to six digits.
func TestTOTPMatchesTheRFCVectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for at, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := identity.TOTPCodeForTest(secret, time.Unix(at, 0)); got != want {
			t.Errorf("at %d: %s, want %s", at, got, want)
		}
	}
}

type enrolled struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QR     string `json:"qr_svg"`
}

// enrollTOTP turns TOTP on for the cookie's account and returns the secret and recovery codes.
func enrollTOTP(t *testing.T, a *app, c opt) (secret string, recovery []string, used string) {
	t.Helper()
	var e enrolled
	w := a.do("POST", "/api/identity/totp/begin", map[string]any{}, c)
	code(t, w, 200)
	w.json(&e)
	if !strings.HasPrefix(e.URI, "otpauth://totp/demo:") || !strings.Contains(e.URI, "secret="+e.Secret) || !strings.HasPrefix(e.QR, "<svg") {
		t.Fatalf("enrollment %+v", e)
	}
	code(t, a.do("POST", "/api/identity/totp/confirm", map[string]string{"code": "000000"}, c), 401)
	used = identity.TOTPCodeForTest(e.Secret, time.Now())
	w = a.do("POST", "/api/identity/totp/confirm", map[string]string{"code": used}, c)
	code(t, w, 200)
	var rc struct {
		Codes []string `json:"recovery_codes"`
	}
	w.json(&rc)
	if len(rc.Codes) != 10 || len(rc.Codes[0]) != 19 {
		t.Fatalf("recovery codes %v", rc.Codes)
	}
	return e.Secret, rc.Codes, used
}

func loginCode(a *app, user, code string) reply {
	return a.do("POST", "/api/login", map[string]string{"username": user, "password": password, "code": code})
}

func TestTOTPSignIn(t *testing.T) {
	a := start(t, "enforce", "")
	own := cookie(a.owner())
	secret, recovery, used := enrollTOTP(t, a, own)
	code(t, a.do("POST", "/api/identity/totp/begin", map[string]any{}, own), 409) // already on

	// The password alone now asks for the code; a wrong code is refused.
	w := a.login("owner", password)
	code(t, w, 401)
	if !strings.Contains(w.Body.String(), "totp_required") {
		t.Fatalf("no totp_required: %s", w.Body)
	}
	code(t, loginCode(a, "owner", "123456"), 401)
	code(t, a.do("POST", "/api/login", map[string]string{"username": "owner", "password": "wrong password!", "code": identity.TOTPCodeForTest(secret, time.Now().Add(30*time.Second))}), 401)

	// The code that confirmed the enrollment cannot be used again; the next one can, once.
	code(t, loginCode(a, "owner", used), 401)
	next := identity.TOTPCodeForTest(secret, time.Now().Add(30*time.Second))
	w = loginCode(a, "owner", next)
	code(t, w, 200)
	sess := cookie(w.cookie("demo_session"))
	var prof struct {
		SignIn string `json:"sign_in"`
		TOTP   bool   `json:"totp"`
	}
	a.do("GET", "/api/identity/profile", nil, sess).json(&prof)
	if prof.SignIn != "password+totp" || !prof.TOTP {
		t.Fatalf("profile %+v", prof)
	}
	code(t, loginCode(a, "owner", next), 401)

	// A recovery code works once, typed any way.
	w = loginCode(a, "owner", strings.ToUpper(strings.ReplaceAll(recovery[0], "-", " ")))
	code(t, w, 200)
	code(t, loginCode(a, "owner", recovery[0]), 401)
	var st struct {
		Enabled bool `json:"enabled"`
		Left    int  `json:"recovery_codes_left"`
	}
	a.do("GET", "/api/identity/totp", nil, own).json(&st)
	if !st.Enabled || st.Left != 9 {
		t.Fatalf("status %+v", st)
	}

	// A new set of recovery codes (it takes a code) replaces the old.
	w = a.do("POST", "/api/identity/totp/recovery-codes", map[string]string{"code": recovery[1]}, own)
	code(t, w, 200)
	code(t, loginCode(a, "owner", recovery[2]), 401)

	// Turning it off takes a code; then the password alone signs in again.
	code(t, a.do("DELETE", "/api/identity/totp", map[string]string{"code": "000000"}, own), 401)
	var fresh struct {
		Codes []string `json:"recovery_codes"`
	}
	w.json(&fresh)
	code(t, a.do("DELETE", "/api/identity/totp", map[string]string{"code": fresh.Codes[0]}, own), 204)
	code(t, a.login("owner", password), 200)
}

func TestAdminResetsTOTP(t *testing.T) {
	a := start(t, "enforce", "")
	own := cookie(a.owner())
	w := a.do("POST", "/api/identity/users", map[string]any{"username": "sam", "password": password}, own)
	code(t, w, 201)
	var sam struct {
		ID string `json:"id"`
	}
	w.json(&sam)
	w = a.login("sam", password)
	code(t, w, 200)
	enrollTOTP(t, a, cookie(w.cookie("demo_session")))
	code(t, a.login("sam", password), 401)

	code(t, a.do("PUT", "/api/identity/users/"+sam.ID, map[string]any{"reset_totp": true}, own), 200)
	code(t, a.login("sam", password), 200)
	// Your own needs a code, not the admin route.
	var me struct {
		ID string `json:"id"`
	}
	a.do("GET", "/api/identity/profile", nil, own).json(&me)
	code(t, a.do("PUT", "/api/identity/users/"+me.ID, map[string]any{"reset_totp": true}, own), 400)
}

func TestRequiredSecondFactorLimitsTheSessionUntilEnrolled(t *testing.T) {
	a := start(t, "enforce", `, "require_second_factor": ["owner", "admin"]`)
	own := cookie(a.owner())
	var prof struct {
		Scopes   []string `json:"scopes"`
		Required bool     `json:"second_factor_required"`
	}
	a.do("GET", "/api/identity/profile", nil, own).json(&prof)
	if !prof.Required || len(prof.Scopes) != 0 {
		t.Fatalf("profile before enrolling %+v", prof)
	}
	// Admin routes are closed; enrolling is open.
	code(t, a.do("GET", "/api/identity/users", nil, own), 403)
	secret, _, _ := enrollTOTP(t, a, own)
	code(t, a.do("GET", "/api/identity/users", nil, own), 200)

	// The role keeps TOTP on: with no passkey it cannot be turned off.
	w := a.do("DELETE", "/api/identity/totp", map[string]string{"code": identity.TOTPCodeForTest(secret, time.Now().Add(30*time.Second))}, own)
	code(t, w, 409)

	// Members are not required.
	w = a.do("POST", "/api/identity/users", map[string]any{"username": "sam", "password": password, "role": "member"}, own)
	code(t, w, 201)
	w = a.login("sam", password)
	code(t, w, 200)
	a.do("GET", "/api/identity/profile", nil, cookie(w.cookie("demo_session"))).json(&prof)
	if prof.Required {
		t.Fatalf("a member was required a second factor: %+v", prof)
	}
}

func TestRequireSecondFactorRejectsUnknownRoles(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"database": {"sqlite": {"path": "` + filepath.Join(dir, "t.db") + `"}}, "auth": {"mode": "enforce"},
		"identity": {"key_file": "` + filepath.Join(dir, "id.key") + `", "setup": "http", "require_second_factor": ["boss"]}}`
	run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
	if err == nil {
		_ = run.Stop(context.Background())
	}
	if err == nil || !strings.Contains(err.Error(), "boss") {
		t.Fatalf("err %v", err)
	}
}
