package sqlite_test

import (
	"context"
	"embed"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/sqlite"
)

//go:embed testdata
var testdata embed.FS

func TestStartMigratesAndReportsHealth(t *testing.T) {
	sqlite.UseMigrations(migrate.Source{FS: testdata, Root: "testdata"})
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "sqlite"}, `{"sqlite":{"path":":memory:"}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()

	st := sqlite.From(run.App)
	if st == nil {
		t.Fatal("store not published")
	}
	if _, err := st.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('hi')`); err != nil {
		t.Fatalf("migration did not create notes: %v", err)
	}
	rec := httptest.NewRecorder()
	health.From(run.App).Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("health = %d %s", rec.Code, rec.Body)
	}
}

func TestBadSchemaModeFailsStart(t *testing.T) {
	_, err := boot.Start(context.Background(), "t", []string{"health", "sqlite"},
		`{"sqlite":{"path":":memory:","schema_mode":"bogus"}}`)
	if err == nil {
		t.Fatal("expected error")
	}
}
