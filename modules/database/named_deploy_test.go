package database_test

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/health"
)

// A REQUIRED named store with the schema behind (every store's first deploy) fails Start, because it is required.
// The deploy step `<app> db migrate --store <name>` must still run, so Start does not open the named stores for it.
func TestDBMigrateRunsForARequiredStoreWithTheSchemaBehind(t *testing.T) {
	useNamedMigrations(t, "data")
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "d.db")
	cfg := cfgJSON(t, map[string]any{"data": sqliteStore(path, map[string]any{"required": true, "schema_mode": "verify"})})
	mods := []string{"health", "database", "cli"}

	// A normal start refuses: the store is required and its schema is behind.
	if _, err := boot.Start(ctx, "t", mods, cfg); err == nil || !strings.Contains(err.Error(), "stores.data") {
		t.Fatalf("a normal Start with a required store behind = %v, want a failure naming stores.data", err)
	}

	// The deploy step boots without opening it and migrates it.
	restore := database.SetDeployStep(true)
	defer restore()
	run, err := boot.Start(ctx, "t", mods, cfg)
	if err != nil {
		t.Fatalf("Start for the deploy step must not need the store: %v", err)
	}
	root := cli.Root(run.App)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"db", "migrate", "--store", "data"})
	if err := root.ExecuteContext(ctx); err != nil || !strings.Contains(out.String(), "store data: migrations applied") {
		t.Fatalf("db migrate = %v %q", err, out.String())
	}
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// After the deploy step the same required store starts normally.
	database.SetDeployStep(false) // restore() at the end puts the original back
	run2, err := boot.Start(ctx, "t", mods, cfg)
	if err != nil {
		t.Fatalf("Start after the migration: %v", err)
	}
	defer func() { _ = run2.Stop(ctx) }()
	if !database.Named(run2.App, "data").Ready() {
		t.Fatalf("store not ready after the migration: %+v", database.Named(run2.App, "data").Status())
	}
}

// A required store that goes down after start fails /healthz with 503 (an optional one stays 200). Registering
// required stores as soft checks would pass every other test.
func TestRequiredStoreDownIsHealth503(t *testing.T) {
	useNamedMigrations(t, "data")
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), map[string]any{"required": true, "ping_interval": "1h"})}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	h := database.Named(run.App, "data")
	if rec := get(health.From(run.App).Handler(), "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthy = %d %s", rec.Code, rec.Body)
	}
	h.Report(syscall.ECONNREFUSED)
	if h.Ready() {
		t.Fatal("Report did not flip the store to unavailable")
	}
	rec := get(health.From(run.App).Handler(), "/healthz")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "database.data") {
		t.Fatalf("a required store down: health = %d %s, want 503 naming database.data", rec.Code, rec.Body)
	}
}

// A schema that is behind reads "migrate: schema version mismatch ..." once, not "migrate: migrate: ...".
func TestMigrateErrorHasOnePrefix(t *testing.T) {
	useNamedMigrations(t, "data")
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), map[string]any{"required": false, "schema_mode": "verify"})}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	msg := database.Named(run.App, "data").Status().LastError
	if n := strings.Count(msg, "migrate:"); n != 1 {
		t.Fatalf("prefix appears %d times in %q, want once", n, msg)
	}
}
