package identity_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
)

// tryStart boots the identity module on dir with key_file at keyFile.
func tryStart(t *testing.T, dir, keyFile, extra string) error {
	t.Helper()
	cfg := fmt.Sprintf(`{"database": {"sqlite": {"path": %q}}, "auth": {"mode": "enforce"}, "identity": {"key_file": %q, "bcrypt_cost": 4%s}}`,
		filepath.Join(dir, "t.db"), keyFile, extra)
	run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
	if err == nil {
		_ = run.Stop(context.Background())
	}
	return err
}

func TestSecondIssuerGuard(t *testing.T) {
	// A database with identity rows and no key file where the config points: refused.
	dir := t.TempDir()
	a := startIn(t, dir, "enforce", "")
	a.owner() // an owner row
	_ = a.running.Stop(context.Background())
	elsewhere := filepath.Join(t.TempDir(), "other.key") // a child with its own -db/key_file
	err := tryStart(t, dir, elsewhere, "")
	if err == nil {
		t.Fatal("a second signing key was created beside existing identity rows")
	}
	if !strings.Contains(err.Error(), elsewhere) || !strings.Contains(err.Error(), "second issuer") {
		t.Errorf("the error must name key_file and the problem: %v", err)
	}
	if _, serr := os.Stat(elsewhere); serr == nil {
		t.Error("the refused start left a key file behind")
	}

	// The service's own key file, still there: starts as before and keeps its key.
	before, _ := os.ReadFile(filepath.Join(dir, "id.key"))
	if err := tryStart(t, dir, filepath.Join(dir, "id.key"), ""); err != nil {
		t.Fatalf("restart with the existing key: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "id.key")); string(after) != string(before) {
		t.Error("the key changed on restart")
	}

	// The deliberate way to a new key.
	if err := tryStart(t, dir, elsewhere, `, "allow_new_signing_key": true`); err != nil {
		t.Fatalf("allow_new_signing_key: %v", err)
	}
	if _, serr := os.Stat(elsewhere); serr != nil {
		t.Error("allow_new_signing_key created no key")
	}
}

func TestSecondIssuerGuardCountsAPIKeys(t *testing.T) {
	dir := t.TempDir()
	a := startIn(t, dir, "enforce", "")
	a.key(a.owner(), map[string]any{"name": "svc", "scopes": []string{"demo.read"}})
	// Drop the owner row so only the API key row is left.
	if _, err := database.From(a.running.App).ExecContext(context.Background(), `DELETE FROM identity_users`); err != nil {
		t.Fatal(err)
	}
	_ = a.running.Stop(context.Background())
	if err := tryStart(t, dir, filepath.Join(t.TempDir(), "other.key"), ""); err == nil {
		t.Fatal("an API key row alone must also block a second key")
	}
}

func TestSecondIssuerGuardAllowsAFreshDatabase(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "fresh.key")
	if err := tryStart(t, dir, key, ""); err != nil {
		t.Fatalf("a database with no identity rows must create its key: %v", err)
	}
	if fi, err := os.Stat(key); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key: %v %v", fi, err)
	}
	// Tables exist but are empty (a previous start): still fresh.
	_ = os.Remove(key)
	if err := tryStart(t, dir, key, ""); err != nil {
		t.Fatalf("empty identity tables are still a fresh instance: %v", err)
	}
}
