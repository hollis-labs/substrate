package database_test

import (
	"context"
	"embed"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/health"
)

//go:embed testdata
var testdata embed.FS

func useMigrations(t *testing.T) {
	t.Helper()
	database.UseMigrations(migrate.Source{FS: testdata, Root: "testdata"})
	t.Cleanup(func() { database.UseMigrations(migrate.Source{}) })
}

func TestSQLiteStartMigratesAndReportsHealth(t *testing.T) {
	useMigrations(t)
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, `{"database":{"sqlite":{"path":":memory:"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()

	st := database.From(run.App)
	if st == nil {
		t.Fatal("store not published")
	}
	if _, err := st.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('hi')`); err != nil {
		t.Fatalf("migration did not create notes: %v", err)
	}
	rec := httptest.NewRecorder()
	health.From(run.App).Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"database"`) {
		t.Fatalf("health = %d %s", rec.Code, rec.Body)
	}
}

func TestConfigErrorsFailStartWithoutLeakingSecrets(t *testing.T) {
	pw := "super" + "secret"
	withCreds := "postgres:" + "//u:" + pw + "@db.invalid/app"
	dsnFile := filepath.Join(t.TempDir(), "dsn")
	_ = os.WriteFile(dsnFile, []byte(withCreds+"\n"), 0o600)
	t.Setenv("EMPTY_DSN_VAR", "")
	for name, c := range map[string]struct{ cfg, want string }{
		"bad schema mode":    {`{"database":{"sqlite":{"path":":memory:"},"schema_mode":"bogus"}}`, ""},
		"unknown driver":     {`{"database":{"driver":"mysql"}}`, "must be sqlite or postgres"},
		"postgres no dsn":    {`{"database":{"driver":"postgres"}}`, "needs dsn_file or dsn_env"},
		"literal dsn":        {`{"database":{"driver":"postgres","postgres":{"dsn":"` + withCreds + `"}}}`, "cannot be written in config"},
		"file and env":       {`{"database":{"driver":"postgres","postgres":{"dsn_file":"` + dsnFile + `","dsn_env":"X"}}}`, "choose one"},
		"empty env":          {`{"database":{"driver":"postgres","postgres":{"dsn_env":"EMPTY_DSN_VAR"}}}`, "is empty"},
		"unreadable file":    {`{"database":{"driver":"postgres","postgres":{"dsn_file":"/nonexistent/secret-dir/dsn"}}}`, "cannot read"},
		"unreachable server": {`{"database":{"driver":"postgres","postgres":{"dsn_file":"` + dsnFile + `","allow_insecure":true}}}`, ""},
	} {
		_, err := boot.Start(context.Background(), "t", []string{"health", "database"}, c.cfg)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
		for _, leak := range []string{pw, "secret-dir"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error leaks %q: %v", name, leak, err)
			}
		}
	}
}

// The Postgres path against a live server. CI provides one; set
// SUBSTRATE_TEST_POSTGRES_DSN to run it locally, or SUBSTRATE_E2E_OFFLINE=1 to
// skip it on purpose.
func TestPostgresMigratesAndReportsHealth(t *testing.T) {
	dsn := os.Getenv("SUBSTRATE_TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("SUBSTRATE_REQUIRE_POSTGRES") == "1" {
			t.Fatal("SUBSTRATE_REQUIRE_POSTGRES=1 but SUBSTRATE_TEST_POSTGRES_DSN is not set")
		}
		if os.Getenv("SUBSTRATE_E2E_OFFLINE") == "1" {
			t.Skip("SUBSTRATE_E2E_OFFLINE=1")
		}
		t.Skip("SUBSTRATE_TEST_POSTGRES_DSN is not set; the Postgres path is covered in CI")
	}
	useMigrations(t)
	t.Setenv("T_PG_DSN", dsn)
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		`{"database":{"driver":"postgres","schema_mode":"auto","postgres":{"dsn_env":"T_PG_DSN","allow_insecure":true}}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	st := database.From(run.App)
	t.Cleanup(func() {
		_, _ = st.ExecContext(ctx, `DROP TABLE IF EXISTS notes`)
		_, _ = st.ExecContext(ctx, `DROP TABLE IF EXISTS goose_db_version`)
	})
	if _, err := st.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('hi')`); err != nil {
		t.Fatalf("migration did not create notes on postgres: %v", err)
	}
	if err := st.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUseMigrationFSMatchesUseMigrations(t *testing.T) {
	database.UseMigrationFS(testdata, "testdata")
	t.Cleanup(func() { database.UseMigrations(migrate.Source{}) })
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, `{"database":{"sqlite":{"path":":memory:"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	if _, err := database.From(run.App).ExecContext(ctx, `INSERT INTO notes (body) VALUES ('hi')`); err != nil {
		t.Fatalf("migrations from the file system did not run: %v", err)
	}
}
