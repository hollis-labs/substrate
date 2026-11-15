package database_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
)

func TestSQLiteForeignKeysConfig(t *testing.T) {
	useMigrations(t)
	ctx := context.Background()
	for _, c := range []struct {
		name, cfg string
		want      int
	}{
		{"unset keeps them on", ``, 1},
		{"null keeps them on", `,"foreign_keys":null`, 1},
		{"true", `,"foreign_keys":true`, 1},
		{"false", `,"foreign_keys":false`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := `{"database":{"sqlite":{"path":"` + filepath.Join(t.TempDir(), "fk.db") + `"` + c.cfg + `}}}`
			run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = run.Stop(ctx) }()
			st := database.From(run.App)
			var w, r int
			if err := st.DB().QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&w); err != nil || w != c.want {
				t.Fatalf("writer foreign_keys = %d (%v), want %d", w, err, c.want)
			}
			if err := st.Reader().QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&r); err != nil || r != c.want {
				t.Fatalf("reader foreign_keys = %d (%v), want %d", r, err, c.want)
			}
			for _, q := range []string{
				`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
				`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id))`,
			} {
				if _, err := st.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			_, err = st.ExecContext(ctx, `INSERT INTO child (id, parent_id) VALUES (1, 42)`)
			if accepted := err == nil; accepted != (c.want == 0) {
				t.Fatalf("orphan insert: err=%v, want accepted=%v", err, c.want == 0)
			}
		})
	}
}

func TestSQLiteForeignKeysMustBeABool(t *testing.T) {
	useMigrations(t)
	for _, v := range []string{`"false"`, `0`, `"off"`} {
		cfg := `{"database":{"sqlite":{"path":":memory:","foreign_keys":` + v + `}}}`
		_, err := boot.Start(context.Background(), "t", []string{"health", "database"}, cfg)
		if err == nil || !strings.Contains(err.Error(), "sqlite.foreign_keys must be true or false") {
			t.Errorf("foreign_keys %s: %v", v, err)
		}
	}
}
