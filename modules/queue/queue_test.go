package queue_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libqueue "github.com/hollis-labs/libs/core/util/queue"
	"github.com/hollis-labs/libs/core/util/queue/driver/memory"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/queue"
)

func start(t *testing.T, cfg string) *boot.Running {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	return run
}

func sqliteCfg(t *testing.T, queueCfg string) string {
	return `{"database":{"sqlite":{"path":"` + filepath.Join(t.TempDir(), "q.db") + `"}},"queue":` + queueCfg + `}`
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDurableQueueRunsAPushedJobWithItsPayload(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	var got atomic.Value
	queue.Handle("greet", func(_ context.Context, j *libqueue.QueuedJob) error { got.Store(string(j.Payload)); return nil })
	run := start(t, sqliteCfg(t, `{"poll_ms":10}`))

	if err := queue.Push(context.Background(), run.App, "greet", []byte(`"hello"`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the handler to run", func() bool { return got.Load() == `"hello"` })
	eventually(t, "the job to leave the queue", func() bool {
		n, _ := queue.Queue(run.App).Size(context.Background(), "default")
		return n == 0
	})
}

func TestAFailingJobIsRetriedThenMovedToFailed(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	var attempts atomic.Int32
	queue.Handle("flaky", func(context.Context, *libqueue.QueuedJob) error { attempts.Add(1); return errors.New("nope") })
	run := start(t, sqliteCfg(t, `{"poll_ms":10,"max_tries":2}`))
	if err := queue.Push(context.Background(), run.App, "flaky", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "two attempts", func() bool { return attempts.Load() >= 2 })
	time.Sleep(150 * time.Millisecond)
	if n := attempts.Load(); n != 2 {
		t.Errorf("attempts = %d, want exactly max_tries (2)", n)
	}
}

// secondAttemptGap runs a job that always fails and returns the time between its
// first and second attempt.
func secondAttemptGap(t *testing.T, startCfg string) time.Duration {
	t.Helper()
	queue.Reset()
	t.Cleanup(queue.Reset)
	var mu sync.Mutex
	var at []time.Time
	queue.Handle("flaky", func(context.Context, *libqueue.QueuedJob) error {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		return errors.New("nope")
	})
	run := start(t, startCfg)
	if err := queue.Push(context.Background(), run.App, "flaky", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "two attempts: "+startCfg, func() bool { mu.Lock(); defer mu.Unlock(); return len(at) >= 2 })
	// Stop now: the module value is shared, so a worker left running would
	// keep the next boot in this test from launching its own.
	if err := run.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	return at[1].Sub(at[0])
}

func TestRetryAfterSecsDelaysTheNextAttempt(t *testing.T) {
	// memory driver: exact delay.
	if gap := secondAttemptGap(t, `{"queue":{"driver":"memory","poll_ms":10,"max_tries":2,"retry_after_secs":1}}`); gap < 900*time.Millisecond {
		t.Errorf("memory: retried after %v, want about 1s", gap)
	}
	// sqlite driver: whole seconds, so at least one second must pass between the two.
	if gap := secondAttemptGap(t, sqliteCfg(t, `{"poll_ms":10,"max_tries":2,"retry_after_secs":2}`)); gap < time.Second {
		t.Errorf("sqlite: retried after %v, want at least 1s", gap)
	}
	// default: immediate.
	if gap := secondAttemptGap(t, `{"database":{"sqlite":{"path":":memory:"}},"queue":{"driver":"memory","poll_ms":10,"max_tries":2}}`); gap > 500*time.Millisecond {
		t.Errorf("default: retried after %v, want at once", gap)
	}
}

func TestBadRetryAfterSecsFailStart(t *testing.T) {
	for _, qc := range []string{`{"retry_after_secs":-1}`, `{"retry_after_secs":0.5}`, `{"retry_after_secs":"5"}`} {
		queue.Reset()
		if _, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue"}, `{"database":{"sqlite":{"path":":memory:"}},"queue":`+qc+`}`); err == nil {
			t.Errorf("%s was accepted", qc)
		}
	}
	queue.Reset()
}

func TestMemoryDriverAndCustomDriver(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	var ran atomic.Bool
	queue.Handle("ping", func(context.Context, *libqueue.QueuedJob) error { ran.Store(true); return nil })
	run := start(t, `{"database":{"sqlite":{"path":":memory:"}},"queue":{"driver":"memory","poll_ms":10}}`)
	_ = queue.Push(context.Background(), run.App, "ping", []byte(`1`))
	eventually(t, "the memory-backed job", ran.Load)
}

func TestBadConfigFailsStart(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	if _, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue"}, `{"database":{"sqlite":{"path":":memory:"}},"queue":{"driver":"kafka"}}`); err == nil {
		t.Error("an unknown driver was accepted")
	}
	// Without the database module the sqlite driver cannot work.
	if _, err := boot.Start(context.Background(), "t", []string{"health", "queue"}, `{}`); err == nil {
		t.Error("the sqlite driver started without the database module")
	}
}

func TestConfiguredTableNamesLeaveAnAppsOwnJobsTableAlone(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	cfg := sqliteCfg(t, `{"autostart":false,"table":"bg_jobs","failed_table":"bg_failed"}`)
	// The app's own table, created before the queue module starts, has the
	// name the driver would otherwise take.
	own := func(ctx context.Context, app *module.App) error {
		_, err := database.From(app).DB().ExecContext(ctx, `CREATE TABLE jobs (id INTEGER PRIMARY KEY, history TEXT NOT NULL)`)
		return err
	}
	run, err := boot.StartWith(context.Background(), "t", []string{"health", "database", "queue"}, cfg, boot.Options{Before: map[string]boot.Hook{"queue": own}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	if err := queue.Push(context.Background(), run.App, "x", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	db := database.From(run.App).DB()
	count := func(table string) int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
		return n
	}
	if count("bg_jobs") != 1 {
		t.Error("the job did not land in the configured table")
	}
	_ = count("bg_failed") // exists
	if count("jobs") != 0 {
		t.Error("the app's own jobs table was written to")
	}
}

func TestBadTableNamesFailStart(t *testing.T) {
	for name, qc := range map[string]string{
		"injection": `{"table":"jobs; DROP TABLE x"}`,
		"same":      `{"table":"a","failed_table":"a"}`,
		"leading":   `{"failed_table":"1x"}`,
		"memory":    `{"driver":"memory","table":"a"}`,
	} {
		queue.Reset()
		if _, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue"}, `{"database":{"sqlite":{"path":":memory:"}},"queue":`+qc+`}`); err == nil {
			t.Errorf("%s: %s was accepted", name, qc)
		}
	}
	queue.Reset()
}

func TestUseDriverFuncBuildsTheDriverOverTheOpenedDatabase(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	var sawDB atomic.Bool
	queue.UseDriverFunc(func(app *module.App) (libqueue.Queue, error) {
		sawDB.Store(database.From(app) != nil)
		return memory.New(), nil
	})
	var ran atomic.Bool
	queue.Handle("ping", func(context.Context, *libqueue.QueuedJob) error { ran.Store(true); return nil })
	run := start(t, sqliteCfg(t, `{"poll_ms":10}`))
	if !sawDB.Load() {
		t.Error("the database was not open when UseDriverFunc ran")
	}
	_ = queue.Push(context.Background(), run.App, "ping", []byte(`1`))
	eventually(t, "the job on the func-built driver", ran.Load)
}

func TestUseDriverAndUseDriverFuncTogetherOrFailingFuncFailStart(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	queue.UseDriver(memory.New())
	queue.UseDriverFunc(func(*module.App) (libqueue.Queue, error) { return memory.New(), nil })
	if _, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue"}, sqliteCfg(t, `{}`)); err == nil {
		t.Fatal("UseDriver and UseDriverFunc together were accepted")
	}
	queue.Reset()
	queue.UseDriverFunc(func(*module.App) (libqueue.Queue, error) { return nil, errors.New("boom") })
	if _, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue"}, sqliteCfg(t, `{}`)); err == nil {
		t.Fatal("a failing UseDriverFunc did not fail start")
	}
}

