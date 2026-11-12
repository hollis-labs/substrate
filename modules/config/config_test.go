package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/module"
)

func run(t *testing.T, base map[string]any, override string) (map[string]any, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "o.yaml")
	if err := os.WriteFile(p, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", p)
	app := &module.App{Config: base}
	err := mod{}.Start(context.Background(), app)
	return app.Config, err
}

func sqliteBase() map[string]any {
	return map[string]any{"database": map[string]any{
		"driver": "sqlite",
		"sqlite": map[string]any{"path": "a.db", "wal": true, "busy_ms": float64(5000)},
	}}
}

func TestNestedMapMergesKeepingSiblings(t *testing.T) {
	got, err := run(t, sqliteBase(), "database:\n  sqlite:\n    path: /other.db\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"database": map[string]any{
		"driver": "sqlite",
		"sqlite": map[string]any{"path": "/other.db", "wal": true, "busy_ms": float64(5000)},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestListReplacesWhole(t *testing.T) {
	base := map[string]any{"m": map[string]any{"hosts": []any{"a", "b", "c"}}}
	got, err := run(t, base, "m:\n  hosts: [x]\n")
	if err != nil {
		t.Fatal(err)
	}
	if h := got["m"].(map[string]any)["hosts"]; !reflect.DeepEqual(h, []any{"x"}) {
		t.Fatalf("hosts = %#v", h)
	}
}

func TestScalarReplaces(t *testing.T) {
	got, err := run(t, sqliteBase(), "database:\n  driver: postgres\n")
	if err != nil {
		t.Fatal(err)
	}
	db := got["database"].(map[string]any)
	if db["driver"] != "postgres" || db["sqlite"].(map[string]any)["path"] != "a.db" {
		t.Fatalf("db = %#v", db)
	}
}

func TestKeyOnlyInOverrideAdded(t *testing.T) {
	got, err := run(t, sqliteBase(), "database:\n  sqlite:\n    extra: 1\nother:\n  k: v\n")
	if err != nil {
		t.Fatal(err)
	}
	sq := got["database"].(map[string]any)["sqlite"].(map[string]any)
	if sq["extra"] != 1 || sq["path"] != "a.db" {
		t.Fatalf("sqlite = %#v", sq)
	}
	if got["other"].(map[string]any)["k"] != "v" {
		t.Fatalf("new module block missing: %#v", got["other"])
	}
}

func TestNullRemovesKey(t *testing.T) {
	got, err := run(t, sqliteBase(), "database:\n  sqlite:\n    wal: null\n")
	if err != nil {
		t.Fatal(err)
	}
	sq := got["database"].(map[string]any)["sqlite"].(map[string]any)
	if _, ok := sq["wal"]; ok {
		t.Fatalf("wal still present: %#v", sq)
	}
	if sq["path"] != "a.db" {
		t.Fatalf("sibling lost: %#v", sq)
	}
}

func TestNullRemovesWholeSubtree(t *testing.T) {
	got, err := run(t, sqliteBase(), "database:\n  sqlite: null\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["database"].(map[string]any)["sqlite"]; ok {
		t.Fatalf("sqlite block still present: %#v", got)
	}
}

func TestNullForAbsentKeyIsNoop(t *testing.T) {
	got, err := run(t, sqliteBase(), "database:\n  nothere: null\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sqliteBase()) {
		t.Fatalf("changed: %#v", got)
	}
}

func TestNullModuleBlockIsNoop(t *testing.T) {
	got, err := run(t, sqliteBase(), "database: null\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sqliteBase()) {
		t.Fatalf("changed: %#v", got)
	}
}

func TestTypeClash(t *testing.T) {
	base := map[string]any{"m": map[string]any{"a": map[string]any{"x": 1}, "b": "scalar", "c": []any{1}}}
	got, err := run(t, base, "m:\n  a: flat\n  b:\n    k: v\n    z: null\n  c:\n    k: v\n")
	if err != nil {
		t.Fatal(err)
	}
	m := got["m"].(map[string]any)
	if m["a"] != "flat" {
		t.Fatalf("scalar over map: %#v", m["a"])
	}
	if !reflect.DeepEqual(m["b"], map[string]any{"k": "v"}) {
		t.Fatalf("map over scalar: %#v", m["b"])
	}
	if !reflect.DeepEqual(m["c"], map[string]any{"k": "v"}) {
		t.Fatalf("map over list: %#v", m["c"])
	}
}

func TestEmptyMapOverrideLeavesBase(t *testing.T) {
	got, err := run(t, sqliteBase(), "database:\n  sqlite: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sqliteBase()) {
		t.Fatalf("changed: %#v", got)
	}
}

func TestMergeDoesNotMutateBase(t *testing.T) {
	base := sqliteBase()
	block := base["database"].(map[string]any) // the manifest's block, held before the merge
	if _, err := run(t, base, "database:\n  sqlite:\n    path: /x\n    wal: null\n"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(block, sqliteBase()["database"]) {
		t.Fatalf("manifest block mutated: %#v", block)
	}
}

func TestNoEnvLeavesConfig(t *testing.T) {
	t.Setenv("SUBSTRATE_CONFIG", "")
	app := &module.App{Config: sqliteBase()}
	if err := (mod{}).Start(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(app.Config, sqliteBase()) {
		t.Fatalf("changed: %#v", app.Config)
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"module block scalar": "database: 3\n",
		"module block list":   "database: [a]\n",
		"bad yaml":            "database: [\n",
		"top-level list":      "- a\n",
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := run(t, sqliteBase(), y)
			if err == nil {
				t.Fatalf("no error, config %#v", got)
			}
		})
	}
	_, err := run(t, sqliteBase(), "database: 3\n")
	if err == nil || !strings.Contains(err.Error(), `"database"`) {
		t.Fatalf("error should name the module: %v", err)
	}
	t.Setenv("SUBSTRATE_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	if err := (mod{}).Start(context.Background(), &module.App{Config: map[string]any{}}); err == nil {
		t.Fatal("missing file: no error")
	}
}

func TestString(t *testing.T) {
	app := &module.App{Config: map[string]any{"m": map[string]any{"k": "v", "n": 1}}}
	if String(app, "m", "k", "d") != "v" || String(app, "m", "n", "d") != "d" || String(app, "x", "k", "d") != "d" {
		t.Fatal("String lookup wrong")
	}
}
