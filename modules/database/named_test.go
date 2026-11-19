package database_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/health"
)

func useNamedMigrations(t *testing.T, name string) {
	t.Helper()
	database.UseNamedMigrationFS(name, testdata, "testdata")
	t.Cleanup(func() { database.UseNamedMigrations(name, emptySource()) })
}

// cfgJSON builds the boot config: a local default store plus the given named stores.
func cfgJSON(t *testing.T, stores map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"database": map[string]any{
		"sqlite": map[string]any{"path": filepath.Join(t.TempDir(), "local.db")},
		"stores": stores,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sqliteStore(path string, extra map[string]any) map[string]any {
	m := map[string]any{"driver": "sqlite", "sqlite": map[string]any{"path": path},
		"retry": map[string]any{"initial": "10ms", "max": "40ms"}, "ping_interval": "20ms"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestNamedStoreIsReadyAndSeparateFromTheDefault(t *testing.T) {
	useMigrations(t)
	useNamedMigrations(t, "data")
	ctx := context.Background()
	dataPath := filepath.Join(t.TempDir(), "data.db")
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(dataPath, nil)}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()

	h := database.Named(run.App, "data")
	if h == nil {
		t.Fatal("named store not published")
	}
	st, err := h.Get()
	if err != nil {
		t.Fatalf("Get on a healthy store: %v", err)
	}
	if _, err := st.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('in data')`); err != nil {
		t.Fatalf("named store was not migrated: %v", err)
	}
	if database.From(run.App) == st {
		t.Fatal("named store is the default store")
	}
	if s := h.Status(); s.State != database.StateReady || !s.Required || s.Target != "data.db" || s.Attempts != 1 {
		t.Fatalf("status = %+v", s)
	}
	if got := database.Statuses(run.App); len(got) != 1 || got[0].Name != "data" {
		t.Fatalf("Statuses = %+v", got)
	}
	if rec := get(health.From(run.App).Handler(), "/healthz"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"database.data"`) {
		t.Fatalf("health = %d %s", rec.Code, rec.Body)
	}
}

// The app boots with the data store unreachable, serves degraded health and 503s,
// and recovers by itself when the store comes back.
func TestOptionalNamedStoreStartsDegradedAndRecovers(t *testing.T) {
	useNamedMigrations(t, "data")
	ctx := context.Background()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The parent of the database file is a regular file, so every open fails until it is replaced.
	dataPath := filepath.Join(blocker, "data.db")
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(dataPath, map[string]any{"required": false})}))
	if err != nil {
		t.Fatalf("an optional store that is down must not fail Start: %v", err)
	}
	defer func() { _ = run.Stop(ctx) }()

	h := database.Named(run.App, "data")
	if _, err := h.Get(); !errors.Is(err, database.ErrUnavailable) {
		t.Fatalf("Get while down = %v, want ErrUnavailable", err)
	}
	if s := h.Status(); s.State != database.StateUnavailable || s.LastError == "" || s.Attempts < 1 {
		t.Fatalf("status while down = %+v", s)
	}
	rec := get(health.From(run.App).Handler(), "/healthz")
	// An optional store being down is degraded but the process is serving: 200, so a deploy gate or supervisor
	// does not roll back or restart the app.
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"degraded"`) ||
		!strings.Contains(rec.Body.String(), "database unavailable: store data (data.db)") {
		t.Fatalf("degraded health = %d %s", rec.Code, rec.Body)
	}
	reached := false
	gate := database.Gate(h)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true }))
	rec = get(gate, "/data")
	if rec.Code != 503 || reached || rec.Header().Get("Retry-After") == "" ||
		!strings.Contains(rec.Body.String(), `"code":"database_unavailable"`) {
		t.Fatalf("gate while down = %d reached=%v %s", rec.Code, reached, rec.Body)
	}
	// The local default store is unaffected.
	if database.From(run.App) == nil {
		t.Fatal("default store missing")
	}

	// The store comes back: no restart, no manual step.
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.Wait(wctx); err != nil {
		t.Fatalf("store did not recover: %v (status %+v)", err, h.Status())
	}
	st, err := h.Get()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('after recovery')`); err != nil {
		t.Fatalf("recovered store was not migrated: %v", err)
	}
	if rec := get(health.From(run.App).Handler(), "/healthz"); rec.Code != 200 {
		t.Fatalf("health after recovery = %d %s", rec.Code, rec.Body)
	}
	reached = false
	if rec := get(gate, "/data"); rec.Code != 200 && !reached {
		t.Fatalf("gate after recovery = %d reached=%v", rec.Code, reached)
	}
	if s := h.Status(); s.State != database.StateReady || s.LastError != "" {
		t.Fatalf("status after recovery = %+v", s)
	}
}

func TestRequiredNamedStoreFailsStart(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	// required defaults to true
	_, err := boot.Start(context.Background(), "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(blocker, "data.db"), nil)}))
	if err == nil || !strings.Contains(err.Error(), "stores.data") {
		t.Fatalf("Start = %v, want a failure naming stores.data", err)
	}
}

func TestStopMakesTheStoreUnavailable(t *testing.T) {
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), nil)}))
	if err != nil {
		t.Fatal(err)
	}
	h := database.Named(run.App, "data")
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Get(); !errors.Is(err, database.ErrUnavailable) || h.Status().State != database.StateStopped {
		t.Fatalf("after Stop: Get err=%v status=%+v", err, h.Status())
	}
}

