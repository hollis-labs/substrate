package queue_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libqueue "github.com/hollis-labs/libs/core/util/queue"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/config"
	"github.com/hollis-labs/substrate/modules/queue"
)

// startOverride boots config, database and queue with the queue block of the
// SUBSTRATE_CONFIG override file set to yamlBlock (bare integers). The manifest
// config carries only the database.
func startOverride(t *testing.T, yamlBlock string) *boot.Running {
	t.Helper()
	t.Cleanup(queue.Reset) // each test calls queue.Reset before queue.Handle
	p := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(p, []byte("queue:\n"+yamlBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	c := `{"database":{"sqlite":{"path":"` + filepath.Join(t.TempDir(), "q.db") + `"}}}`
	run, err := boot.Start(context.Background(), "t", []string{"config", "health", "database", "queue"}, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	return run
}

// poll_ms: 20 takes effect: a job pushed after the start runs well inside the
// 3s default poll.
func TestPollMsFromAnOverrideFileTakesEffect(t *testing.T) {
	var ran atomic.Bool
	queue.Reset()
	queue.Handle("j", func(context.Context, *libqueue.QueuedJob) error { ran.Store(true); return nil })
	run := startOverride(t, "  poll_ms: 20\n")
	if err := queue.Push(context.Background(), run.App, "j", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for !ran.Load() {
		if time.Now().After(deadline) {
			t.Fatal("job did not run within 1.5s: poll_ms from the override file was ignored (default poll is 3s)")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// max_tries: 2 takes effect: a job that always fails runs exactly twice, where
// the default is three attempts.
func TestMaxTriesFromAnOverrideFileTakesEffect(t *testing.T) {
	var attempts atomic.Int32
	queue.Reset()
	queue.Handle("flaky", func(context.Context, *libqueue.QueuedJob) error { attempts.Add(1); return errors.New("nope") })
	run := startOverride(t, "  poll_ms: 10\n  max_tries: 2\n")
	if err := queue.Push(context.Background(), run.App, "flaky", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "two attempts", func() bool { return attempts.Load() >= 2 })
	time.Sleep(300 * time.Millisecond)
	if n := attempts.Load(); n != 2 {
		t.Fatalf("attempts = %d, want exactly 2: max_tries from the override file was ignored", n)
	}
}

// concurrency: 2 takes effect: two jobs are in their handlers at the same time,
// which one worker (the default of 1) cannot do.
func TestConcurrencyFromAnOverrideFileTakesEffect(t *testing.T) {
	var inFlight, peak atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	queue.Reset()
	queue.Handle("slow", func(context.Context, *libqueue.QueuedJob) error {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
		return nil
	})
	run := startOverride(t, "  poll_ms: 10\n  concurrency: 2\n")
	for i := 0; i < 2; i++ {
		if err := queue.Push(context.Background(), run.App, "slow", []byte(`1`)); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "two handlers in flight at once", func() bool { return peak.Load() >= 2 })
	once.Do(func() { close(release) })
}

// retry_after_secs: 1 takes effect: the retry waits about a second. Before, a
// bare integer here failed start with "retry_after_secs must be a number".
func TestRetryAfterSecsFromAnOverrideFileTakesEffect(t *testing.T) {
	queue.Reset()
	var mu sync.Mutex
	var at []time.Time
	queue.Handle("flaky", func(context.Context, *libqueue.QueuedJob) error {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		return errors.New("nope")
	})
	run := startOverride(t, "  driver: memory\n  poll_ms: 10\n  max_tries: 2\n  retry_after_secs: 1\n")
	if err := queue.Push(context.Background(), run.App, "flaky", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "two attempts", func() bool { mu.Lock(); defer mu.Unlock(); return len(at) >= 2 })
	mu.Lock()
	gap := at[1].Sub(at[0])
	mu.Unlock()
	if gap < 900*time.Millisecond {
		t.Fatalf("retried after %v, want about 1s", gap)
	}
	_ = run
}
