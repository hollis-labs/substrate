package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/health"
)

// Named stores.
//
// An app has one default store (the "database" block above, published under Key
// and read with From). It can also have named stores, so it can keep its
// boot-critical data (settings, definitions, identity) in a small local store
// that always starts, and its growing data (events, logs, telemetry, domain
// tables) in another store, usually Postgres, that may be down without taking
// the app down:
//
//	database:
//	  sqlite: {path: local.db}            # the default store, always required
//	  start_wait: 3s                      # how long Start waits for optional stores' first attempt (default 3s, 0s = do not wait)
//	  stores:
//	    data:
//	      driver: postgres                # required: a named store never defaults to the default store's file
//	      required: false                 # default true
//	      schema_mode: verify             # default for Postgres
//	      postgres:
//	        dsn_file: /run/secrets/data.dsn
//	        migrate_dsn_file: /run/secrets/data-migrate.dsn   # schema-owner role, used only to migrate
//	      connect_timeout: 10s            # per attempt, default 10s
//	      retry: {initial: 1s, max: 30s}  # backoff between attempts
//	      ping_interval: 5s               # liveness check once connected
//
// A named store has the same block as the default store plus the keys above;
// unknown keys are an error naming the store and the key. A named SQLite store
// needs an explicit sqlite.path, and no two SQLite stores may be the same file.
// Register a store's migrations with UseNamedMigrationFS and read it with Named.
//
// A required store (the default, and any named store unless required: false)
// fails Start if it cannot be opened, and a required store that goes down later
// makes /healthz answer 503. A store with required: false never fails Start for
// an outage: its first attempt runs beside Start (Start waits at most start_wait
// for all of them together, so a black-holed server cannot hold up boot), and
// if it fails the app comes up with the store unavailable while a background
// loop keeps connecting with exponential backoff. While it is unavailable
// Handle.Get returns ErrUnavailable, /healthz reports "degraded" with HTTP 200
// (the process is serving; a 503 would make a deploy gate roll it back or a
// supervisor restart it) naming the store and its target (host:port/database,
// never credentials), and Gate answers 503 database_unavailable. Nothing is
// queued or written locally: a refused write is refused. When the server
// returns the store becomes ready again by itself; there is no manual step.
//
// States. unavailable: the server cannot be reached, will be retried. failed:
// the error will not fix itself without a human or a deploy (a misconfigured
// or rotated DSN, a rejected login, a missing database, a schema that is behind
// or a migration that failed); the loop keeps trying, at the slowest backoff, so
// the fix is picked up without a restart, but the log and health say it is not
// an outage. A misconfiguration found at Start (bad name, missing or unknown
// key, literal DSN, both dsn_file and dsn_env) fails Start; an unreadable
// secret is an outage, because it may be provisioned after the app starts.
//
// Migrations. A Postgres named store defaults to schema_mode verify, so the
// running app does not migrate a shared server. A deploy step runs them: start
// the app once with the store's schema_mode set to auto (SUBSTRATE_CONFIG
// override) and migrate_dsn_file or migrate_dsn_env naming the schema-owner
// role; the runtime role never needs DDL rights. If the app starts while the
// schema is behind, the store is failed (health degraded, data endpoints 503)
// and recovers by itself after the deploy step has migrated it.

// NamedKeyPrefix is the key prefix under which a named store's *Handle is
// published ("database.store.<name>").
const NamedKeyPrefix = "database.store."

// ErrUnavailable is returned (wrapped) by Handle.Get while the store is not
// connected. Test with errors.Is.
var ErrUnavailable = errors.New("database unavailable")

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// State is a named store's connection state.
type State string

const (
	// StateConnecting: no attempt has finished yet.
	StateConnecting State = "connecting"
	// StateReady: connected and the last liveness check passed.
	StateReady State = "ready"
	// StateUnavailable: not connected, or the last liveness check failed; will be retried.
	StateUnavailable State = "unavailable"
	// StateFailed: the last error will not fix itself (configuration, login, schema); still retried slowly.
	StateFailed State = "failed"
	// StateStopped: the module was stopped.
	StateStopped State = "stopped"
)