func TestNamedConfigErrorsFailStart(t *testing.T) {
	ok := filepath.Join(t.TempDir(), "d.db")
	for name, c := range map[string]struct {
		store map[string]any
		key   string
		want  string
	}{
		"bad name":          {sqliteStore(ok, nil), "Data-1", "name must match"},
		"no driver":         {map[string]any{"sqlite": map[string]any{"path": ok}}, "data", "driver is required"},
		"no sqlite path":    {map[string]any{"driver": "sqlite"}, "data", "sqlite.path is required"},
		"typo in store":     {sqliteStore(ok, map[string]any{"requird": false}), "data", `stores.data: unknown key "requird"`},
		"typo in retry":     {sqliteStore(ok, map[string]any{"retry": map[string]any{"inital": "1s"}}), "data", `stores.data.retry: unknown key "inital"`},
		"typo in sqlite":    {map[string]any{"driver": "sqlite", "sqlite": map[string]any{"path": ok, "jurnal": "wal"}}, "data", `stores.data.sqlite: unknown key "jurnal"`},
		"typo in postgres":  {map[string]any{"driver": "postgres", "postgres": map[string]any{"dsn_fle": "x"}}, "data", `stores.data.postgres: unknown key "dsn_fle"`},
		"required not bool": {sqliteStore(ok, map[string]any{"required": "no"}), "data", "required must be true or false"},
		"unknown driver":    {map[string]any{"driver": "mysql"}, "data", "stores.data: driver"},
		"literal dsn":       {map[string]any{"driver": "postgres", "postgres": map[string]any{"dsn": "postgres://x"}}, "data", "cannot be written in config"},
		"bad duration":      {sqliteStore(ok, map[string]any{"connect_timeout": "soon"}), "data", "positive duration"},
		"retry order":       {sqliteStore(ok, map[string]any{"retry": map[string]any{"initial": "5s", "max": "1s"}}), "data", "retry.max must be at least"},
		"optional too":      {sqliteStore(ok, map[string]any{"required": false, "driver": "mysql"}), "data", "must be sqlite or postgres"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := boot.Start(context.Background(), "t", []string{"health", "database"},
				cfgJSON(t, map[string]any{c.key: c.store}))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Start = %v, want error containing %q", err, c.want)
			}
		})
	}
}

// A secret that is not readable yet is an outage, not a failed start, and no
// credential reaches health, status or errors.
func TestPostgresOptionalStoreDownKeepsCredentialsOut(t *testing.T) {
	ctx := context.Background()
	pw := "hunter" + "2secret"
	dsn := "postgres:" + "//app:" + pw + "@127.0.0.1:1/appdb?sslmode=disable" // port 1: nothing listens
	dsnFile := filepath.Join(t.TempDir(), "dsn")
	missing := filepath.Join(t.TempDir(), "not-there")

	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
		"unreadable": map[string]any{"driver": "postgres", "required": false, "connect_timeout": "500ms",
			"retry":    map[string]any{"initial": "20ms", "max": "40ms"},
			"postgres": map[string]any{"dsn_file": missing, "allow_insecure": true}},
		"down": map[string]any{"driver": "postgres", "required": false, "connect_timeout": "500ms",
			"retry":    map[string]any{"initial": "20ms", "max": "40ms"},
			"postgres": map[string]any{"dsn_file": dsnFile, "allow_insecure": true}},
	}))
	if err != nil {
		t.Fatalf("Start with unreadable secrets must succeed: %v", err)
	}
	defer func() { _ = run.Stop(ctx) }()
	if s := database.Named(run.App, "unreadable").Status(); s.State != database.StateUnavailable ||
		!strings.Contains(s.LastError, "cannot read postgres.dsn_file") {
		t.Fatalf("unreadable status = %+v", s)
	}
	// Provision the secret after Start: the loop picks it up on the next attempt and reports the real target.
	if err := os.WriteFile(dsnFile, []byte(dsn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := database.Named(run.App, "down")
	deadline := time.Now().Add(5 * time.Second)
	for h.Status().Target != "127.0.0.1:1/appdb" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	s := h.Status()
	if s.Target != "127.0.0.1:1/appdb" || s.State != database.StateUnavailable {
		t.Fatalf("status after secret appeared = %+v", s)
	}
	rec := get(health.From(run.App).Handler(), "/healthz")
	for _, leak := range []string{pw, "app:" + pw, "password"} {
		if strings.Contains(rec.Body.String(), leak) || strings.Contains(s.LastError, leak) {
			t.Fatalf("credential leaked (%q): health=%s status=%+v", leak, rec.Body, s)
		}
	}
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"degraded"`) ||
		!strings.Contains(rec.Body.String(), "database unavailable: store down (127.0.0.1:1/appdb)") {
		t.Fatalf("health = %d %s", rec.Code, rec.Body)
	}
}

// A live server. CI provides one; set SUBSTRATE_TEST_POSTGRES_DSN to run it locally.
func TestPostgresNamedStoreReady(t *testing.T) {
	dsn := pgDSN(t)
	useNamedMigrations(t, "data")
	t.Setenv("T_PG_DSN", dsn)
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
		"data": map[string]any{"driver": "postgres", "schema_mode": "auto", "required": false,
			"postgres": map[string]any{"dsn_env": "T_PG_DSN", "allow_insecure": true}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	h := database.Named(run.App, "data")
	st, err := h.Get()
	if err != nil {
		t.Fatalf("Get: %v (status %+v)", err, h.Status())
	}
	if _, err := st.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('hi')`); err != nil {
		t.Fatalf("migration did not create notes on postgres: %v", err)
	}
	if !strings.Contains(h.Status().Target, "/") {
		t.Fatalf("target = %q", h.Status().Target)
	}
}
