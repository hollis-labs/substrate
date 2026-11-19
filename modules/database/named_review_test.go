package database_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/store"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/health"
)

//go:embed testdata_b
var testdataB embed.FS

// pgDSN returns the live test server's DSN, or skips. It clears the tables these
// tests create before the test and again after it. The cleanup cannot go through
// the store under test: a deferred run.Stop closes that store before t.Cleanup runs.
func pgDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SUBSTRATE_TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("SUBSTRATE_REQUIRE_POSTGRES") == "1" {
			t.Fatal("SUBSTRATE_REQUIRE_POSTGRES=1 but SUBSTRATE_TEST_POSTGRES_DSN is not set")
		}
		t.Skip("SUBSTRATE_TEST_POSTGRES_DSN is not set; the Postgres path is covered in CI")
	}
	pgReset(t, dsn)
	t.Cleanup(func() { pgReset(t, dsn) })
	return dsn
}

func pgReset(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, store.Config{Driver: store.DriverPostgres, Postgres: store.PostgresConfig{DSN: dsn, AllowInsecure: true}})
	if err != nil {
		t.Fatalf("reset: open: %v", err)
	}
	defer func() { _ = st.Close() }()
	for _, tbl := range []string{"notes", "events_b", "goose_db_version", "goose_db_version_data", "goose_db_version_events"} {
		if _, err := st.ExecContext(ctx, `DROP TABLE IF EXISTS `+tbl); err != nil {
			t.Fatalf("reset: drop %s: %v", tbl, err)
		}
	}
}

// Two named stores in one database each keep their own goose version table, so
// both stores' migrations run (a shared table would skip the second store's 00001).
func TestTwoStoresInOneDatabaseBothMigrate(t *testing.T) {
	dsn := pgDSN(t)
	useNamedMigrations(t, "data")
	database.UseNamedMigrationFS("events", testdataB, "testdata_b")
	t.Cleanup(func() { database.UseNamedMigrations("events", emptySource()) })
	t.Setenv("T_PG_DSN", dsn)
	pg := func() map[string]any {
		return map[string]any{"driver": "postgres", "schema_mode": "auto", "required": false,
			"postgres": map[string]any{"dsn_env": "T_PG_DSN", "allow_insecure": true}}
	}
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{"data": pg(), "events": pg()}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	data, events := database.Named(run.App, "data"), database.Named(run.App, "events")
	for _, h := range []*database.Handle{data, events} {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := h.Wait(wctx); err != nil {
			t.Fatalf("%s not ready: %v (%+v)", h.Name(), err, h.Status())
		}
		cancel()
	}
	st, _ := data.Get()
	for _, tbl := range []string{"notes", "events_b", "goose_db_version_data", "goose_db_version_events"} {
		var n int
		if err := st.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name = ?`, tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s exists = %d (err %v): the second store's migrations were skipped", tbl, n, err)
		}
	}
}

func TestNamedStoreUsesItsOwnVersionTable(t *testing.T) {
	useNamedMigrations(t, "data")
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), nil)}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	st, err := database.Named(run.App, "data").Get()
	if err != nil {
		t.Fatal(err)
	}
	for tbl, want := range map[string]int{"goose_db_version_data": 1, "goose_db_version": 0, "notes": 1} {
		var n int
		if err := st.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name = ?`, tbl).Scan(&n); err != nil || n != want {
			t.Fatalf("table %s present = %d, want %d (err %v)", tbl, n, want, err)
		}
	}
}

