package identity_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
)

// ---- helpers ----

func (a *app) token(owner *http.Cookie) string {
	a.t.Helper()
	var tok struct{ Token string }
	r := a.do("POST", "/api/token", map[string]any{}, cookie(owner))
	code(a.t, r, 200)
	r.json(&tok)
	return tok.Token
}

func (a *app) me(tok string) int { return a.do("GET", "/api/me", nil, bearer(tok)).Code }

type rotated struct {
	KID        string    `json:"kid"`
	RetiredKID string    `json:"retired_kid"`
	NotAfter   time.Time `json:"not_after"`
}

func (a *app) rotate(owner *http.Cookie, compromised bool) rotated {
	a.t.Helper()
	var out rotated
	r := a.do("POST", "/api/identity/signing-key/rotate", map[string]any{"compromised": compromised}, cookie(owner))
	code(a.t, r, 200)
	r.json(&out)
	return out
}

type keyState struct {
	Active  struct{ KID string } `json:"active"`
	Retired []struct {
		KID      string
		NotAfter time.Time `json:"not_after"`
	}
}

func (a *app) keyState(owner *http.Cookie) keyState {
	a.t.Helper()
	var st keyState
	r := a.do("GET", "/api/identity/signing-key", nil, cookie(owner))
	code(a.t, r, 200)
	r.json(&st)
	return st
}

func kids(t *testing.T, raw []byte) []string {
	t.Helper()
	var doc struct{ Keys []struct{ Kid string } }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, k := range doc.Keys {
		out = append(out, k.Kid)
	}
	return out
}

func (a *app) servedKids() []string {
	a.t.Helper()
	r := a.do("GET", "/api/identity/jwks", nil)
	code(a.t, r, 200)
	return kids(a.t, r.Body.Bytes())
}

func fileKids(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return kids(t, b)
}

func retiredFileKids(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "id.key.retired.json"))
	if err != nil {
		t.Fatal(err)
	}
	var l []struct{ KID string }
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range l {
		out = append(out, e.KID)
	}
	return out
}

