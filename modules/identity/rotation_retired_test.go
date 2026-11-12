package identity_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type retiredEntry struct {
	KID       string    `json:"kid"`
	X         string    `json:"x"`
	RetiredAt time.Time `json:"retired_at"`
	NotAfter  time.Time `json:"not_after"`
}

func readRetiredFile(t *testing.T, dir string) []retiredEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "id.key.retired.json"))
	if err != nil {
		t.Fatal(err)
	}
	var l []retiredEntry
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatalf("retired.json is not a list: %v: %s", err, b)
	}
	return l
}

// activePublicX is the base64url public key of the active key, from the published JWKS file.
func activePublicX(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "id.key.jwks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Keys []map[string]string }
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Keys) != 1 {
		t.Fatalf("jwks file: %v %s", err, b)
	}
	if _, err := base64.RawURLEncoding.DecodeString(doc.Keys[0]["x"]); err != nil {
		t.Fatal(err)
	}
	return doc.Keys[0]["x"]
}

// TestARotationThatFailsBeforeTheCommitPointLeavesTheActiveKeyListedOnlyTransiently pins
// what is on disk after a rotation fails at "before-commit" (the retired record is
// written first, so the still-active key is listed there) and what the next start
// does with it. Order in RotateSigningKey: retired record, staged JWKS, key_file.new,
// before-commit hook, rename (commit). The error return removes key_file.new and
// changes nothing in memory; it does not rewrite the retired record.
func TestARotationThatFailsBeforeTheCommitPointLeavesTheActiveKeyListedOnlyTransiently(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	old := a.token(owner)
	active := a.keyState(owner).Active.KID
	activeX := activePublicX(t, a.dir)
	keyBytes, _ := os.ReadFile(filepath.Join(a.dir, "id.key"))

	a.svc.SetFaultForTest(func(p string) error {
		if p == "before-commit" {
			return errors.New("disk full")
		}
		return nil
	})
	code(t, a.do("POST", "/api/identity/signing-key/rotate", map[string]any{}, cookie(owner)), 500)
	a.svc.SetFaultForTest(nil)

	// On disk after the failure: the still-active key IS listed as retired, with its
	// real public key and a not_after of retired_key_ttl (70m) from retired_at.
	got := readRetiredFile(t, a.dir)
	if len(got) != 1 || got[0].KID != active || got[0].X != activeX {
		t.Fatalf("retired.json after the failed rotation = %+v, want exactly the active key %s", got, active)
	}
	if d := got[0].NotAfter.Sub(got[0].RetiredAt); d != 70*time.Minute {
		t.Errorf("not_after - retired_at = %v, want the 70m floor", d)
	}
	// Everything else is as it was: key_file, the in-memory ring, the staged leftovers.
	if now, _ := os.ReadFile(filepath.Join(a.dir, "id.key")); string(now) != string(keyBytes) {
		t.Error("key_file changed")
	}
	if _, err := os.Stat(filepath.Join(a.dir, "id.key.new")); err == nil {
		t.Error("id.key.new was left behind")
	}
	if st := a.keyState(owner); st.Active.KID != active || len(st.Retired) != 0 {
		t.Errorf("in-memory ring after the failure = %+v, want the old active key and no retired", st)
	}
	if served := a.servedKids(); len(served) != 1 || served[0] != active {
		t.Errorf("served kids = %v, want only the active key", served)
	}
	_ = a.running.Stop(context.Background())

	// The next start: loadKey sees a retired entry equal to the active key, drops it
	// and rewrites the file (an empty list, not a stale one), and nothing is lost.
	b := startIn(t, a.dir, "enforce", "")
	if got := readRetiredFile(t, b.dir); len(got) != 0 {
		t.Errorf("retired.json after the next start = %+v, want the stale entry dropped", got)
	}
	if st := b.keyState(owner); st.Active.KID != active || len(st.Retired) != 0 {
		t.Errorf("ring after the next start = %+v", st)
	}
	if served := b.servedKids(); len(served) != 1 || served[0] != active {
		t.Errorf("served kids after the next start = %v", served)
	}
	if b.me(old) != 200 {
		t.Error("a token minted before the failed rotation must verify")
	}
}

// A retry after the failed rotation rewrites the retired record from memory, so the
// stale entry is replaced, never duplicated.
func TestARetryAfterAFailedRotationDoesNotDuplicateTheRetiredEntry(t *testing.T) {
	a := start(t, "enforce", "")
	owner := a.owner()
	old := a.token(owner)
	active := a.keyState(owner).Active.KID
	a.svc.SetFaultForTest(func(p string) error {
		if p == "before-commit" {
			return errors.New("disk full")
		}
		return nil
	})
	code(t, a.do("POST", "/api/identity/signing-key/rotate", map[string]any{}, cookie(owner)), 500)
	code(t, a.do("POST", "/api/identity/signing-key/rotate", map[string]any{}, cookie(owner)), 500)
	a.svc.SetFaultForTest(nil)
	res := a.rotate(owner, false)
	got := readRetiredFile(t, a.dir)
	if len(got) != 1 || got[0].KID != active || res.RetiredKID != active {
		t.Fatalf("retired.json after fail, fail, success = %+v (retired_kid %s), want the old key once", got, res.RetiredKID)
	}
	if a.me(old) != 200 {
		t.Error("the old token must verify through the overlap")
	}
}
