// Package database is the data module: libs core/store opened from config on
// SQLite or Postgres, goose migrations, and a health check. The app owns the
// data, the schema and its migrations (one squashed schema per app); this
// module opens the store, applies or verifies the schema, and nothing else.
//
// Config (manifest or SUBSTRATE_CONFIG), under "database":
//
//	driver:      "sqlite" (default) or "postgres".
//	schema_mode: "auto" (apply migrations, then verify) or "verify". Default auto
//	             for SQLite and verify for Postgres: a shared server migrates deliberately.
//
//	sqlite:      path (file, ":memory:" for tests; default "<app>.db"),
//	             synchronous (OFF|NORMAL|FULL|EXTRA), journal ("wal"|"delete"),
//	             read_pool (size of the read-only pool behind Store.Reader(), an
//	             integer 1 to 64; unset keeps the libs default, 4. The write side
//	             is always one connection. Ignored for ":memory:", which has one
//	             connection for everything).
//	             open_lock_timeout (Go duration, default 30s; see below).
//	postgres:    dsn_file or dsn_env (the connection string is a secret, so it is
//	             read from a file or an environment variable, never from config),
//	             migrate_dsn_file or migrate_dsn_env (schema-owner role, used only
//	             to migrate), allow_insecure (tests and local containers),
//	             max_conns, application_name (default the app name).
//
// Concurrent starts. On SQLite a file database is opened and migrated while
// holding an exclusive advisory lock (flock) on "<path>.open.lock", so two
// processes starting at once on one new file take turns: the second waits for
// the first to finish migrating, then opens and finds the schema current. The
// kernel releases the lock when its holder exits, so a crash leaves nothing
// stale (the lock file stays on disk and its existence means nothing). Waiting
// longer than open_lock_timeout fails the start with an error naming the lock
// file. Not taken for ":memory:" or Postgres. Where flock is unavailable
// (non-unix builds) the lock is a no-op.
package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/health"
)

// Key under which the opened *store.Store is published.
const Key = "database.store"

var (
	mu         sync.Mutex
	migrations *migrate.Source
)

// UseMigrations registers the app's migration source (see UseMigrationFS). Call it before
// boot.Start; with none registered the module opens the store and skips schema
// management.
func UseMigrations(src migrate.Source) {
	mu.Lock()
	defer mu.Unlock()
	migrations = &src
}

// UseMigrationFS registers migrations from an embedded file system whose Root
// holds one directory per dialect (root/sqlite, root/postgres). It is
// UseMigrations without making the app import libs for migrate.Source.
func UseMigrationFS(fsys fs.FS, root string) {
	UseMigrations(migrate.Source{FS: fsys, Root: root})
}

func init() { module.Register(&mod{}) }

type mod struct {
	st    *store.Store
	named []*Handle
}

func (*mod) Name() string { return "database" }

func (m *mod) Start(ctx context.Context, app *module.App) error {
	sc, err := configFrom(app)
	if err != nil {
		return err
	}
	lockTimeout, err := openLockTimeout(app)
	if err != nil {
		return err
	}
	mu.Lock()
	src := migrations
	mu.Unlock()
	st, err := openAndMigrate(ctx, sc, lockTimeout, src)
	if err != nil {
		return err
	}
	m.st = st
	app.Set(Key, st)
	if reg := health.From(app); reg != nil {
		reg.Add("database", st.Ping)
	}
	defaultPath := ""
	if sc.Driver == store.DriverSQLite && !isMemory(sc.SQLite.Path) {
		defaultPath = sc.SQLite.Path
	}
	if err := m.startNamed(ctx, app, defaultPath); err != nil {
		_ = m.Stop(ctx)
		return err
	}
	return nil
}