func TestSQLiteStoresMustNotShareAFile(t *testing.T) {
	dir := t.TempDir()
	same := filepath.Join(dir, "a.db")
	respelled := filepath.Join(dir, "sub", "..", "a.db")
	for name, c := range map[string]struct {
		def    string
		stores map[string]any
		want   string
	}{
		"named equals default": {same, map[string]any{"data": sqliteStore(same, nil)}, `is the same file as store "default"`},
		"respelled path":       {same, map[string]any{"data": sqliteStore(respelled, nil)}, "is the same file"},
		"two named":            {filepath.Join(dir, "d.db"), map[string]any{"a": sqliteStore(same, nil), "b": sqliteStore(respelled, nil)}, `is the same file as store "a"`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"stores":%s}}`, c.def, mustJSON(t, c.stores))
			_, err := boot.Start(context.Background(), "t", []string{"health", "database"}, cfg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Start = %v, want error containing %q", err, c.want)
			}
		})
	}
}

// A schema that is behind (verify mode) is a failed store, not an outage; the
// deploy step MigrateNamed fixes it and the running app recovers without a restart.
func TestSchemaBehindIsFailedAndRecoversAfterMigrateNamed(t *testing.T) {
	useNamedMigrations(t, "data")
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), map[string]any{"required": false, "schema_mode": "verify"})}))
	if err != nil {
		t.Fatalf("an optional store with the schema behind must not fail Start: %v", err)
	}
	defer func() { _ = run.Stop(ctx) }()
	h := database.Named(run.App, "data")
	if s := h.Status(); s.State != database.StateFailed || !strings.Contains(s.LastError, "migrate:") {
		t.Fatalf("status = %+v, want failed with a migrate error", s)
	}
	rec := get(health.From(run.App).Handler(), "/healthz")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "[failed]") {
		t.Fatalf("health = %d %s", rec.Code, rec.Body)
	}
	if err := database.MigrateNamed(ctx, run.App, "data"); err != nil {
		t.Fatalf("MigrateNamed: %v", err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.Wait(wctx); err != nil {
		t.Fatalf("store did not recover after the migration: %v (%+v)", err, h.Status())
	}
	if err := database.MigrateNamed(ctx, run.App, "nope"); err == nil || !strings.Contains(err.Error(), `no store "nope"`) {
		t.Fatalf("MigrateNamed of an unknown store = %v", err)
	}
}

// Optional stores whose server accepts the connection and never answers must
// not hold up Start for connect_timeout each, and Get never waits on the network.
func TestHungServersDoNotBlockStartOrGet(t *testing.T) {
	var conns []net.Conn
	var mu sync.Mutex
	var addrs []string
	for i := 0; i < 2; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		addrs = append(addrs, ln.Addr().String())
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
			}
		}()
	}
	defer func() {
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	}()
	for i, a := range addrs {
		t.Setenv(fmt.Sprintf("T_HUNG_%d", i), "postgres://app:pw@"+a+"/db?sslmode=disable")
	}
	store := func(i int) map[string]any {
		return map[string]any{"driver": "postgres", "required": false, "connect_timeout": "5s",
			"postgres": map[string]any{"dsn_env": fmt.Sprintf("T_HUNG_%d", i), "allow_insecure": true}}
	}
	cfg := fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"start_wait":"300ms","stores":%s}}`,
		filepath.Join(t.TempDir(), "l.db"), mustJSON(t, map[string]any{"one": store(0), "two": store(1)}))
	ctx := context.Background()
	t0 := time.Now()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("Start took %v with two hung optional stores (connect_timeout 5s each); start_wait must bound it", d)
	}
	h := database.Named(run.App, "one")
	t1 := time.Now()
	if _, err := h.Get(); !errors.Is(err, database.ErrUnavailable) {
		t.Fatalf("Get = %v", err)
	}
	if d := time.Since(t1); d > 50*time.Millisecond {
		t.Fatalf("Get blocked for %v while the server is down", d)
	}
	t2 := time.Now()
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t2); d > 3*time.Second {
		t.Fatalf("Stop took %v: the in-flight connect attempt was not cancelled", d)
	}
}

func TestWaitReturnsAfterStopAndNilHandlesAreSafe(t *testing.T) {
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
		"down": map[string]any{"driver": "postgres", "required": false,
			"postgres": map[string]any{"dsn_file": filepath.Join(t.TempDir(), "missing"), "allow_insecure": true}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	h := database.Named(run.App, "down")
	res := make(chan error, 1)
	go func() { res <- h.Wait(ctx) }()
	time.Sleep(50 * time.Millisecond)
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-res:
		if !errors.Is(err, database.ErrUnavailable) {
			t.Fatalf("Wait after Stop = %v, want ErrUnavailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after Stop")
	}

	var nilH *database.Handle
	if nilH.Ready() || nilH.Name() != "" || nilH.Status().State != database.StateUnavailable {
		t.Fatal("nil handle must behave as never available")
	}
	if _, err := nilH.Get(); !errors.Is(err, database.ErrUnavailable) {
		t.Fatalf("nil Get = %v", err)
	}
	nilH.Report(driver.ErrBadConn)
	nilH.OnChange(func(database.Status) {})
	if err := nilH.Wait(ctx); !errors.Is(err, database.ErrUnavailable) {
		t.Fatalf("nil Wait = %v", err)
	}
	if database.Named(run.App, "misspelt") != nil {
		t.Fatal("unknown name must give nil")
	}
	// A nil handle (a misspelt store name) fails at construction, not as a route that is silently always 503.
	mustPanic := func(name string, f func()) {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "nil") {
				t.Fatalf("%s did not panic with a message about nil: %v", name, r)
			}
		}()
		f()
	}
	mustPanic("Gate(nil)", func() { database.Gate(nil) })
	mustPanic("GateWith(nil)", func() { database.GateWith(nil, func(http.ResponseWriter, *http.Request, database.Status) {}) })
	mustPanic("GateWith(h, nil)", func() {
		h2 := database.Named(run.App, "down")
		_ = h2
		var nonNil = database.Named(run.App, "down")
		if nonNil == nil {
			// run is stopped but the handle stays published; fall back to a handle that exists
			panic("nil handle for test setup")
		}
		database.GateWith(nonNil, nil)
	})
	rec := httptest.NewRecorder()
	database.WriteUnavailable(rec, nil)
	if rec.Code != 503 {
		t.Fatalf("WriteUnavailable(nil) = %d", rec.Code)
	}
}

