package scheduler_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	libsched "github.com/hollis-labs/libs/core/util/scheduler"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/config"
	"github.com/hollis-labs/substrate/modules/scheduler"
)

// startWithOverride boots config, database and scheduler with the scheduler block
// of the SUBSTRATE_CONFIG override file set to yamlBlock (bare integers) and a
// recording runner in place of the queue.
func startWithOverride(t *testing.T, yamlBlock string) (*boot.Running, *recorder) {
	t.Helper()
	reset(t)
	rec := &recorder{}
	scheduler.UseRunner(rec)
	p := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(p, []byte("scheduler:\n"+yamlBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	c := `{"database":{"sqlite":{"path":"` + filepath.Join(t.TempDir(), "s.db") + `"}}}`
	run, err := boot.Start(context.Background(), "t", []string{"config", "health", "database", "scheduler"}, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	return run, rec
}

func due(id string) libsched.Schedule {
	return libsched.Schedule{ID: id, Enabled: true, Interval: time.Hour, NextRun: time.Now().Add(-time.Second), JobType: "j"}
}

// tick_ms: 20 from the override file takes effect: a schedule created after the
// start fires well inside the 1s default tick.
func TestTickMsFromAnOverrideFileTakesEffect(t *testing.T) {
	run, rec := startWithOverride(t, "  tick_ms: 20\n")
	create(t, run, due("a"))
	deadline := time.Now().Add(600 * time.Millisecond)
	for rec.jobs.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no fire within 600ms: tick_ms from the override file was ignored (default tick is 1s)")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// due_batch: 1 takes effect: of three due schedules one fires per tick, where the
// default batch of 100 would fire all three in the first tick.
func TestDueBatchFromAnOverrideFileTakesEffect(t *testing.T) {
	run, rec := startWithOverride(t, "  tick_ms: 400\n  due_batch: 1\n")
	for _, id := range []string{"a", "b", "c"} {
		create(t, run, due(id))
	}
	eventually(t, "the first fire", func() bool { return rec.jobs.Load() > 0 })
	time.Sleep(150 * time.Millisecond) // well inside the next 400ms tick
	if n := rec.jobs.Load(); n != 1 {
		t.Fatalf("%d schedules fired in the first tick, want 1: due_batch from the override file was ignored", n)
	}
	eventually(t, "all three, one per tick", func() bool { return rec.jobs.Load() == 3 })
}