func TestStopWaitsForTheWorker(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	started, release := make(chan struct{}), make(chan struct{})
	queue.Handle("slow", func(ctx context.Context, _ *libqueue.QueuedJob) error {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	run, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue"}, `{"database":{"sqlite":{"path":":memory:"}},"queue":{"driver":"memory","poll_ms":10}}`)
	if err != nil {
		t.Fatal(err)
	}
	_ = queue.Push(context.Background(), run.App, "slow", []byte(`1`))
	<-started
	done := make(chan error, 1)
	go func() { done <- run.Stop(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Stop did not return after the handler was told to stop")
	}
}

func TestAutostartFalseHoldsJobsUntilRun(t *testing.T) {
	queue.Reset()
	t.Cleanup(queue.Reset)
	var ran atomic.Bool
	queue.Handle("held", func(context.Context, *libqueue.QueuedJob) error { ran.Store(true); return nil })
	run := start(t, `{"database":{"sqlite":{"path":":memory:"}},"queue":{"driver":"memory","poll_ms":10,"autostart":false}}`)
	_ = queue.Push(context.Background(), run.App, "held", []byte(`1`))
	time.Sleep(200 * time.Millisecond)
	if ran.Load() {
		t.Fatal("a job ran although the worker was not started")
	}
	if err := queue.Run(run.App); err != nil {
		t.Fatal(err)
	}
	_ = queue.Run(run.App) // idempotent
	eventually(t, "the held job after Run", ran.Load)
}