func TestGateWithWritesTheCallersEnvelope(t *testing.T) {
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
		"down": map[string]any{"driver": "postgres", "required": false,
			"postgres": map[string]any{"dsn_file": filepath.Join(t.TempDir(), "missing"), "allow_insecure": true}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	h := database.Named(run.App, "down")
	deny := func(w http.ResponseWriter, r *http.Request, s database.Status) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"error":{"code":"unavailable","message":"store %s is %s"}}`, s.Name, s.State)
	}
	rec := get(database.GateWith(h, deny)(http.NotFoundHandler()), "/x")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `{"error":{"code":"unavailable","message":"store down is unavailable"}}`) {
		t.Fatalf("GateWith = %d %s", rec.Code, rec.Body)
	}
}

func TestReportFlipsStateAndOnChangeSeesIt(t *testing.T) {
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"},
		cfgJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), nil)}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	h := database.Named(run.App, "data")
	var mu sync.Mutex
	var seen []database.State
	h.OnChange(func(s database.Status) { mu.Lock(); seen = append(seen, s.State); mu.Unlock() })
	h.OnChange(func(database.Status) { panic("a bad hook must not kill the loop") })

	h.Report(errors.New("syntax error at or near SELECT")) // a query error, not a connection error
	if !h.Ready() {
		t.Fatal("a non-connection error must not flip the state")
	}
	h.Report(fmt.Errorf("query: %w", driver.ErrBadConn))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 || seen[0] != database.StateUnavailable || seen[1] != database.StateReady {
		t.Fatalf("OnChange saw %v, want [unavailable ready]: Report flips at once and the poked ping restores it", seen)
	}
}

func TestIsUnavailableClassifiesConnectionErrors(t *testing.T) {
	for _, err := range []error{
		database.ErrUnavailable, fmt.Errorf("x: %w", database.ErrUnavailable), driver.ErrBadConn,
		fmt.Errorf("write: %w", net.ErrClosed), &net.OpError{Op: "dial", Err: errors.New("connection refused")},
		errors.New("FATAL: terminating connection due to administrator command (SQLSTATE 57P01)"),
		errors.New("connection reset by peer"), errors.New("failed to connect to `host=db`: dial error"),
		errors.New("FATAL: sorry, too many clients already (SQLSTATE 53300)"),
	} {
		if !database.IsUnavailable(err) {
			t.Errorf("IsUnavailable(%v) = false", err)
		}
	}
	for _, err := range []error{nil, errors.New("syntax error"), errors.New(`duplicate key value violates unique constraint (SQLSTATE 23505)`), context.Canceled} {
		if database.IsUnavailable(err) {
			t.Errorf("IsUnavailable(%v) = true", err)
		}
	}
}

// Logs say what is wrong and never carry the password.
func TestLogsNameTheStoreAndNeverCarryCredentials(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(syncWriter{&buf, &mu}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	pw := "tr0ub4dor" + "3secret"
	t.Setenv("T_LOG_DSN", "postgres://app:"+pw+"@127.0.0.1:1/appdb?sslmode=disable&sslpassword="+pw)
	ctx := context.Background()
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfgJSON(t, map[string]any{
		"data": map[string]any{"driver": "postgres", "required": false, "connect_timeout": "500ms",
			"postgres": map[string]any{"dsn_env": "T_LOG_DSN", "allow_insecure": true}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	_ = run.Stop(ctx)
	mu.Lock()
	defer mu.Unlock()
	out := buf.String()
	if !strings.Contains(out, "optional store is unavailable at start; the app is starting degraded") {
		t.Fatalf("an optional store that is down at start must be logged loudly, got:\n%s", out)
	}
	if !strings.Contains(out, "store unavailable") || !strings.Contains(out, "store=data") || !strings.Contains(out, "127.0.0.1:1/appdb") {
		t.Fatalf("expected a warning naming the store and target, got:\n%s", out)
	}
	if strings.Contains(out, pw) {
		t.Fatalf("a credential reached the log:\n%s", out)
	}
}

type syncWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func TestStartWaitMustBeADuration(t *testing.T) {
	_, err := boot.Start(context.Background(), "t", []string{"health", "database"},
		fmt.Sprintf(`{"database":{"sqlite":{"path":%q},"start_wait":"soon","stores":%s}}`, filepath.Join(t.TempDir(), "l.db"),
			mustJSON(t, map[string]any{"data": sqliteStore(filepath.Join(t.TempDir(), "d.db"), nil)})))
	if err == nil || !strings.Contains(err.Error(), "start_wait") {
		t.Fatalf("Start = %v", err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