// Status is a point-in-time view of a named store, safe to serve to a UI banner
// or a status endpoint: it carries no credentials.
type Status struct {
	Name      string    `json:"name"`
	State     State     `json:"state"`
	Required  bool      `json:"required"`
	Target    string    `json:"target,omitempty"`     // host:port/database, or the SQLite file name
	LastError string    `json:"last_error,omitempty"` // redacted
	Since     time.Time `json:"since"`                // when State last changed
	Attempts  int       `json:"attempts"`             // connection attempts so far
}

var (
	namedMu         sync.Mutex
	namedMigrations = map[string]*migrate.Source{}
)

// UseNamedMigrations registers the migration source for a named store, like
// UseMigrations for the default store. Call it before boot.Start. An empty
// Source clears it.
//
// Each named store keeps its own goose version table, "goose_db_version_<name>",
// unless src.Table says otherwise. Two stores that share one database would
// otherwise share one version table, and the second store's migrations would be
// skipped as already applied.
func UseNamedMigrations(name string, src migrate.Source) {
	namedMu.Lock()
	defer namedMu.Unlock()
	if src.FS == nil {
		delete(namedMigrations, name)
		return
	}
	if src.Table == "" {
		src.Table = migrate.DefaultTable + "_" + name
	}
	namedMigrations[name] = &src
}

// UseNamedMigrationFS is UseNamedMigrations from an embedded file system whose
// Root holds one directory per dialect (root/sqlite, root/postgres).
func UseNamedMigrationFS(name string, fsys fs.FS, root string) {
	UseNamedMigrations(name, migrate.Source{FS: fsys, Root: root})
}

func namedSource(name string) *migrate.Source {
	namedMu.Lock()
	defer namedMu.Unlock()
	return namedMigrations[name]
}

// Handle is a named store that can be unavailable. Get it with Named. All
// methods are safe on a nil Handle, which behaves as a store that is never
// available, so a misspelt name cannot panic a route.
type Handle struct {
	name     string
	required bool
	log      *slog.Logger

	connect func(ctx context.Context) (*store.Store, string, error) // store, target
	connTO  time.Duration
	retry   backoff
	pingInt time.Duration

	mu       sync.RWMutex
	st       *store.Store
	state    State
	target   string
	lastErr  string
	since    time.Time
	attempts int

	firstOnce sync.Once
	hooks     []func(Status)
	poke      chan struct{} // asks the loop to check liveness now

	ready   chan struct{} // closed the first time the store is ready
	first   chan struct{} // closed when the first attempt has finished
	stopped chan struct{} // closed by Stop
	cancel  context.CancelFunc
	done    chan struct{}
}

// Name is the store's name.
func (h *Handle) Name() string {
	if h == nil {
		return ""
	}
	return h.name
}

