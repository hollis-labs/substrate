package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/config"
)

// Bare integers in the SUBSTRATE_CONFIG override file reach identity as numbers
// it reads: min_password and bcrypt_cost take effect in the resolved config, and
// the password rule and the hash cost follow them.
func TestNumericKeysFromAnOverrideFileTakeEffect(t *testing.T) {
	p := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(p, []byte("identity:\n  min_password: 20\n  bcrypt_cost: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	run, err := boot.Start(context.Background(), "t", []string{"config"}, `{"identity":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	c, err := parseConfig(run.App)
	if err != nil {
		t.Fatal(err)
	}
	if c.minPassword != 20 {
		t.Errorf("min_password = %d, want 20 (the override's bare integer was ignored)", c.minPassword)
	}
	if c.cost != 5 {
		t.Errorf("bcrypt_cost = %d, want 5 (the override's bare integer was ignored)", c.cost)
	}
	s := &Service{cfg: c}
	h, err := s.hashPassword("01234567890123456789")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := bcrypt.Cost([]byte(h)); got != 5 {
		t.Errorf("hash cost = %d, want 5", got)
	}
	if err := s.checkPassword("0123456789012345678"); err == nil { // 19 characters
		t.Error("a 19-character password passed min_password 20")
	}
	if err := s.checkPassword("01234567890123456789"); err != nil { // 20 characters
		t.Errorf("a 20-character password refused: %v", err)
	}
}
