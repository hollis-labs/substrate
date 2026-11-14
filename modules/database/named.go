package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
//	  stores:
//	    data:
//	      required: false                 # default true
//	      driver: postgres
//	      schema_mode: verify
//	      postgres: {dsn_file: /run/secrets/data.dsn}
//	      connect_timeout: 10s            # per attempt, default 10s
//	      retry: {initial: 1s, max: 30s}  # backoff between attempts
//	      ping_interval: 5s               # liveness check once connected
//
// A named store has the same block as the default store plus the keys above.
// Register its migrations with UseNamedMigrationFS and read it with Named.
//
// A required store (the default) fails Start if it cannot be opened. A store
// with required: false never fails Start for an outage: Start tries once, and
// if that fails the app comes up with the store unavailable while a background
// loop keeps connecting with exponential backoff. While it is unavailable
// Handle.Get returns ErrUnavailable, /healthz reports "degraded" (HTTP 503)
// naming the store and its target (host:port/database, never credentials), and
// Gate answers 503 database_unavailable. Nothing is queued or written locally:
// a refused write is refused. When the server returns the store becomes ready
// again by itself; there is no manual step. A misconfiguration (unknown
// driver, a literal DSN, both dsn_file and dsn_env) still fails Start; an
// unreadable secret is treated as an outage, because it may be provisioned
// after the app starts.
//
// Migrations for a Postgres named store follow schema_mode: default verify, so
// the running app does not migrate a shared server; run migrations from a
// deploy step with the migration role.

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
	// StateConnecting: no successful connection yet and the first attempt is running.
	StateConnecting State = "connecting"
	// StateReady: connected and the last liveness check passed.
	StateReady State = "ready"
	// StateUnavailable: not connected, or the last liveness check failed.
	StateUnavailable State = "unavailable"
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
func UseNamedMigrations(name string, src migrate.Source) {
	namedMu.Lock()
	defer namedMu.Unlock()
	if src.FS == nil {
		delete(namedMigrations, name)
		return
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

// Handle is a named store that can be unavailable. Get it with Named.
type Handle struct {
	name     string
	required bool

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
	ready    chan struct{} // closed the first time the store is ready

	cancel context.CancelFunc
	done   chan struct{}
}

// Name is the store's name.
func (h *Handle) Name() string { return h.name }

// Get returns the store while it is ready, and an error wrapping ErrUnavailable
// otherwise. Callers refuse the write or read with it; nothing is queued.
func (h *Handle) Get() (*store.Store, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.state == StateReady && h.st != nil {
		return h.st, nil
	}
	return nil, fmt.Errorf("%w: store %q", ErrUnavailable, h.name)
}

// Ready reports whether Get would succeed.
func (h *Handle) Ready() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state == StateReady && h.st != nil
}

// Status returns the current status.
func (h *Handle) Status() Status {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Status{Name: h.name, State: h.state, Required: h.required, Target: h.target,
		LastError: h.lastErr, Since: h.since, Attempts: h.attempts}
}

// Wait blocks until the store has been ready at least once, or ctx ends.
func (h *Handle) Wait(ctx context.Context) error {
	select {
	case <-h.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handle) set(state State, st *store.Store, target string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
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
}

// unavailableError is what the health check reports while not ready.
func (h *Handle) unavailableError() error {
	s := h.Status()
	msg := fmt.Sprintf("database unavailable: store %s", s.Name)
	if s.Target != "" {
		msg += " (" + s.Target + ")"
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
	first := h.state == ""
	if first {
		h.state, h.since = StateConnecting, time.Now()
	}
	h.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, h.connTO)
	defer cancel()
	st, target, err := h.connect(cctx)
	if err != nil {
		h.set(StateUnavailable, nil, target, err)
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
// pool reconnects by itself, so a recovered server needs no reopen.
func (h *Handle) loop(ctx context.Context) {
	defer close(h.done)
	delay := h.retry.initial
	for ctx.Err() == nil {
		h.mu.RLock()
		st := h.st
		h.mu.RUnlock()
		if st == nil {
			if err := h.attempt(ctx); err == nil {
				delay = h.retry.initial
				continue
			}
			if !sleep(ctx, jitter(delay)) {
				return
			}
			delay = h.retry.next(delay)
			continue
		}
		if !sleep(ctx, h.pingInt) {
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

// startNamed builds and starts a Handle for every database.stores.<name>.
func (m *mod) startNamed(ctx context.Context, app *module.App) error {
	cfg, _ := app.Config["database"].(map[string]any)
	stores, _ := cfg["stores"].(map[string]any)
	names := make([]string, 0, len(stores))
	for n := range stores {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		raw, ok := stores[name].(map[string]any)
		if !ok {
			return fmt.Errorf("database: stores.%s must be a mapping", name)
		}
		h, err := newHandle(app.Name, name, raw)
		if err != nil {
			return err
		}
		// Validate the shape before touching the network. A secret that cannot
		// be read yet is not a shape error.
		if _, err := storeConfig(app.Name, raw); err != nil && !errors.Is(err, errSecret) {
			return err
		}
		app.Set(NamedKeyPrefix+name, h)
		m.named = append(m.named, h)
		if reg := health.From(app); reg != nil {
			reg.Add("database."+name, func(context.Context) error {
				if h.Ready() {
					return nil
				}
				return h.unavailableError()
			})
		}
		err = h.attempt(ctx)
		if err != nil && h.required {
			return fmt.Errorf("database: store %s: %w", name, err)
		}
		lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		h.cancel = cancel
		go h.loop(lctx)
	}
	return nil
}

func (m *mod) stopNamed(ctx context.Context) error {
	var first error
	for _, h := range m.named {
		if h.cancel != nil {
			h.cancel()
			<-h.done
		}
		h.mu.Lock()
		st := h.st
		h.st, h.state = nil, StateStopped
		h.mu.Unlock()
		if st != nil {
			if err := st.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	m.named = nil
	return first
}

func newHandle(appName, name string, raw map[string]any) (*Handle, error) {
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("database: stores.%s: name must match %s", name, nameRE)
	}
	h := &Handle{name: name, required: true, ready: make(chan struct{}), done: make(chan struct{}),
		connTO: 10 * time.Second, pingInt: 5 * time.Second,
		retry: backoff{initial: time.Second, max: 30 * time.Second}}
	if v, ok := raw["required"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return nil, fmt.Errorf("database: stores.%s.required must be true or false", name)
		}
		h.required = b
	}
	var err error
	if h.connTO, err = durationAt(raw, "connect_timeout", name, h.connTO); err != nil {
		return nil, err
	}
	if h.pingInt, err = durationAt(raw, "ping_interval", name, h.pingInt); err != nil {
		return nil, err
	}
	if r, ok := raw["retry"].(map[string]any); ok {
		if h.retry.initial, err = durationAt(r, "initial", name+".retry", h.retry.initial); err != nil {
			return nil, err
		}
		if h.retry.max, err = durationAt(r, "max", name+".retry", h.retry.max); err != nil {
			return nil, err
		}
		if h.retry.max < h.retry.initial {
			return nil, fmt.Errorf("database: stores.%s.retry.max must be at least retry.initial", name)
		}
	}
	lockTO, err := openLockTimeoutFrom(raw)
	if err != nil {
		return nil, err
	}
	h.connect = func(ctx context.Context) (*store.Store, string, error) {
		sc, err := storeConfig(appName, raw) // re-reads the secret each attempt
		if err != nil {
			return nil, "", err
		}
		target := targetOf(sc)
		st, err := openAndMigrate(ctx, sc, lockTO, namedSource(name))
		return st, target, err
	}
	return h, nil
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

var (
	reKV  = regexp.MustCompile(`(?i)\b(host|port|dbname)=('[^']*'|\S+)`)
	rePwd = regexp.MustCompile(`(?i)\b(password|passwd|pwd)=('[^']*'|\S+)`)
	reURL = regexp.MustCompile(`(?i)(://[^/@\s:]*):[^@\s]*@`)
)

// targetOf is where a store connects, for display: host:port/database for
// Postgres and the file name for SQLite. Never a user name or password.
func targetOf(sc store.Config) string {
	if sc.Driver == store.DriverSQLite {
		return filepath.Base(sc.SQLite.Path)
	}
	dsn := sc.Postgres.DSN
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		return u.Host + "/" + strings.TrimPrefix(u.Path, "/")
	}
	var host, port, db string
	for _, m := range reKV.FindAllStringSubmatch(dsn, -1) {
		v := strings.Trim(m[2], "'")
		switch strings.ToLower(m[1]) {
		case "host":
			host = v
		case "port":
			port = v
		case "dbname":
			db = v
		}
	}
	if host == "" {
		return "postgres"
	}
	if port != "" {
		host += ":" + port
	}
	return host + "/" + db
}

// redactError turns an error into a short string safe to show: credentials in
// a URL or key=value DSN are masked and it is cut to 240 characters.
func redactError(err error) string {
	s := rePwd.ReplaceAllString(err.Error(), "$1=***")
	s = reURL.ReplaceAllString(s, "$1:***@")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "..."
	}
	return s
}
