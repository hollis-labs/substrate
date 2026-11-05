// Package queue is the background-job module: libs core/util/queue (a
// driver-based job queue with retries, priorities and a polling worker) started
// with the app. The app owns its job types and handlers; this module owns the
// queue, the worker and their lifecycle.
//
// Register handlers before boot.Start:
//
//	queue.Handle("email.send", func(ctx context.Context, job *libqueue.QueuedJob) error { ... })
//
// and enqueue with queue.Push(ctx, app, "email.send", payload, libqueue.WithDelay(...)).
//
// Config under "queue":
//
//	driver:       "sqlite" (default; durable, on the database module's SQLite store)
//	              or "memory" (tests, throwaway work). There is no Postgres driver in
//	              libs: with Postgres, supply one with queue.UseDriver.
//	queues:       queue names the worker polls, in priority order (default ["default"]).
//	concurrency:  polling goroutines (default 1).
//	poll_ms:      sleep between empty polls (default 3000).
//	max_tries:    default attempts per job (default 3; 0 is unlimited).
//	autostart:    start the worker with the app (default true). Set false for an
//	              app whose binary is also a CLI, and call queue.Run(app) from the
//	              daemon command, so a short-lived command never runs jobs.
package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/store"
	libqueue "github.com/hollis-labs/libs/core/util/queue"
	"github.com/hollis-labs/libs/core/util/queue/driver/memory"
	qsqlite "github.com/hollis-labs/libs/core/util/queue/driver/sqlite"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"
)

// Key under which the libqueue.Queue is published (read by scheduler by name).
const Key = "queue.queue"

var (
	mu       sync.Mutex
	handlers = map[string]libqueue.Handler{}
	custom   libqueue.Queue
)

// Handle registers the handler for a job type. Call before boot.Start.
func Handle(jobType string, h libqueue.Handler) {
	mu.Lock()
	handlers[jobType] = h
	mu.Unlock()
}

// UseDriver supplies the queue backend (for example one over Postgres). Call
// before boot.Start; it replaces the configured driver.
func UseDriver(q libqueue.Queue) { mu.Lock(); custom = q; mu.Unlock() }

// Reset clears registered handlers and the custom driver (for tests).
func Reset() {
	mu.Lock()
	handlers, custom = map[string]libqueue.Handler{}, nil
	mu.Unlock()
}

func init() { module.Register(&mod{}) }

type mod struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func (*mod) Name() string { return "queue" }

func (m *mod) Start(_ context.Context, app *module.App) error {
	cfg, _ := app.Config["queue"].(map[string]any)
	num := func(k string, def float64) float64 {
		if n, ok := cfg[k].(float64); ok {
			return n
		}
		return def
	}
	mu.Lock()
	q, hs := custom, make(map[string]libqueue.Handler, len(handlers))
	for k, v := range handlers {
		hs[k] = v
	}
	mu.Unlock()

	if q == nil {
		driver, _ := cfg["driver"].(string)
		switch driver {
		case "memory":
			q = memory.New()
		case "", "sqlite":
			st := database.From(app)
			if st == nil {
				return errors.New("queue: the sqlite driver needs the database module (or set queue.driver to memory, or call queue.UseDriver)")
			}
			if st.Config().Driver != store.DriverSQLite {
				return errors.New("queue: libs has a SQLite queue driver only; with Postgres call queue.UseDriver")
			}
			d, err := qsqlite.New(st.DB(), qsqlite.Opts{})
			if err != nil {
				return fmt.Errorf("queue: %w", err)
			}
			q = d
		default:
			return fmt.Errorf("queue: driver %q must be sqlite or memory", driver)
		}
	}

	var names []string
	if l, ok := cfg["queues"].([]any); ok {
		for _, v := range l {
			if s, ok := v.(string); ok && s != "" {
				names = append(names, s)
			}
		}
	}
	w := libqueue.NewWorker(q, libqueue.WorkerOpts{
		Queues:       names,
		Concurrency:  int(num("concurrency", 1)),
		PollInterval: time.Duration(num("poll_ms", 3000)) * time.Millisecond,
		MaxTries:     int(num("max_tries", 3)),
	})
	for t, h := range hs {
		w.Register(t, h)
	}
	launch := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cancel != nil {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel, m.done = cancel, make(chan struct{})
		go func(done chan struct{}) { defer close(done); _ = w.Start(ctx) }(m.done)
	}
	app.Set(Key, q)
	app.Set(launchKey, launch)
	if auto, ok := cfg["autostart"].(bool); !ok || auto {
		launch()
	}
	return nil
}

const launchKey = "queue.launch"

// Run starts the worker if it is not running (see autostart). It is idempotent.
func Run(app *module.App) error {
	v, _ := app.Value(launchKey)
	launch, ok := v.(func())
	if !ok {
		return errors.New("queue module is not started")
	}
	launch()
	return nil
}

func (m *mod) Stop(ctx context.Context) error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("queue: worker did not stop: %w", ctx.Err())
	}
	m.mu.Lock()
	m.cancel = nil
	m.mu.Unlock()
	return nil
}

// Queue returns the queue published by the module, or nil.
func Queue(app *module.App) libqueue.Queue {
	v, _ := app.Value(Key)
	q, _ := v.(libqueue.Queue)
	return q
}

// Push enqueues a job.
func Push(ctx context.Context, app *module.App, jobType string, payload []byte, opts ...libqueue.PushOption) error {
	q := Queue(app)
	if q == nil {
		return errors.New("queue module is not started")
	}
	return q.Push(ctx, jobType, payload, opts...)
}
