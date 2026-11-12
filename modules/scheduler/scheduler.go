// Package scheduler is the timed-activation module: libs core/util/scheduler
// (durable schedules, fire identity, leased claims, retries, misfire and overlap
// policy) started with the app. The app owns what a job means; this module owns
// the engine, its store and their lifecycle.
//
// By default a fired job is pushed onto the queue module's queue as a job of
// the schedule's JobType with its payload, so a worker handles it. Apps that
// dispatch differently call scheduler.UseRunner before boot.Start.
//
// Create schedules through the store: scheduler.Store(app).CreateSchedule(ctx, sched).
//
// Config under "scheduler":
//
//	tick_ms:             how often due schedules are checked (default 1000).
//	due_batch:           schedules and fires handled per tick (default 100).
//	claim_lease_seconds: how long a claimed fire is exclusive before another
//	                     process may redeliver it (default 600).
//	autostart:           start the engine with the app (default true). Set false for
//	                     an app whose binary is also a CLI, and call
//	                     scheduler.Run(app) from the daemon command, so a short-lived
//	                     command never fires schedules.
//
// libs provides a reference store for SQLite only. With Postgres, or an app that
// keeps schedules in its own tables, supply a Store with scheduler.UseStore, or
// scheduler.UseStoreFunc when it is built over the app's database (libs ships a
// conformance suite for it). UseOptionsFunc adds engine options such as an observer.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/store"
	libqueue "github.com/hollis-labs/libs/core/util/queue"
	libsched "github.com/hollis-labs/libs/core/util/scheduler"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"
)

// Key under which the *libsched.Engine is published; StoreKey publishes the store.
const (
	Key      = "scheduler.engine"
	StoreKey = "scheduler.store"
)

var (
	mu         sync.Mutex
	runner     libsched.Runner
	custom     libsched.Store
	storeFunc  func(*module.App) (libsched.Store, error)
	optionFunc func(libsched.Store) []libsched.Option
)

// UseRunner supplies the dispatcher for fired jobs, replacing the default
// (push to the queue module). Call before boot.Start.
func UseRunner(r libsched.Runner) { mu.Lock(); runner = r; mu.Unlock() }

// UseStore supplies the schedule store, replacing the reference SQLite one.
func UseStore(s libsched.Store) { mu.Lock(); custom = s; mu.Unlock() }

// UseStoreFunc supplies the schedule store lazily: f runs when the module starts,
// after the modules it requires (so database.From(app) works), for a store that
// is built over the app's own database. Use it instead of UseStore when the store
// cannot exist before boot.Start.
func UseStoreFunc(f func(app *module.App) (libsched.Store, error)) {
	mu.Lock()
	storeFunc = f
	mu.Unlock()
}

// UseOptionsFunc supplies extra engine options (an observer, a claim lease) once
// the store exists. They are applied after the config defaults, so they win.
func UseOptionsFunc(f func(store libsched.Store) []libsched.Option) {
	mu.Lock()
	optionFunc = f
	mu.Unlock()
}

// Reset clears the hooks (for tests).
func Reset() {
	mu.Lock()
	runner, custom, storeFunc, optionFunc = nil, nil, nil, nil
	mu.Unlock()
}

func init() { module.Register(&mod{}) }

type mod struct{ eng *libsched.Engine }

func (*mod) Name() string { return "scheduler" }

// queueRunner is the default Runner: it pushes the fired job onto the queue
// module's queue, found by key at dispatch time so module order does not matter.
type queueRunner struct{ app *module.App }

func (r queueRunner) Enqueue(ctx context.Context, job libsched.Job) error {
	v, _ := r.app.Value(queueKey)
	q, ok := v.(libqueue.Queue)
	if !ok {
		return errors.New("scheduler: no runner is set and the queue module is not started; call scheduler.UseRunner or add the queue module")
	}
	return q.Push(ctx, job.JobType, job.Payload)
}

// queueKey is the key the queue module publishes its queue under.
const queueKey = "queue.queue"

func (m *mod) Start(ctx context.Context, app *module.App) error {
	cfg, _ := app.Config["scheduler"].(map[string]any)
	num := func(k string, def float64) float64 {
		if n, ok := cfg[k].(float64); ok && n > 0 {
			return n
		}
		return def
	}
	mu.Lock()
	r, st, sf, of := runner, custom, storeFunc, optionFunc
	mu.Unlock()
	if st != nil && sf != nil {
		return errors.New("scheduler: set UseStore or UseStoreFunc, not both")
	}
	if sf != nil {
		built, err := sf(app)
		if err != nil {
			return fmt.Errorf("scheduler: store: %w", err)
		}
		st = built
	}

	schedStore := st
	if schedStore == nil {
		db := database.From(app)
		if db == nil {
			return errors.New("scheduler: needs the database module (or call scheduler.UseStore)")
		}
		if db.Config().Driver != store.DriverSQLite {
			return errors.New("scheduler: libs has a SQLite reference store only; with Postgres call scheduler.UseStore")
		}
		if err := sqlstore.Migrate(ctx, db.DB()); err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
		s, err := sqlstore.New(db.DB())
		if err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
		schedStore = s
	}
	if r == nil {
		r = queueRunner{app: app}
	}
	opts := []libsched.Option{
		libsched.WithTickCadence(time.Duration(num("tick_ms", 1000)) * time.Millisecond),
		libsched.WithDueBatchLimit(int(num("due_batch", 100))),
		libsched.WithClaimLease(time.Duration(num("claim_lease_seconds", 600)) * time.Second),
	}
	if of != nil {
		opts = append(opts, of(schedStore)...)
	}
	eng := libsched.New(schedStore, r, opts...)
	m.eng = eng
	app.Set(Key, eng)
	app.Set(StoreKey, schedStore)
	if auto, ok := cfg["autostart"].(bool); !ok || auto {
		eng.Start() // idempotent
	}
	return nil
}

// Run starts the engine if it is not running (see autostart). It is idempotent.
func Run(app *module.App) error {
	eng := Engine(app)
	if eng == nil {
		return errors.New("scheduler module is not started")
	}
	eng.Start()
	return nil
}

func (m *mod) Stop(context.Context) error {
	if m.eng != nil {
		m.eng.Stop()
		m.eng = nil
	}
	return nil
}

// Engine returns the running engine, or nil.
func Engine(app *module.App) *libsched.Engine {
	v, _ := app.Value(Key)
	e, _ := v.(*libsched.Engine)
	return e
}

// Store returns the schedule store, or nil. With the reference store the value is
// a *sqlstore.Store with CreateSchedule, GetSchedule, ListSchedules and DeleteSchedule.
func Store(app *module.App) libsched.Store {
	v, _ := app.Value(StoreKey)
	s, _ := v.(libsched.Store)
	return s
}