// Get returns the store while it is ready, and an error wrapping ErrUnavailable
// otherwise. It never blocks on the network: callers refuse the write or read
// with the error; nothing is queued.
func (h *Handle) Get() (*store.Store, error) {
	if h == nil {
		return nil, fmt.Errorf("%w: no such store", ErrUnavailable)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.state == StateReady && h.st != nil {
		return h.st, nil
	}
	return nil, fmt.Errorf("%w: store %q", ErrUnavailable, h.name)
}

// Ready reports whether Get would succeed.
func (h *Handle) Ready() bool {
	if h == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state == StateReady && h.st != nil
}

// Status returns the current status.
func (h *Handle) Status() Status {
	if h == nil {
		return Status{State: StateUnavailable}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Status{Name: h.name, State: h.state, Required: h.required, Target: h.target,
		LastError: h.lastErr, Since: h.since, Attempts: h.attempts}
}

// Wait blocks until the store has been ready at least once, the module is
// stopped (an error wrapping ErrUnavailable), or ctx ends.
func (h *Handle) Wait(ctx context.Context) error {
	if h == nil {
		return fmt.Errorf("%w: no such store", ErrUnavailable)
	}
	select {
	case <-h.ready:
		return nil
	default:
	}
	select {
	case <-h.ready:
		return nil
	case <-h.stopped:
		return fmt.Errorf("%w: store %q stopped", ErrUnavailable, h.name)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handle) set(state State, st *store.Store, target string, err error) {
	h.mu.Lock()
	prev, prevErr := h.state, h.lastErr
	if state != h.state {
		h.since = time.Now()
	}
	h.state = state
	if st != nil {
		h.st = st
	}
	if target != "" {
		h.target = target
	}
	if err != nil {
		h.lastErr = redactError(err)
	} else if state == StateReady {
		h.lastErr = ""
	}
	cur, curErr, attempts, tgt, since := h.state, h.lastErr, h.attempts, h.target, h.since
	var hooks []func(Status)
	var snap Status
	if cur != prev {
		hooks = append(hooks, h.hooks...)
		snap = Status{Name: h.name, State: h.state, Required: h.required, Target: h.target, LastError: h.lastErr, Since: h.since, Attempts: h.attempts}
	}
	h.mu.Unlock()
	for _, fn := range hooks {
		callHook(fn, snap, h.log)
	}

	// Log a transition, a changed error, and every 20th identical failure; never every retry.
	switch {
	case cur == StateReady && prev != StateReady && prev != "":
		h.log.Info("store ready", "target", tgt, "attempts", attempts)
	case cur == StateReady && prev == "":
		h.log.Info("store ready", "target", tgt)
	case cur == StateFailed && (prev != cur || prevErr != curErr || attempts%20 == 0):
		h.log.Error("store failed: needs a fix, retrying at the slowest backoff", "target", tgt, "attempt", attempts, "error", curErr)
	case cur == StateUnavailable && (prev != cur || prevErr != curErr || attempts%20 == 0):
		h.log.Warn("store unavailable, retrying", "target", tgt, "attempt", attempts, "error", curErr, "since", since.Format(time.RFC3339))
	}
}

// OnChange registers fn to be called, from the connection loop, every time the
// store's state changes (ready, unavailable, failed, stopped), with the new
// Status. Use it to re-arm work that depends on the store after an outage.
// fn must return quickly and must not call back into the Handle's Stop; a
// panic in it is recovered and logged.
func (h *Handle) OnChange(fn func(Status)) {
	if h == nil || fn == nil {
		return
	}
	h.mu.Lock()
	h.hooks = append(h.hooks, fn)
	h.mu.Unlock()
}

func callHook(fn func(Status), s Status, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("store OnChange hook panicked", "panic", fmt.Sprint(r))
		}
	}()
	fn(s)
}

// Report tells the handle that a query saw the server down. If err is a
// connection-level failure (see IsUnavailable) while the store is ready, the
// store flips to unavailable now and the loop checks liveness at once, instead
// of waiting for the next ping, so a handler that just got a driver error and
// the next one agree on 503. A false alarm costs one ping.
func (h *Handle) Report(err error) {
	if h == nil || err == nil || errors.Is(err, ErrUnavailable) || !IsUnavailable(err) {
		return
	}
	if h.Ready() {
		h.set(StateUnavailable, nil, "", err)
	}
	select {
	case h.poke <- struct{}{}:
	default:
	}
}

// unavailableError is what the health check reports while not ready.
func (h *Handle) unavailableError() error {
	s := h.Status()
	msg := fmt.Sprintf("database unavailable: store %s", s.Name)
	if s.Target != "" {
		msg += " (" + s.Target + ")"
	}
	if s.State == StateFailed {
		msg += " [failed]"
	}
	if s.LastError != "" {
		msg += ": " + s.LastError
	}
	return errors.New(msg)
}

// attempt tries to connect once.
func (h *Handle) attempt(ctx context.Context) error {
	h.mu.Lock()
	h.attempts++
	if h.state == "" {
		h.state, h.since = StateConnecting, time.Now()
	}
	h.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, h.connTO)
	defer cancel()
	st, target, err := h.connect(cctx)
	if err != nil {
		state := StateUnavailable
		if isPermanent(err) {
			state = StateFailed
		}
		h.set(state, nil, target, err)
		return err
	}
	h.set(StateReady, st, target, nil)
	select {
	case <-h.ready:
	default:
		close(h.ready)
	}
	return nil
}

