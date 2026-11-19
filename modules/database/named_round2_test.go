package database_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/health"
)

func pgStore(extra map[string]any) map[string]any {
	m := map[string]any{"driver": "postgres", "required": false, "connect_timeout": "500ms",
		"retry": map[string]any{"initial": "20ms", "max": "40ms"}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// Only a missing file or an empty variable is an outage. A secret file that is
// unreadable for any other reason fails Start and names the path.
func TestSecretFileProblemsFailStartAndNameThePath(t *testing.T) {
	dir := t.TempDir()
	closedPort := "postgres://app:pw@127.0.0.1:1/db?sslmode=disable"

	isDir := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(isDir, 0o700); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ file, want string }{
		"is a directory": {isDir, "is a directory"},
		"empty file":     {empty, "is empty"},
	}
	if os.Geteuid() != 0 { // root reads anything
		locked := filepath.Join(dir, "locked")
		if err := os.WriteFile(locked, []byte(closedPort), 0o000); err != nil {
			t.Fatal(err)
		}
		cases["permission denied"] = struct{ file, want string }{locked, "permission denied"}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := boot.Start(context.Background(), "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
				"data": pgStore(map[string]any{"postgres": map[string]any{"dsn_file": c.file, "allow_insecure": true}}),
			}))
			if err == nil || !strings.Contains(err.Error(), c.file) || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "stores.data") {
				t.Fatalf("Start = %v, want an error naming stores.data, the path %q and %q", err, c.file, c.want)
			}
		})
	}

	// A missing file and an empty variable are outages: Start succeeds.
	t.Setenv("T_EMPTY_DSN", "")
	run, err := boot.Start(context.Background(), "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
		"gone": pgStore(map[string]any{"postgres": map[string]any{"dsn_file": filepath.Join(dir, "not-yet"), "allow_insecure": true}}),
		"env":  pgStore(map[string]any{"postgres": map[string]any{"dsn_env": "T_EMPTY_DSN", "allow_insecure": true}}),
	}))
	if err != nil {
		t.Fatalf("a missing secret file and an empty variable must be outages, not start failures: %v", err)
	}
	_ = run.Stop(context.Background())
}

// A process that only verifies the schema never reads the migration credential.
func TestVerifyModeDoesNotReadTheMigrationSecret(t *testing.T) {
	dir := t.TempDir()
	cannotBeRead := filepath.Join(dir, "migrate-secret-dir") // a directory: reading it as a file would fail Start
	if err := os.Mkdir(cannotBeRead, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("T_DSN", "postgres://app:pw@127.0.0.1:1/db?sslmode=disable")
	pg := func(mode string) map[string]any {
		return pgStore(map[string]any{"schema_mode": mode, "postgres": map[string]any{
			"dsn_env": "T_DSN", "migrate_dsn_file": cannotBeRead, "allow_insecure": true}})
	}
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{"data": pg("verify")}))
	if err != nil {
		t.Fatalf("verify mode must not read migrate_dsn_file: %v", err)
	}
	_ = run.Stop(ctx)
	// Default for Postgres is verify too.
	m := pg("")
	delete(m, "schema_mode")
	run, err = boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{"data": m}))
	if err != nil {
		t.Fatalf("the Postgres default (verify) must not read migrate_dsn_file: %v", err)
	}
	_ = run.Stop(ctx)
	// auto does read it, and says where it failed.
	_, err = boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{"data": pg("auto")}))
	if err == nil || !strings.Contains(err.Error(), cannotBeRead) {
		t.Fatalf("auto mode reads migrate_dsn_file; Start = %v, want an error naming %s", err, cannotBeRead)
	}
}

