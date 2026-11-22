package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/modules/identity"
)

func TestRecoveryLinkResetsSignIn(t *testing.T) {
	dir := t.TempDir()
	a := startIn(t, dir, "enforce", "")
	own := cookie(a.owner())
	enrollTOTP(t, a, own)

	// Offline: the CLI prints the link (stdout) and logs the minting.
	r := runCLI(t, dir, nil, "identity", "recover", "--user", "Owner", "--url", "https://app.example.test/")
	if r.err != nil {
		t.Fatalf("recover: %v %s", r.err, r.stderr)
	}
	link := strings.TrimSpace(r.stdout)
	token, ok := strings.CutPrefix(link, "https://app.example.test/recover#r=")
	if !ok || !strings.HasPrefix(token, "recover_") || !strings.Contains(r.logs, "recovery link minted") {
		t.Fatalf("link %q logs %q", link, r.logs)
	}
	if r := runCLI(t, dir, nil, "identity", "recover", "--user", "nobody"); r.err == nil {
		t.Fatal("a recovery link for an unknown user")
	}

	page := header("X-Requested-With", "x")
	var info struct {
		Username string `json:"username"`
		TOTP     bool   `json:"totp"`
	}
	w := a.do("POST", "/api/recover/check", map[string]string{"token": token}, page)
	code(t, w, 200)
	w.json(&info)
	if info.Username != "owner" || !info.TOTP {
		t.Fatalf("check %+v", info)
	}
	code(t, a.do("POST", "/api/recover/check", map[string]string{"token": token + "x"}, page), 403)

	apply := map[string]any{"token": token, "new_password": "a brand new password", "clear_totp": true}
	code(t, a.do("POST", "/api/recover", apply), 403)                                                 // no X-Requested-With
	code(t, a.do("POST", "/api/recover", apply, page, header("Origin", "https://evil.example")), 403) // another site
	code(t, a.do("POST", "/api/recover", map[string]any{"token": token, "new_password": "short"}, page), 400)
	w = a.do("POST", "/api/recover", apply, page)
	code(t, w, 200)
	var prof struct {
		SignIn string `json:"sign_in"`
		TOTP   bool   `json:"totp"`
	}
	a.do("GET", "/api/identity/profile", nil, cookie(w.cookie("demo_session"))).json(&prof)
	if prof.SignIn != "recovery" || prof.TOTP {
		t.Fatalf("profile after recovery %+v", prof)
	}
	// The old session ended; the old password no longer works; the new one needs no code.
	code(t, a.do("GET", "/api/identity/profile", nil, own), 401)
	code(t, a.login("owner", password), 401)
	code(t, a.do("POST", "/api/login", map[string]string{"username": "owner", "password": "a brand new password"}), 200)
	// The link works once.
	code(t, a.do("POST", "/api/recover", apply, page), 403)
}

func TestRecoveryLinkExpires(t *testing.T) {
	a := start(t, "enforce", "")
	a.owner()
	ctx := context.Background()
	if _, _, _, err := a.svc.RecoveryLink(ctx, "nobody", 0); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, _, _, err := a.svc.RecoveryLink(ctx, "owner", 25*time.Hour); err == nil {
		t.Fatal("a 25h link")
	}
	token, _, _, err := a.svc.RecoveryLink(ctx, "owner", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	code(t, a.do("POST", "/api/recover", map[string]any{"token": token}, header("X-Requested-With", "x")), 403)
}
