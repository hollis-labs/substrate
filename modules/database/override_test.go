package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/config"
)

// A bare integer in the SUBSTRATE_CONFIG override file reaches the module as a
// number it reads: postgres.max_conns takes effect in the resolved store config.
// (A boot to a live Postgres is not needed to see the setting; the config module
// runs for real through boot and configFrom reads what it left.)
func TestMaxConnsFromAnOverrideFileTakesEffect(t *testing.T) {
	p := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(p, []byte("database:\n  postgres:\n    max_conns: 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	t.Setenv("T_DSN", "postgres://u@localhost/db")
	run, err := boot.Start(context.Background(), "t", []string{"config"},
		`{"database":{"driver":"postgres","postgres":{"dsn_env":"T_DSN"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	sc, err := configFrom(run.App)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Postgres.MaxConns != 7 {
		t.Fatalf("max_conns = %d, want 7 (the override's bare integer was ignored)", sc.Postgres.MaxConns)
	}
}
