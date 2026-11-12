package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
)

// openCursors holds n read cursors open at once on the store's read pool and
// reports whether one more read is admitted (true) or has to wait (false).
func readsAdmitted(t *testing.T, readPoolCfg string) int {
	t.Helper()
	useMigrations(t)
	ctx := context.Background()
	cfg := `{"database":{"sqlite":{"path":"` + filepath.Join(t.TempDir(), "a.db") + `"` + readPoolCfg + `}}}`
	run, err := boot.Start(ctx, "t", []string{"health", "database"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	st := database.From(run.App)
	for i := 0; i < 3; i++ {
		if _, err := st.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('x')`); err != nil {
			t.Fatal(err)
		}
	}
	var held []*sql.Rows
	var cancels []context.CancelFunc
	defer func() {
		for _, r := range held {
			_ = r.Close()
		}
		for _, c := range cancels {
			c()
		}
	}()
	type res struct {
		rows *sql.Rows
		err  error
	}
	for n := 0; n < 100; n++ {
		// The query context outlives the call: cancelling it would close the cursor.
		c, cancel := context.WithCancel(ctx)
		cancels = append(cancels, cancel)
		ch := make(chan res, 1)
		go func() {
			rows, err := st.Reader().QueryContext(c, `SELECT id FROM notes`)
			ch <- res{rows, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatal(r.err)
			}
			_ = r.rows.Next() // cursor stays open and keeps its connection
			held = append(held, r.rows)
		case <-time.After(300 * time.Millisecond):
			return n // the (n+1)th read is waiting for a connection
		}
	}
	t.Fatal("read pool never filled")
	return -1
}

func TestReadPoolSize(t *testing.T) {
	for _, c := range []struct {
		name, cfg string
		want      int
	}{
		{"default unchanged", ``, 4},
		{"one", `,"read_pool":1`, 1},
		{"two", `,"read_pool":2`, 2},
		{"seven", `,"read_pool":7`, 7},
	} {
		if got := readsAdmitted(t, c.cfg); got != c.want {
			t.Errorf("%s: %d concurrent reads admitted, want %d", c.name, got, c.want)
		}
	}
}

func TestReadPoolValidated(t *testing.T) {
	useMigrations(t)
	for _, v := range []string{`0`, `-1`, `65`, `1000000`, `2.5`, `"4"`, `true`} {
		_, err := boot.Start(context.Background(), "t", []string{"health", "database"},
			`{"database":{"sqlite":{"path":"`+filepath.Join(t.TempDir(), "a.db")+`","read_pool":`+v+`}}}`)
		if err == nil || !strings.Contains(err.Error(), "read_pool") {
			t.Errorf("read_pool %s: err = %v, want a read_pool error", v, err)
		}
	}
	for _, v := range []int{1, database.MaxReadPool} {
		run, err := boot.Start(context.Background(), "t", []string{"health", "database"},
			`{"database":{"sqlite":{"path":"`+filepath.Join(t.TempDir(), "a.db")+`","read_pool":`+strconv.Itoa(v)+`}}}`)
		if err != nil {
			t.Errorf("read_pool %d refused: %v", v, err)
			continue
		}
		_ = run.Stop(context.Background())
	}
}