// loop keeps the store connected: it retries with backoff until the first
// success, then checks liveness every pingInt and flips the state with it. The
// pool reconnects by itself, so a recovered server needs no reopen. An error
// that will not fix itself is retried at the slowest backoff.
func (h *Handle) loop(ctx context.Context) {
	defer close(h.done)
	markFirst := h.markFirst
	defer markFirst()
	delay := h.retry.initial
	for ctx.Err() == nil {
		h.mu.RLock()
		st := h.st
		h.mu.RUnlock()
		if st == nil {
			err := h.attempt(ctx)
			markFirst()
			if err == nil {
				delay = h.retry.initial
				continue
			}
			wait := delay
			if h.Status().State == StateFailed {
				wait = h.retry.max
			}
			if !sleep(ctx, jitter(wait)) {
				return
			}
			delay = h.retry.next(delay)
			continue
		}
		markFirst()
		if !sleepOrPoke(ctx, h.pingInt, h.poke) {
			return
		}
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := st.Ping(pctx)
		cancel()
		if err != nil {
			h.set(StateUnavailable, nil, "", err)
		} else {
			h.set(StateReady, nil, "", nil)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func sleepOrPoke(ctx context.Context, d time.Duration, poke <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-poke:
		return true
	case <-t.C:
		return true
	}
}

type backoff struct{ initial, max time.Duration }

func (b backoff) next(cur time.Duration) time.Duration {
	n := cur * 2
	if n > b.max {
		n = b.max
	}
	return n
}

// jitter spreads retries by up to 20% either way.
func jitter(d time.Duration) time.Duration {
	return d - d/5 + time.Duration(rand.Int64N(int64(d)*2/5+1))
}

// Named returns the named store published by the database module, or nil.
func Named(app *module.App, name string) *Handle {
	v, _ := app.Value(NamedKeyPrefix + name)
	h, _ := v.(*Handle)
	return h
}

// Statuses returns the status of every named store, ordered by name.
func Statuses(app *module.App) []Status {
	var out []Status
	for k, v := range app.Values {
		if strings.HasPrefix(k, NamedKeyPrefix) {
			if h, ok := v.(*Handle); ok {
				out = append(out, h.Status())
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

const defaultStartWait = 3 * time.Second

// startNamed builds and starts a Handle for every database.stores.<name>.
func (m *mod) startNamed(ctx context.Context, app *module.App, defaultPath string) error {
	cfg, _ := app.Config["database"].(map[string]any)
	stores, _ := cfg["stores"].(map[string]any)
	if len(stores) == 0 {
		return nil
	}
	startWait, err := durationAtZero(cfg, "start_wait", "database", defaultStartWait)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(stores))
	for n := range stores {
		names = append(names, n)
	}
	sort.Strings(names)

	// Build and validate every handle before connecting to anything.
	files := map[string]string{} // canonical sqlite path -> owner
	if defaultPath != "" {
		files[canonPath(defaultPath)] = "default"
	}
	handles := make([]*Handle, 0, len(names))
	for _, name := range names {
		raw, ok := stores[name].(map[string]any)
		if !ok {
			return fmt.Errorf("database: stores.%s must be a mapping", name)
		}
		h, sc, err := newHandle(app.Name, name, raw)
		if err != nil {
			return err
		}
		if sc.Driver == store.DriverSQLite && !isMemory(sc.SQLite.Path) {
			c := canonPath(sc.SQLite.Path)
			if owner, dup := files[c]; dup {
				return fmt.Errorf("database: stores.%s: sqlite.path %q is the same file as store %q; every store needs its own file", name, sc.SQLite.Path, owner)
			}
			files[c] = name
		}
		handles = append(handles, h)
	}

	for _, h := range handles {
		app.Set(NamedKeyPrefix+h.name, h)
		m.named = append(m.named, h)
		if reg := health.From(app); reg != nil {
			check := func(context.Context) error {
				if h.Ready() {
					return nil
				}
				return h.unavailableError()
			}
			if h.required {
				reg.Add("database."+h.name, check)
			} else {
				reg.AddSoft("database."+h.name, check)
			}
		}
	}

	// Required stores open now, one after the other, and fail Start.
	for _, h := range handles {
		if !h.required {
			continue
		}
		if err := h.attempt(ctx); err != nil {
			return fmt.Errorf("database: store %s: %w", h.name, err)
		}
		h.markFirst()
		h.startLoop(ctx)
	}
	// Optional stores connect beside Start; Start waits for all of them together, at most start_wait.
	var optional []*Handle
	for _, h := range handles {
		if h.required {
			continue
		}
		h.startLoop(ctx)
		optional = append(optional, h)
	}
	if len(optional) > 0 && startWait > 0 {
		timer := time.NewTimer(startWait)
		defer timer.Stop()
	wait:
		for _, h := range optional {
			select {
			case <-h.first:
			case <-timer.C:
				break wait
			case <-ctx.Done():
				break wait
			}
		}
	}
	return nil
}

// markFirst records that the first attempt has finished; safe to call twice.
func (h *Handle) markFirst() { h.firstOnce.Do(func() { close(h.first) }) }

func (h *Handle) startLoop(ctx context.Context) {
	lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	h.cancel = cancel
	go h.loop(lctx)
}

func (m *mod) stopNamed(ctx context.Context) error {
	var first error
	for _, h := range m.named {
		if h.cancel != nil {
			h.cancel()
			<-h.done
		}
		h.set(StateStopped, nil, "", nil)
		h.mu.Lock()
		st := h.st
		h.st = nil
		h.mu.Unlock()
		select {
		case <-h.stopped:
		default:
			close(h.stopped)
		}
		if st != nil {
			if err := st.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	m.named = nil
	return first
}

// MigrateNamed applies the migrations registered for a named store, over its
// migration credential (migrate_dsn_file or migrate_dsn_env), whatever its
// schema_mode says. It is for a deploy step: an app exposes it as a verb such as
// "<app> migrate --store data", run with the schema-owner role's secret. It
// reads the store's block from app.Config, so the module set it runs under needs
// only config and database. A running app whose store was failed because the
// schema was behind picks the change up on its next attempt.
func MigrateNamed(ctx context.Context, app *module.App, name string) error {
	cfg, _ := app.Config["database"].(map[string]any)
	stores, _ := cfg["stores"].(map[string]any)
	raw, ok := stores[name].(map[string]any)
	if !ok {
		return fmt.Errorf("database: no store %q in the database.stores config", name)
	}
	src := namedSource(name)
	if src == nil {
		return fmt.Errorf("database: stores.%s: no migrations are registered (UseNamedMigrationFS)", name)
	}
	if _, _, err := newHandle(app.Name, name, raw); err != nil {
		return err
	}
	sc, err := storeConfig(app.Name, raw)
	if err != nil {
		return &storeError{name: name, err: err}
	}
	sc.SchemaMode = store.SchemaAuto
	lockTO, err := openLockTimeoutFrom(raw)
	if err != nil {
		return &storeError{name: name, err: err}
	}
	st, err := openAndMigrate(ctx, sc, lockTO, src)
	if err != nil {
		return fmt.Errorf("database: stores.%s: %s", name, redactError(err))
	}
	return st.Close()
}

// Keys a named store block may contain; anything else is an error, so a typo
// ("retires", "requird") is not silently ignored.
var (
	storeKeys    = setOf("driver", "schema_mode", "sqlite", "postgres", "required", "connect_timeout", "retry", "ping_interval")
	sqliteKeys   = setOf("path", "synchronous", "journal", "read_pool", "open_lock_timeout")
	postgresKeys = setOf("dsn", "dsn_file", "dsn_env", "migrate_dsn", "migrate_dsn_file", "migrate_dsn_env", "allow_insecure", "max_conns", "application_name")
	retryKeys    = setOf("initial", "max")
)

func setOf(k ...string) map[string]bool {
	m := make(map[string]bool, len(k))
	for _, s := range k {
		m[s] = true
	}
	return m
}

func unknownKey(where string, m map[string]any, allowed map[string]bool) error {
	var bad []string
	for k := range m {
		if !allowed[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	ok := make([]string, 0, len(allowed))
	for k := range allowed {
		ok = append(ok, k)
	}
	sort.Strings(ok)
	return fmt.Errorf("database: %s: unknown key %q (known: %s)", where, bad[0], strings.Join(ok, ", "))
}

// storeError names the store in every error from its config.
type storeError struct {
	name string
	err  error
}

func (e *storeError) Error() string {
	return "database: stores." + e.name + ": " + strings.TrimPrefix(e.err.Error(), "database: ")
}
func (e *storeError) Unwrap() error { return e.err }

func newHandle(appName, name string, raw map[string]any) (*Handle, store.Config, error) {
	var none store.Config
	if !nameRE.MatchString(name) {
		return nil, none, fmt.Errorf("database: stores.%s: name must match %s", name, nameRE)
	}
	wrap := func(err error) error { return &storeError{name: name, err: err} }
	if err := unknownKey("stores."+name, raw, storeKeys); err != nil {
		return nil, none, err
	}
	if s, ok := raw["sqlite"].(map[string]any); ok {
		if err := unknownKey("stores."+name+".sqlite", s, sqliteKeys); err != nil {
			return nil, none, err
		}
	}
	if p, ok := raw["postgres"].(map[string]any); ok {
		if err := unknownKey("stores."+name+".postgres", p, postgresKeys); err != nil {
			return nil, none, err
		}
	}
	if r, ok := raw["retry"].(map[string]any); ok {
		if err := unknownKey("stores."+name+".retry", r, retryKeys); err != nil {
			return nil, none, err
		}
	}
	driver, _ := raw["driver"].(string)
	if driver == "" {
		return nil, none, fmt.Errorf("database: stores.%s: driver is required (sqlite or postgres); a named store never defaults to the default store's file", name)
	}
	if driver == string(store.DriverSQLite) {
		s, _ := raw["sqlite"].(map[string]any)
		if p, _ := s["path"].(string); p == "" {
			return nil, none, fmt.Errorf("database: stores.%s: sqlite.path is required", name)
		}
	}

	h := &Handle{name: name, required: true, log: slog.Default().With("module", "database", "store", name),
		ready: make(chan struct{}), first: make(chan struct{}), stopped: make(chan struct{}), done: make(chan struct{}),
		poke:   make(chan struct{}, 1),
		connTO: 10 * time.Second, pingInt: 5 * time.Second,
		retry: backoff{initial: time.Second, max: 30 * time.Second}}
	if v, ok := raw["required"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return nil, none, fmt.Errorf("database: stores.%s.required must be true or false", name)
		}
		h.required = b
	}
	var err error
	if h.connTO, err = durationAt(raw, "connect_timeout", name, h.connTO); err != nil {
		return nil, none, err
	}
	if h.pingInt, err = durationAt(raw, "ping_interval", name, h.pingInt); err != nil {
		return nil, none, err
	}
	if r, ok := raw["retry"].(map[string]any); ok {
		if h.retry.initial, err = durationAt(r, "initial", name+".retry", h.retry.initial); err != nil {
			return nil, none, err
		}
		if h.retry.max, err = durationAt(r, "max", name+".retry", h.retry.max); err != nil {
			return nil, none, err
		}
		if h.retry.max < h.retry.initial {
			return nil, none, fmt.Errorf("database: stores.%s.retry.max must be at least retry.initial", name)
		}
	}
	// Validate the shape before touching the network. A secret that cannot be
	// read yet is not a shape error.
	sc, err := storeConfig(appName, raw)
	if err != nil && !errors.Is(err, errSecret) {
		return nil, none, wrap(err)
	}
	lockTO, err := openLockTimeoutFrom(raw)
	if err != nil {
		return nil, none, wrap(err)
	}
	h.connect = func(ctx context.Context) (*store.Store, string, error) {
		sc, err := storeConfig(appName, raw) // re-reads the secret each attempt
		if err != nil {
			if errors.Is(err, errSecret) {
				return nil, "", wrap(err)
			}
			return nil, "", &permanentError{wrap(err)}
		}
		target := targetOf(sc)
		st, err := openAndMigrate(ctx, sc, lockTO, namedSource(name))
		return st, target, err
	}
	return h, sc, nil
}

func durationAt(m map[string]any, key, where string, def time.Duration) (time.Duration, error) {
	v, ok := m[key].(string)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("database: stores.%s.%s %q must be a positive duration such as 5s", where, key, v)
	}
	return d, nil
}

// durationAtZero is durationAt that also accepts 0s.
func durationAtZero(m map[string]any, key, where string, def time.Duration) (time.Duration, error) {
	v, ok := m[key].(string)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("database: %s.%s %q must be a duration such as 3s (0s to not wait)", where, key, v)
	}
	return d, nil
}

// canonPath is a SQLite path in a form two spellings of one file share.
func canonPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		p = filepath.Join(dir, filepath.Base(p))
	}
	return filepath.Clean(p)
}

// permanentError marks an error that will not fix itself.
type permanentError struct{ error }

func (e *permanentError) Unwrap() error { return e.error }

// migrateError is a failed or refused schema step.
type migrateError struct{ err error }

func (e *migrateError) Error() string { return "migrate: " + e.err.Error() }
func (e *migrateError) Unwrap() error { return e.err }

// reAuth matches the Postgres errors that need a human: invalid authorization
// (class 28: bad password, no such role) and a database that does not exist.
var reAuth = regexp.MustCompile(`SQLSTATE (28[0-9A-Z]{3}|3D000)`)

// isPermanent reports whether an error will not fix itself: a configuration
// problem, a refused or failed migration (including a schema that is behind in
// verify mode), a rejected login or a missing database.
func isPermanent(err error) bool {
	var pe *permanentError
	var me *migrateError
	return errors.As(err, &pe) || errors.As(err, &me) || reAuth.MatchString(err.Error())
}

var (
	// key=value secrets, including quoted values with escaped quotes.
	rePwd = regexp.MustCompile(`(?i)\b(password|passwd|pwd|sslpassword|passphrase)\s*=\s*('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|\S+)`)
	// everything between :// and the last @ of the word is treated as userinfo, even if an unescaped
	// "/" in a password makes it look like a path: over-masking beats leaking a fragment.
	reUserinfo = regexp.MustCompile(`(?i)(://)\S*@`)
	reKVTarget = regexp.MustCompile(`(?i)\b(host|port|dbname)\s*=\s*('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|\S+)`)
	reHost     = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)$`)
	rePort     = regexp.MustCompile(`^[0-9]{1,5}$`)
	reDBName   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)
)

// targetOf is where a store connects, for display: host:port/database for
// Postgres and the file name for SQLite. It never includes a user name or any
// part of a password: anything that does not parse cleanly collapses to
// "postgres".
func targetOf(sc store.Config) string {
	if sc.Driver == store.DriverSQLite {
		return filepath.Base(sc.SQLite.Path)
	}
	dsn := strings.TrimSpace(sc.Postgres.DSN)
	var host, port, db string
	if i := strings.Index(dsn, "://"); i > 0 && (strings.EqualFold(dsn[:i], "postgres") || strings.EqualFold(dsn[:i], "postgresql")) {
		rest := dsn[i+3:]
		auth, path := rest, ""
		if j := strings.IndexAny(rest, "/?"); j >= 0 {
			auth, path = rest[:j], rest[j:]
		}
		if k := strings.LastIndex(auth, "@"); k >= 0 {
			auth = auth[k+1:]
		}
		host, port = splitHostPort(auth)
		if strings.HasPrefix(path, "/") {
			db = strings.TrimPrefix(path, "/")
			if q := strings.IndexByte(db, '?'); q >= 0 {
				db = db[:q]
			}
			if u, err := url.PathUnescape(db); err == nil {
				db = u
			}
		}
	} else {
		for _, m := range reKVTarget.FindAllStringSubmatch(dsn, -1) {
			v := strings.Trim(m[2], `'"`)
			switch strings.ToLower(m[1]) {
			case "host":
				host = v
			case "port":
				port = v
			case "dbname":
				db = v
			}
		}
	}
	if !reHost.MatchString(host) || (port != "" && !rePort.MatchString(port)) || (db != "" && !reDBName.MatchString(db)) {
		return "postgres"
	}
	t := host
	if port != "" {
		t += ":" + port
	}
	if db != "" {
		t += "/" + db
	}
	return t
}

func splitHostPort(a string) (host, port string) {
	if strings.HasPrefix(a, "[") {
		if j := strings.Index(a, "]"); j > 0 {
			host = a[:j+1]
			if strings.HasPrefix(a[j+1:], ":") {
				port = a[j+2:]
			}
			return
		}
	}
	if j := strings.LastIndex(a, ":"); j >= 0 {
		return a[:j], a[j+1:]
	}
	return a, ""
}

// redactError turns an error into a short string safe to show: passwords in a
// key=value or URL form (including quoted values and a userinfo part) are
// masked, and it is cut to 240 characters.
func redactError(err error) string {
	s := rePwd.ReplaceAllString(err.Error(), "$1=***")
	s = reUserinfo.ReplaceAllString(s, "$1***@")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "..."
	}
	return s
}