func TestWrongTypesAndValuesAreRefused(t *testing.T) {
	ok := filepath.Join(t.TempDir(), "d.db")
	sq := func(extra map[string]any) map[string]any { return sqliteStore(ok, extra) }
	for name, c := range map[string]struct {
		cfg  string
		want string
	}{
		"connect_timeout number": {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":%s}}}`, filepath.Join(t.TempDir(), "l.db"), mustJSON(t, sq(map[string]any{"connect_timeout": 5}))), "stores.data.connect_timeout must be a string"},
		"ping_interval bool":     {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":%s}}}`, filepath.Join(t.TempDir(), "l.db"), mustJSON(t, sq(map[string]any{"ping_interval": true}))), "stores.data.ping_interval must be a string"},
		"retry is a string":      {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":%s}}}`, filepath.Join(t.TempDir(), "l.db"), mustJSON(t, sq(map[string]any{"retry": "fast"}))), "stores.data.retry must be a mapping"},
		"sqlite is a string":     {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":{"driver":"sqlite","sqlite":"x"}}}}`, filepath.Join(t.TempDir(), "l.db")), "stores.data.sqlite must be a mapping"},
		"allow_insecure string":  {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":{"driver":"postgres","postgres":{"allow_insecure":"yes"}}}}}`, filepath.Join(t.TempDir(), "l.db")), "stores.data.postgres.allow_insecure must be true or false"},
		"max_conns string":       {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":{"driver":"postgres","postgres":{"max_conns":"ten"}}}}}`, filepath.Join(t.TempDir(), "l.db")), "stores.data.postgres.max_conns must be a number"},
		"bad schema_mode":        {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":%s}}}`, filepath.Join(t.TempDir(), "l.db"), mustJSON(t, sq(map[string]any{"schema_mode": "yolo"}))), `stores.data.schema_mode "yolo" must be auto or verify`},
		"bad journal":            {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":{"driver":"sqlite","sqlite":{"path":%q,"journal":"memory"}}}}}`, filepath.Join(t.TempDir(), "l.db"), ok), `sqlite.journal "memory" must be wal or delete`},
		"bad synchronous":        {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":{"data":{"driver":"sqlite","sqlite":{"path":%q,"synchronous":"fast"}}}}}`, filepath.Join(t.TempDir(), "l.db"), ok), `sqlite.synchronous "fast" must be OFF`},
		"start_wait number":      {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"start_wait":3,"stores":{"data":%s}}}`, filepath.Join(t.TempDir(), "l.db"), mustJSON(t, sq(nil))), "start_wait must be a string"},
		"stores not a mapping":   {fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":["data"]}}`, filepath.Join(t.TempDir(), "l.db")), "stores must be a mapping"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := boot.Start(context.Background(), "t", []string{"health", "database"}, c.cfg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Start = %v, want error containing %q", err, c.want)
			}
		})
	}
}

// The deploy step: `<app> db migrate --store <name>` through the cli module.
func TestDBMigrateVerbRunsAStoresMigrations(t *testing.T) {
	useNamedMigrations(t, "data")
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database", "cli"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), map[string]any{"required": false, "schema_mode": "verify"})}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	h := database.Named(run.App, "data")
	if h.Ready() || h.Status().State != database.StateFailed {
		t.Fatalf("with the schema behind (verify) the store should be failed, got %+v", h.Status())
	}
	root := cli.Root(run.App)
	if root == nil {
		t.Fatal("cli root missing")
	}
	exec := func(args ...string) (string, error) {
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.ExecuteContext(ctx)
		return out.String(), err
	}
	if _, err := exec("db", "migrate"); err == nil || !strings.Contains(err.Error(), "--store is required") {
		t.Fatalf("migrate without --store = %v", err)
	}
	if _, err := exec("db", "migrate", "--store", "nope"); err == nil || !strings.Contains(err.Error(), `no store "nope"`) {
		t.Fatalf("migrate of an unknown store = %v", err)
	}
	out, err := exec("db", "migrate", "--store", "data")
	if err != nil || !strings.Contains(out, "store data: migrations applied") {
		t.Fatalf("db migrate = %v %q", err, out)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.Wait(wctx); err != nil {
		t.Fatalf("the running store did not pick up the migration: %v (%+v)", err, h.Status())
	}
}

func TestNoDBVerbWithoutNamedStores(t *testing.T) {
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database", "cli"}, `{"database":{"sqlite":{"path":":memory:"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	for _, c := range cli.Root(run.App).Commands() {
		if c.Name() == "db" {
			t.Fatal("an app with no named stores must not get a db verb")
		}
	}
}

var reDSNPassword = regexp.MustCompile(`(?i)(password=)(\S+)|(://[^:/@\s]+:)([^@\s]+)(@)`)

// A rejected login is a failed store (it needs a human), not an outage, and the
// password stays out of everything. Needs a server that checks passwords; one
// that trusts every connection skips the test.
func TestPostgresWrongPasswordIsFailed(t *testing.T) {
	dsn := pgDSN(t)
	if !reDSNPassword.MatchString(dsn) {
		t.Skip("the test DSN has no password to get wrong")
	}
	wrong := "wr0ng" + "pass" + "word"
	bad := reDSNPassword.ReplaceAllStringFunc(dsn, func(m string) string {
		sub := reDSNPassword.FindStringSubmatch(m)
		if sub[1] != "" {
			return sub[1] + wrong
		}
		return sub[3] + wrong + sub[5]
	})
	t.Setenv("T_BAD_PW_DSN", bad)
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
		"data": pgStore(map[string]any{"connect_timeout": "3s", "postgres": map[string]any{"dsn_env": "T_BAD_PW_DSN", "allow_insecure": true}}),
	}))
	if err != nil {
		t.Fatalf("a rejected login at start must not fail Start of an optional store: %v", err)
	}
	defer func() { _ = run.Stop(ctx) }()
	h := database.Named(run.App, "data")
	deadline := time.Now().Add(5 * time.Second)
	for h.Status().State == database.StateConnecting && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.Ready() {
		t.Skip("the server accepted a wrong password (trust authentication); this test needs one that checks")
	}
	s := h.Status()
	if s.State != database.StateFailed {
		t.Fatalf("status = %+v, want failed (a rejected login is not an outage)", s)
	}
	rec := get(health.From(run.App).Handler(), "/healthz")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "[failed]") {
		t.Fatalf("health = %d %s", rec.Code, rec.Body)
	}
	real := reDSNPassword.FindStringSubmatch(dsn)
	realPW := real[2]
	if real[4] != "" {
		realPW = real[4]
	}
	for _, leak := range []string{wrong, realPW} {
		if leak != "" && (strings.Contains(rec.Body.String(), leak) || strings.Contains(s.LastError, leak) || strings.Contains(s.Target, leak)) {
			t.Fatalf("a password leaked: health=%s status=%+v", rec.Body, s)
		}
	}
}