func eq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// remoteVerifier is a second app: it trusts this one through jwks_url.
func remoteVerifier(t *testing.T, a *app) *libauth.JWTVerifier {
	t.Helper()
	srv := httptest.NewServer(a.h)
	t.Cleanup(srv.Close)
	v, err := libauth.NewJWTVerifier(libauth.JWTConfig{Issuer: "demo", Audience: "demo", JWKSURL: srv.URL + "/api/identity/jwks", MinRefresh: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return v
}

func verifies(v *libauth.JWTVerifier, tok string) bool {
	_, err := v.Verify(context.Background(), tok)
	return err == nil
}

func tokenHeader(t *testing.T, tok string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- the overlap ----

func TestRotationKeepsOldTokensVerifyingThroughTheOverlap(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	remote := remoteVerifier(t, a)
	old := a.token(owner)
	before := a.keyState(owner).Active.KID
	if !verifies(remote, old) {
		t.Fatal("the second app must accept the token before rotation")
	}

	res := a.rotate(owner, false)
	if res.RetiredKID != before || res.KID == before || res.KID == "" {
		t.Fatalf("rotation reply %+v, active before was %s", res, before)
	}
	if got := time.Until(res.NotAfter); got < 69*time.Minute || got > 71*time.Minute {
		t.Errorf("not_after is %s away, want about 70m", got)
	}
	fresh := a.token(owner)

	// This module: old and new tokens both verify.
	if a.me(old) != 200 || a.me(fresh) != 200 {
		t.Fatal("old and new tokens must both verify in the module through the overlap")
	}
	// The published set lists both keys, new first, and so does the file.
	want := []string{res.KID, before}
	if got := a.servedKids(); !eq(got, want) {
		t.Errorf("served kids %v, want %v", got, want)
	}
	if got := fileKids(t, filepath.Join(a.dir, "id.key.jwks.json")); !eq(got, want) {
		t.Errorf("jwks file kids %v, want %v", got, want)
	}
	if st := a.keyState(owner); st.Active.KID != res.KID || len(st.Retired) != 1 || st.Retired[0].KID != before {
		t.Errorf("signing-key state %+v", st)
	}
	// A second app that trusts this one by jwks_url sees both after its next fetch.
	if err := remote.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !verifies(remote, old) || !verifies(remote, fresh) {
		t.Error("the jwks_url verifier must accept old and new tokens through the overlap")
	}
	// Tokens minted after the rotation carry the new kid.
	if !strings.Contains(tokenHeader(t, fresh), res.KID) || !strings.Contains(tokenHeader(t, old), before) {
		t.Error("token kids do not match the keys that signed them")
	}
}

func TestRetiredKeyLeavesTheSetAfterNotAfterAndTheSweepPrunesTheFile(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	now := atomic.Int64{}
	now.Store(time.Now().UnixNano())
	a.svc.SetClockForTest(func() time.Time { return time.Unix(0, now.Load()) })
	remote := remoteVerifier(t, a)

	old := a.token(owner)
	res := a.rotate(owner, false)
	if err := remote.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	now.Add(int64(69 * time.Minute)) // inside the overlap
	if a.me(old) != 200 {
		t.Fatal("the old token must verify until not_after")
	}
	now.Add(int64(2 * time.Minute)) // 71 minutes after the rotation
	// The sweep step first, before anything else reads the set: it prunes the file.
	if got := retiredFileKids(t, a.dir); len(got) != 1 {
		t.Fatalf("before the sweep the retired file lists %v", got)
	}
	if err := a.svc.PruneForTest(); err != nil {
		t.Fatal(err)
	}
	if got := retiredFileKids(t, a.dir); len(got) != 0 {
		t.Errorf("the sweep left %v in the retired file", got)
	}
	if got := fileKids(t, filepath.Join(a.dir, "id.key.jwks.json")); !eq(got, []string{res.KID}) {
		t.Errorf("jwks file after the sweep: %v", got)
	}
	if a.me(old) != 401 {
		t.Error("after not_after the old key must not verify")
	}
	if got := a.servedKids(); !eq(got, []string{res.KID}) {
		t.Errorf("served kids after not_after: %v", got)
	}
	if err := remote.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if verifies(remote, old) {
		t.Error("the jwks_url verifier must drop the old key once it is gone from the set")
	}
	if a.me(a.token(owner)) != 200 {
		t.Error("new tokens keep working")
	}
}

func TestExpiryIsEnforcedWithoutWaitingForTheSweep(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	now := atomic.Int64{}
	now.Store(time.Now().UnixNano())
	a.svc.SetClockForTest(func() time.Time { return time.Unix(0, now.Load()) })
	old := a.token(owner)
	a.rotate(owner, false)
	now.Add(int64(71 * time.Minute))
	if a.me(old) != 401 {
		t.Error("a verification after not_after must refuse the retired key even before the hourly sweep ran")
	}
}

func TestCompromisedRotationCutsTheOldKeyAtOnce(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	remote := remoteVerifier(t, a)
	old := a.token(owner)
	res := a.rotate(owner, true)
	if a.me(old) != 401 {
		t.Error("compromised: the old token must fail at once")
	}
	if got := a.servedKids(); !eq(got, []string{res.KID}) {
		t.Errorf("served kids %v, want only %s", got, res.KID)
	}
	if st := a.keyState(owner); len(st.Retired) != 0 {
		t.Errorf("retired = %+v, want none", st.Retired)
	}
	if got := retiredFileKids(t, a.dir); len(got) != 0 {
		t.Errorf("retired file lists %v", got)
	}
	if err := remote.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if verifies(remote, old) {
		t.Error("the jwks_url verifier must refuse the old token after its refetch")
	}
	// It stays cut across a restart.
	_ = a.running.Stop(context.Background())
	b := startIn(t, a.dir, "enforce", "")
	if b.me(old) != 401 || b.me(b.token(owner)) != 200 {
		t.Error("after a restart the compromised key must stay out and the new one work")
	}
}

func TestOnlyAdministratorsMayRotate(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	raw, _ := a.key(owner, map[string]any{"name": "reader", "scopes": []string{"demo.read"}})
	adminKey, _ := a.key(owner, map[string]any{"name": "adm", "scopes": []string{"demo.admin"}})
	for _, route := range [][2]string{{"POST", "/api/identity/signing-key/rotate"}, {"GET", "/api/identity/signing-key"}} {
		code(t, a.do(route[0], route[1], nil), 401)
		code(t, a.do(route[0], route[1], nil, bearer(raw)), 403)
	}
	kid := a.keyState(owner).Active.KID
	code(t, a.do("POST", "/api/identity/signing-key/rotate", nil, bearer(raw)), 403)
	if a.keyState(owner).Active.KID != kid {
		t.Fatal("a refused call must not rotate")
	}
	code(t, a.do("POST", "/api/identity/signing-key/rotate", map[string]any{}, bearer(adminKey)), 200) // {app}.admin suffices
	code(t, a.do("POST", "/api/identity/signing-key/rotate", map[string]any{"bogus": 1}, cookie(owner)), 400)
}

// ---- restart and crash points ----

func TestRotationSurvivesARestart(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	old := a.token(owner)
	res := a.rotate(owner, false)
	fresh := a.token(owner)
	served := a.servedKids()
	_ = a.running.Stop(context.Background())

	b := startIn(t, a.dir, "enforce", "")
	if b.me(old) != 200 || b.me(fresh) != 200 {
		t.Error("old and new tokens must verify after a restart")
	}
	if got := b.servedKids(); !eq(got, served) {
		t.Errorf("jwks changed across the restart: %v then %v", served, got)
	}
	if st := b.keyState(owner); st.Active.KID != res.KID || len(st.Retired) != 1 {
		t.Errorf("state after restart %+v", st)
	}
}

type crash struct{}

func rotateCrashing(t *testing.T, a *app, point string) {
	t.Helper()
	a.svc.SetFaultForTest(func(p string) error {
		if p == point {
			panic(crash{})
		}
		return nil
	})
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("the hook at %q never fired", point)
		} else if _, ok := r.(crash); !ok {
			panic(r)
		}
	}()
	_, _ = a.svc.RotateSigningKey(false)
}

func TestCrashOnEitherSideOfTheCommitPointLosesNoToken(t *testing.T) {
	for _, tc := range []struct {
		point       string
		wantRotated bool
	}{
		{"before-commit", false}, // retired record written, key_file untouched
		{"after-commit", true},   // key_file is the new key, JWKS file not yet rewritten
		{"after-jwks", true},     // everything on disk, process died before the swap
	} {
		t.Run(tc.point, func(t *testing.T) {
			a := start(t, "enforce", "")
			owner := a.owner()
			old := a.token(owner)
			before := a.keyState(owner).Active.KID
			rotateCrashing(t, a, tc.point)
			_ = a.running.Stop(context.Background())

			b := startIn(t, a.dir, "enforce", "")
			st := b.keyState(owner)
			if (st.Active.KID != before) != tc.wantRotated {
				t.Fatalf("active %s, before %s, rotated=%v expected", st.Active.KID, before, tc.wantRotated)
			}
			if b.me(old) != 200 {
				t.Error("a token minted before the crash must still verify")
			}
			if b.me(b.token(owner)) != 200 {
				t.Error("a token minted after recovery must verify")
			}
			wantServed := 1
			if tc.wantRotated {
				wantServed = 2
			}
			if got := b.servedKids(); len(got) != wantServed {
				t.Errorf("served kids %v, want %d", got, wantServed)
			}
			if got := fileKids(t, filepath.Join(b.dir, "id.key.jwks.json")); len(got) != wantServed {
				t.Errorf("jwks file kids %v, want %d", got, wantServed)
			}
			if !tc.wantRotated && len(st.Retired) != 0 {
				t.Errorf("a stale retired entry for the active key survived: %+v", st.Retired)
			}
			if _, err := os.Stat(filepath.Join(b.dir, "id.key.new")); err == nil {
				t.Error("a leftover id.key.new survived the restart")
			}
		})
	}
}

func TestTheRetiredRecordIsOnDiskBeforeTheKeyIsReplaced(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	before := a.keyState(owner).Active.KID
	var seen []string
	a.svc.SetFaultForTest(func(p string) error {
		if p == "before-commit" {
			seen = retiredFileKids(t, a.dir)
		}
		return nil
	})
	a.rotate(owner, false)
	if !eq(seen, []string{before}) {
		t.Errorf("at the commit point the retired file lists %v, want the old key %s", seen, before)
	}
}

func TestAWriteErrorBeforeTheCommitPointChangesNothing(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	old := a.token(owner)
	before := a.keyState(owner).Active.KID
	keyBytes, _ := os.ReadFile(filepath.Join(a.dir, "id.key"))
	a.svc.SetFaultForTest(func(p string) error {
		if p == "before-commit" {
			return errors.New("disk full")
		}
		return nil
	})
	code(t, a.do("POST", "/api/identity/signing-key/rotate", map[string]any{}, cookie(owner)), 500)
	a.svc.SetFaultForTest(nil)
	if a.keyState(owner).Active.KID != before {
		t.Error("the active key changed after a failed rotation")
	}
	if now, _ := os.ReadFile(filepath.Join(a.dir, "id.key")); string(now) != string(keyBytes) {
		t.Error("key_file changed after a failed rotation")
	}
	if a.me(old) != 200 || a.me(a.token(owner)) != 200 {
		t.Error("tokens must keep working after a failed rotation")
	}
	a.rotate(owner, false) // and a retry succeeds
}

func TestStartRecoversAndRefuses(t *testing.T) {
	t.Run("a retired entry equal to the active key is dropped", func(t *testing.T) {
		a := start(t, "enforce", "")
		owner := a.owner()
		old := a.token(owner)
		active := a.keyState(owner).Active.KID
		jw, _ := os.ReadFile(filepath.Join(a.dir, "id.key.jwks.json"))
		var doc struct{ Keys []map[string]string }
		_ = json.Unmarshal(jw, &doc)
		entry := fmt.Sprintf(`[{"kid":%q,"x":%q,"retired_at":"2026-01-01T00:00:00Z","not_after":"2999-01-01T00:00:00Z"}]`, active, doc.Keys[0]["x"])
		_ = a.running.Stop(context.Background())
		if err := os.WriteFile(filepath.Join(a.dir, "id.key.retired.json"), []byte(entry), 0o600); err != nil {
			t.Fatal(err)
		}
		b := startIn(t, a.dir, "enforce", "")
		if st := b.keyState(owner); len(st.Retired) != 0 {
			t.Errorf("stale entry kept: %+v", st.Retired)
		}
		if got := retiredFileKids(t, b.dir); len(got) != 0 {
			t.Errorf("stale entry left in the file: %v", got)
		}
		if got := b.servedKids(); len(got) != 1 {
			t.Errorf("served %v", got)
		}
		if b.me(old) != 200 {
			t.Error("tokens of the active key keep verifying")
		}
	})
	for name, body := range map[string]string{
		"not json":     `{{{`,
		"bad key":      `[{"kid":"abc","x":"AAAA","retired_at":"2026-01-01T00:00:00Z","not_after":"2999-01-01T00:00:00Z"}]`,
		"no not_after": `[{"kid":"abc","x":"AAAA"}]`,
		"not a list":   `{"kid":"x"}`,
		"truncated":    `[{"kid":"abc"`,
	} {
		t.Run("corrupt "+name+" refuses to start", func(t *testing.T) {
			dir := t.TempDir()
			a := startIn(t, dir, "enforce", "")
			_ = a.running.Stop(context.Background())
			if err := os.WriteFile(filepath.Join(dir, "id.key.retired.json"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := fmt.Sprintf(`{"database": {"sqlite": {"path": %q}}, "auth": {"mode": "enforce"}, "identity": {"key_file": %q, "bcrypt_cost": 4}}`, filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"))
			run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
			if err == nil {
				_ = run.Stop(context.Background())
				t.Fatal("started with a corrupt retired file")
			}
			if !strings.Contains(err.Error(), "retired.json") {
				t.Errorf("the error does not name the file: %v", err)
			}
		})
	}
}

func TestRetiredKeyTTLHasAFloorOfSeventyMinutes(t *testing.T) {
	for _, ttl := range []string{"69m", "1h", "1s"} {
		dir := t.TempDir()
		cfg := fmt.Sprintf(`{"database": {"sqlite": {"path": %q}}, "auth": {"mode": "off"}, "identity": {"key_file": %q, "bcrypt_cost": 4, "retired_key_ttl": %q}}`, filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"), ttl)
		run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
		if err == nil {
			_ = run.Stop(context.Background())
			t.Errorf("retired_key_ttl %s was accepted", ttl)
			continue
		}
		if !strings.Contains(err.Error(), "retired_key_ttl") {
			t.Errorf("the error for %s does not name the key: %v", ttl, err)
		}
	}
	a := start(t, "enforce", `, "retired_key_ttl": "3h"`)
	owner := a.owner()
	if got := time.Until(a.rotate(owner, false).NotAfter); got < 179*time.Minute {
		t.Errorf("a configured 3h overlap gave %s", got)
	}
}

// ---- concurrency ----

func TestConcurrentRotationsGiveOneSuccessAndOneConflict(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	entered, release := make(chan struct{}), make(chan struct{})
	a.svc.SetFaultForTest(func(p string) error {
		if p == "before-commit" {
			close(entered)
			<-release
		}
		return nil
	})
	first := make(chan int)
	go func() {
		first <- a.do("POST", "/api/identity/signing-key/rotate", map[string]any{}, cookie(owner)).Code
	}()
	<-entered // the first rotation is mid-flight
	code(t, a.do("POST", "/api/identity/signing-key/rotate", map[string]any{}, cookie(owner)), 409)
	close(release)
	if c := <-first; c != 200 {
		t.Fatalf("the first rotation: %d", c)
	}
	if st := a.keyState(owner); len(st.Retired) != 1 {
		t.Errorf("exactly one key should be retired: %+v", st.Retired)
	}
}

func TestMintAndVerifyWhileRotating(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	seed := a.token(owner)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var failures atomic.Int64
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				var tok struct{ Token string }
				r := a.do("POST", "/api/token", map[string]any{}, cookie(owner))
				if r.Code != 200 {
					failures.Add(1)
					continue
				}
				r.json(&tok)
				if a.me(tok.Token) != 200 || a.me(seed) != 200 {
					failures.Add(1)
				}
				a.do("GET", "/api/identity/jwks", nil)
			}
		}()
	}
	for i := 0; i < 5; i++ {
		if _, err := a.svc.RotateSigningKey(false); err != nil {
			t.Error(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Errorf("%d mint or verify calls failed during rotation", n)
	}
	if st := a.keyState(owner); len(st.Retired) != 5 {
		t.Errorf("five rotations should leave five retired keys, got %d", len(st.Retired))
	}
}
