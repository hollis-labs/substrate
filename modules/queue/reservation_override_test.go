package queue_test

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/modules/queue"
)

// reservation_secs set as a bare integer in the SUBSTRATE_CONFIG override file
// reaches the sqlite driver: a reserved job is not handed out again inside the
// window and is handed out again after it. No handler runs (autostart is off), so
// nothing can block a cleanup.
func TestReservationSecsFromAnOverrideFileTakesEffect(t *testing.T) {
	queue.Reset()
	run := startOverride(t, "  autostart: false\n  reservation_secs: 2\n")
	ctx := context.Background()
	if err := queue.Push(ctx, run.App, "long", []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	q := queue.Queue(run.App)
	if j, err := q.Pop(ctx, "default"); err != nil || j == nil {
		t.Fatalf("first pop: %v %v", j, err)
	}
	start := time.Now()
	time.Sleep(900 * time.Millisecond)
	if j, _ := q.Pop(ctx, "default"); j != nil {
		t.Fatal("the reserved job was handed out again inside the 2s window")
	}
	for time.Since(start) < 5*time.Second {
		if j, _ := q.Pop(ctx, "default"); j != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("reservation_secs from the override file was ignored: the job was not handed out again within 5s (default window is 60s)")
}
