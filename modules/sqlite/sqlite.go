// Package sqlite is the data module: libs core/store (single-writer SQLite,
// goose migrations) opened from config, with a health check. The app owns the
// data, the schema and its migrations; this module only opens and verifies.
//
// Config (manifest or SUBSTRATE_CONFIG), under "sqlite":
//
//	path:        database file, ":memory:" for tests. Default "<app>.db".
//	synchronous: OFF|NORMAL|FULL|EXTRA (default per core/store).
//	schema_mode: "auto" (apply migrations, then verify) or "verify".
package sqlite

import (
	"context"
	"fmt"
	"sync"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/health"
)

// Key under which the opened *store.Store is published.
const Key = "sqlite.store"

var (
	mu         sync.Mutex
	migrations *migrate.Source
)

// UseMigrations registers the app's migration source. Call it before
// boot.Start; with none registered the module opens the store and skips schema
// management.
func UseMigrations(src migrate.Source) {
	mu.Lock()
	defer mu.Unlock()
	migrations = &src
}

func init() { module.Register(&mod{}) }

type mod struct{ st *store.Store }

func (*mod) Name() string { return "sqlite" }

func (m *mod) Start(ctx context.Context, app *module.App) error {
	cfg, _ := app.Config["sqlite"].(map[string]any)
	str := func(k, def string) string {
		if s, ok := cfg[k].(string); ok && s != "" {
			return s
		}
		return def
	}
	sc := store.Config{
		Driver:     store.DriverSQLite,
		SchemaMode: store.SchemaMode(str("schema_mode", string(store.SchemaAuto))),
		SQLite: store.SQLiteConfig{
			Path:            str("path", app.Name+".db"),
			Synchronous:     str("synchronous", ""),
			CreateParentDir: true,
		},
	}
	st, err := store.Open(ctx, sc)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	mu.Lock()
	src := migrations
	mu.Unlock()
	if src != nil {
		if err := migrate.Ensure(ctx, sc, st, *src); err != nil {
			_ = st.Close()
			return fmt.Errorf("migrate: %w", err)
		}
	}
	m.st = st
	app.Set(Key, st)
	if reg := health.From(app); reg != nil {
		reg.Add("sqlite", st.Ping)
	}
	return nil
}

func (m *mod) Stop(context.Context) error {
	if m.st == nil {
		return nil
	}
	err := m.st.Close()
	m.st = nil
	return err
}

// From returns the store published by the sqlite module, or nil.
func From(app *module.App) *store.Store {
	v, _ := app.Value(Key)
	s, _ := v.(*store.Store)
	return s
}
