//go:build unix

package database_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
)

const (
	envChildDB      = "SUBSTRATE_DBTEST_CHILD_DB"
	envChildBarrier = "SUBSTRATE_DBTEST_CHILD_BARRIER"
)

// TestMain lets the test binary act as the "second process" of the concurrent
// start tests: with envChildDB set it waits for the barrier file, starts the
// database module on that path, and exits 0 or 1.
func TestMain(m *testing.M) {
	if dbPath := os.Getenv(envChildDB); dbPath != "" {
		os.Exit(runChild(dbPath, os.Getenv(envChildBarrier)))
	}
	os.Exit(m.Run())
}

func runChild(dbPath, barrier string) int {
	database.UseMigrationFS(testdata, "testdata")
	for {
		if _, err := os.Stat(barrier); err == nil {
			break
		}
		time.Sleep(200 * time.Microsecond)
	}
	cfg, _ := json.Marshal(map[string]any{"database": map[string]any{"sqlite": map[string]any{"path": dbPath}}})
	run, err := boot.Start(context.Background(), "t", []string{"health", "database"}, string(cfg))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = run.Stop(context.Background())
	return 0
}

// Two or more real processes starting at the same moment on one new SQLite file
// must all succeed and migrate once. Without the open lock a share of them fails
// with "database is locked" or "table notes already exists".
func TestConcurrentProcessesOpenOneNewFile(t *testing.T) {
	rounds, procs := 8, 6
	if testing.Short() {
		rounds = 2
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for r := 0; r < rounds; r++ {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "sub", "app.db") // parent dir does not exist yet either
		barrier := filepath.Join(dir, "go")
		cmds := make([]*exec.Cmd, procs)
		outs := make([]*strings.Builder, procs)
		for i := range cmds {
			c := exec.Command(exe, "-test.run=^$")
			c.Env = append(os.Environ(), envChildDB+"="+dbPath, envChildBarrier+"="+barrier)
			outs[i] = &strings.Builder{}
			c.Stderr = outs[i]
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			cmds[i] = c
		}
		time.Sleep(300 * time.Millisecond) // let every child reach the barrier loop
		if err := os.WriteFile(barrier, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		for i, c := range cmds {
			if err := c.Wait(); err != nil {
				t.Errorf("round %d process %d: %v: %s", r, i, err, outs[i])
			}
		}
		assertMigratedOnce(t, dbPath)
	}
}

func assertMigratedOnce(t *testing.T, dbPath string) {
	t.Helper()
	database.UseMigrationFS(testdata, "testdata")
	t.Cleanup(func() { database.UseMigrations(migrate.Source{}) })
	ctx := context.Background()
	cfg, _ := json.Marshal(map[string]any{"database": map[string]any{"sqlite": map[string]any{"path": dbPath}}})
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, string(cfg))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = run.Stop(ctx) }()
	var n int
	if err := database.From(run.App).Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM goose_db_version WHERE version_id = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("migration 1 recorded %d times, want once", n)
	}
}

// A lock held by someone else fails the start after open_lock_timeout with an
// error naming the lock file, and releasing it lets the next start through.
func TestOpenLockTimeoutAndRelease(t *testing.T) {
	useMigrations(t)
	dbPath := filepath.Join(t.TempDir(), "app.db")
	lockPath := dbPath + ".open.lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	cfg := `{"database":{"sqlite":{"path":"` + dbPath + `","open_lock_timeout":"150ms"}}}`
	start := time.Now()
	_, err = boot.Start(context.Background(), "t", []string{"health", "database"}, cfg)
	if err == nil || !strings.Contains(err.Error(), lockPath) || !strings.Contains(err.Error(), "open_lock_timeout") {
		t.Fatalf("held lock: err = %v, want a timeout naming %s", err, lockPath)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 5*time.Second {
		t.Fatalf("timeout took %s, want about 150ms", d)
	}
	_ = f.Close() // releases the flock
	run, err := boot.Start(context.Background(), "t", []string{"health", "database"}, cfg)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	_ = run.Stop(context.Background())
}

// A leftover lock file with no holder (a crashed process) blocks nothing, and a
// holder that exits releases the lock.
func TestLeftoverLockFileIsNotStale(t *testing.T) {
	useMigrations(t)
	dbPath := filepath.Join(t.TempDir(), "app.db")
	if err := os.WriteFile(dbPath+".open.lock", []byte("pid 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := boot.Start(context.Background(), "t", []string{"health", "database"},
		`{"database":{"sqlite":{"path":"`+dbPath+`","open_lock_timeout":"2s"}}}`)
	if err != nil {
		t.Fatalf("leftover lock file blocked the start: %v", err)
	}
	_ = run.Stop(context.Background())
}

func TestOpenLockTimeoutValidated(t *testing.T) {
	for _, v := range []string{`"soon"`, `"0s"`, `"-1s"`} {
		_, err := boot.Start(context.Background(), "t", []string{"health", "database"},
			`{"database":{"sqlite":{"path":"`+filepath.Join(t.TempDir(), "a.db")+`","open_lock_timeout":`+v+`}}}`)
		if err == nil || !strings.Contains(err.Error(), "open_lock_timeout") {
			t.Errorf("%s: err = %v", v, err)
		}
	}
}