// openAndMigrate opens the store (taking the SQLite open lock for a file
// database) and applies or verifies the schema when a migration source is
// registered. It is the one open path for the default store and every named
// store.
func openAndMigrate(ctx context.Context, sc store.Config, lockTimeout time.Duration, src *migrate.Source) (*store.Store, error) {
	if sc.Driver == store.DriverSQLite && !isMemory(sc.SQLite.Path) {
		release, err := acquireOpenLock(ctx, sc.SQLite.Path, lockTimeout)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	st, err := store.Open(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if src != nil && src.FS != nil { // an empty Source (a cleared registration) means no migrations
		if err := migrate.Ensure(ctx, sc, st, *src); err != nil {
			_ = st.Close()
			return nil, &migrateError{err}
		}
	}
	return st, nil
}

// configFrom builds the default store's config from app config.
func configFrom(app *module.App) (store.Config, error) {
	cfg, _ := app.Config["database"].(map[string]any)
	return storeConfig(app.Name, cfg)
}

// storeConfig builds a store config from one store's config block (the
// "database" block for the default store, "database.stores.<name>" for a named
// one). Secrets are read here, so calling it again re-reads a rotated secret.
func storeConfig(appName string, cfg map[string]any) (store.Config, error) {
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	sub := func(k string) map[string]any { m, _ := cfg[k].(map[string]any); return m }

	driver := store.Driver(str(cfg, "driver"))
	if driver == "" {
		driver = store.DriverSQLite
	}
	sc := store.Config{Driver: driver, SchemaMode: store.SchemaMode(str(cfg, "schema_mode"))}
	for _, k := range []string{"dsn", "migrate_dsn"} {
		if p := sub("postgres"); p != nil {
			if _, literal := p[k]; literal {
				return sc, fmt.Errorf("database: postgres.%s is a secret and cannot be written in config; use %s_file or %s_env", k, k, k)
			}
		}
	}
	switch driver {
	case store.DriverSQLite:
		s := sub("sqlite")
		if s == nil {
			s = map[string]any{}
		}
		readPool, err := readPoolFrom(s)
		if err != nil {
			return sc, err
		}
		sc.SQLite = store.SQLiteConfig{
			Path:            orDefault(str(s, "path"), appName+".db"),
			Synchronous:     str(s, "synchronous"),
			Journal:         str(s, "journal"),
			ReadConns:       readPool,
			CreateParentDir: true,
		}
	case store.DriverPostgres:
		p := sub("postgres")
		dsn, err := secretFrom(p, "dsn")
		if err != nil {
			return sc, err
		}
		mig, err := secretFrom(p, "migrate_dsn")
		if err != nil && !errors.Is(err, errUnset) {
			return sc, err
		}
		pc := store.PostgresConfig{DSN: dsn, MigrateDSN: mig, ApplicationName: orDefault(str(p, "application_name"), appName)}
		pc.AllowInsecure, _ = p["allow_insecure"].(bool)
		if n, ok := p["max_conns"].(float64); ok {
			pc.MaxConns = int(n)
		}
		sc.Postgres = pc
	default:
		return sc, fmt.Errorf("database: driver %q must be sqlite or postgres", driver)
	}
	return sc, nil
}

// MaxReadPool is the largest accepted database.sqlite.read_pool.
const MaxReadPool = 64

// readPoolFrom reads sqlite.read_pool: absent (or null) is 0, which keeps the
// libs default; otherwise a whole number from 1 to MaxReadPool.
func readPoolFrom(s map[string]any) (int, error) {
	v, ok := s["read_pool"]
	if !ok || v == nil {
		return 0, nil
	}
	var n int
	switch x := v.(type) {
	case float64:
		n = int(x)
		if float64(n) != x {
			return 0, fmt.Errorf("database: sqlite.read_pool %v must be a whole number from 1 to %d", v, MaxReadPool)
		}
	default:
		return 0, fmt.Errorf("database: sqlite.read_pool must be a whole number from 1 to %d", MaxReadPool)
	}
	if n < 1 || n > MaxReadPool {
		return 0, fmt.Errorf("database: sqlite.read_pool %d must be from 1 to %d", n, MaxReadPool)
	}
	return n, nil
}

var errUnset = errors.New("not set")

// errSecret marks a secret that could not be read (file missing, variable
// empty). A named store that is allowed to start unavailable retries these,
// because the secret may be provisioned after the app starts; every other
// configuration error fails the start.
var errSecret = errors.New("secret unavailable")

// secretFrom reads <name>_file or <name>_env from m. The value is never put in
// an error.
func secretFrom(m map[string]any, name string) (string, error) {
	file, _ := m[name+"_file"].(string)
	env, _ := m[name+"_env"].(string)
	switch {
	case file != "" && env != "":
		return "", fmt.Errorf("database: postgres.%s_file and %s_env are both set; choose one", name, name)
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("database: cannot read postgres.%s_file: %w", name, errSecret)
		}
		return strings.TrimSpace(string(b)), nil
	case env != "":
		v := os.Getenv(env)
		if v == "" {
			return "", fmt.Errorf("database: environment variable %s (postgres.%s_env) is empty: %w", env, name, errSecret)
		}
		return v, nil
	}
	if name == "dsn" {
		return "", fmt.Errorf("database: postgres needs dsn_file or dsn_env")
	}
	return "", errUnset
}

const defaultOpenLockTimeout = 30 * time.Second

// openLockTimeout reads database.sqlite.open_lock_timeout (a Go duration).
func openLockTimeout(app *module.App) (time.Duration, error) {
	cfg, _ := app.Config["database"].(map[string]any)
	return openLockTimeoutFrom(cfg)
}

func openLockTimeoutFrom(cfg map[string]any) (time.Duration, error) {
	s, _ := cfg["sqlite"].(map[string]any)
	v, ok := s["open_lock_timeout"].(string)
	if !ok || v == "" {
		return defaultOpenLockTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("database: sqlite.open_lock_timeout %q must be a positive duration such as 30s", v)
	}
	return d, nil
}

// isMemory reports the SQLite in-memory spellings, which have no file to lock.
func isMemory(p string) bool {
	return p == ":memory:" || strings.HasPrefix(p, "file::memory:")
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

func (m *mod) Stop(ctx context.Context) error {
	err := m.stopNamed(ctx)
	if m.st == nil {
		return err
	}
	if cerr := m.st.Close(); err == nil {
		err = cerr
	}
	m.st = nil
	return err
}

// From returns the store published by the database module, or nil.
func From(app *module.App) *store.Store {
	v, _ := app.Value(Key)
	s, _ := v.(*store.Store)
	return s
}
