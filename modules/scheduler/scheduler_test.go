package scheduler_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	libqueue "github.com/hollis-labs/libs/core/util/queue"
	libsched "github.com/hollis-labs/libs/core/util/scheduler"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/database"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/queue"
	"github.com/hollis-labs/substrate/modules/scheduler"
)

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func cfg(t *testing.T) string {
	return `{"database":{"sqlite":{"path":"` + filepath.Join(t.TempDir(), "s.db") + `"}},"scheduler":{"tick_ms":20},"queue":{"poll_ms":10}}`
}

func reset(t *testing.T) {
	scheduler.Reset()
	queue.Reset()
	t.Cleanup(func() { scheduler.Reset(); queue.Reset() })
}

func create(t *testing.T, run *boot.Running, s libsched.Schedule) {
	t.Helper()
	st, ok := scheduler.Store(run.App).(*sqlstore.Store)
	if !ok {
		t.Fatalf("store is %T", scheduler.Store(run.App))
	}
	if err := st.CreateSchedule(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

// A schedule fires, the default runner pushes it to the queue, the worker runs
// the app's handler: the three modules compose with no glue code.
func TestScheduleFiresThroughTheQueueToTheHandler(t *testing.T) {
	reset(t)
	var payload atomic.Value
	queue.Handle("tick", func(_ context.Context, j *libqueue.QueuedJob) error { payload.Store(string(j.Payload)); return nil })
	run, err := boot.Start(context.Background(), "t", []string{"health", "database", "queue", "scheduler"}, cfg(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })

	create(t, run, libsched.Schedule{
		ID: "every-50ms", Enabled: true, Interval: 50 * time.Millisecond, NextRun: time.Now().Add(-time.Second),
		JobType: "tick", Payload: []byte(`"from-the-schedule"`),
	})
	eventually(t, "the scheduled job to reach its handler", func() bool { return payload.Load() == `"from-the-schedule"` })
}

type recorder struct{ jobs atomic.Int32 }

func (r *recorder) Enqueue(context.Context, libsched.Job) error { r.jobs.Add(1); return nil }

func TestACustomRunnerReplacesTheQueue(t *testing.T) {
	reset(t)
	rec := &recorder{}
	scheduler.UseRunner(rec)
	run, err := boot.Start(context.Background(), "t", []string{"health", "database", "scheduler"}, cfg(t)) // no queue module
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	create(t, run, libsched.Schedule{ID: "x", Enabled: true, Interval: 50 * time.Millisecond, NextRun: time.Now().Add(-time.Second), JobType: "j"})
	eventually(t, "the custom runner", func() bool { return rec.jobs.Load() > 0 })
}

func TestStartNeedsAStoreOrTheDatabase(t *testing.T) {
	reset(t)
	if _, err := boot.Start(context.Background(), "t", []string{"health", "scheduler"}, `{}`); err == nil {
		t.Fatal("started with no database and no store")
	}
}

func TestStopStopsTheEngine(t *testing.T) {
	reset(t)
	rec := &recorder{}
	scheduler.UseRunner(rec)
	run, err := boot.Start(context.Background(), "t", []string{"health", "database", "scheduler"}, cfg(t))
	if err != nil {
		t.Fatal(err)
	}
	create(t, run, libsched.Schedule{ID: "x", Enabled: true, Interval: 30 * time.Millisecond, NextRun: time.Now().Add(-time.Second), JobType: "j"})
	eventually(t, "a first fire", func() bool { return rec.jobs.Load() > 0 })
	if err := run.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := rec.jobs.Load()
	time.Sleep(200 * time.Millisecond)
	if rec.jobs.Load() != n {
		t.Error("the engine kept firing after Stop")
	}
}

func TestAutostartFalseHoldsFiresUntilRun(t *testing.T) {
	reset(t)
	rec := &recorder{}
	scheduler.UseRunner(rec)
	c := `{"database":{"sqlite":{"path":"` + filepath.Join(t.TempDir(), "s.db") + `"}},"scheduler":{"tick_ms":20,"autostart":false}}`
	run, err := boot.Start(context.Background(), "t", []string{"health", "database", "scheduler"}, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	create(t, run, libsched.Schedule{ID: "x", Enabled: true, Interval: 30 * time.Millisecond, NextRun: time.Now().Add(-time.Second), JobType: "j"})
	time.Sleep(250 * time.Millisecond)
	if rec.jobs.Load() != 0 {
		t.Fatal("a schedule fired although the engine was not started")
	}
	if err := scheduler.Run(run.App); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a fire after Run", func() bool { return rec.jobs.Load() > 0 })
}
