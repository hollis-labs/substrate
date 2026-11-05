// Package conformance checks every catalogue module and profile against the
// module contract, so a new module or profile is held to the same properties.
package conformance

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/hollis-labs/substrate/catalog"
	"github.com/hollis-labs/substrate/manifest"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/policy"

	_ "github.com/hollis-labs/substrate/modules/auth"
	_ "github.com/hollis-labs/substrate/modules/capabilities"
	_ "github.com/hollis-labs/substrate/modules/cli"
	_ "github.com/hollis-labs/substrate/modules/config"
	_ "github.com/hollis-labs/substrate/modules/database"
	_ "github.com/hollis-labs/substrate/modules/events"
	_ "github.com/hollis-labs/substrate/modules/health"
	_ "github.com/hollis-labs/substrate/modules/httphost"
	_ "github.com/hollis-labs/substrate/modules/logging"
	_ "github.com/hollis-labs/substrate/modules/mcp"
	_ "github.com/hollis-labs/substrate/modules/pluginhost"
	_ "github.com/hollis-labs/substrate/modules/secrets"
)

func newApp(t *testing.T) *module.App {
	t.Helper()
	return &module.App{
		Name:   "conformance",
		Values: map[string]any{},
		Config: map[string]any{
			// Modules that touch disk get a private location.
			"database": map[string]any{"sqlite": map[string]any{"path": filepath.Join(t.TempDir(), "c.db")}},
		},
	}
}

// startStop starts names, checks the properties every selection must hold, and
// stops them. It returns the app as it was while running.
func startStop(t *testing.T, names []string) {
	t.Helper()
	policy.UsePDP(policy.AllowAll{}) // policy refuses to start without an app-supplied PDP
	app := newApp(t)
	var rt module.Runtime
	ctx := context.Background()
	if err := rt.Start(ctx, app, names); err != nil {
		t.Fatalf("start %v: %v", names, err)
	}
	if len(app.Started) != len(names) {
		t.Errorf("Started = %v, want %v", app.Started, names)
	}
	for i := range names {
		if app.Started[i] != names[i] {
			t.Errorf("Started = %v, want %v", app.Started, names)
			break
		}
	}
	if err := rt.Stop(ctx); err != nil {
		t.Errorf("stop: %v", err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Errorf("second Stop must be a no-op, got: %v", err)
	}
}

func resolve(t *testing.T, m manifest.Manifest) []string {
	t.Helper()
	names, err := m.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return names
}

func TestCatalogIsConsistent(t *testing.T) {
	var cat []string
	for _, m := range catalog.Modules {
		cat = append(cat, m.Name)
		for _, r := range m.Requires {
			if _, ok := catalog.Find(r); !ok {
				t.Errorf("%s requires %q, which is not in the catalogue", m.Name, r)
			}
		}
		if want := "github.com/hollis-labs/substrate/modules/" + m.Name; m.Import != want {
			t.Errorf("%s imports %q, want %q", m.Name, m.Import, want)
		}
	}
	sort.Strings(cat)

	if got := module.Linked(); !equal(got, cat) {
		t.Errorf("linked modules %v != catalogue %v (every catalogue module must register its own name from init)", got, cat)
	}

	dirs, err := os.ReadDir("../modules")
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, d := range dirs {
		if d.IsDir() {
			onDisk = append(onDisk, d.Name())
		}
	}
	if !equal(onDisk, cat) {
		t.Errorf("modules/ directories %v != catalogue %v", onDisk, cat)
	}

	for p, ms := range catalog.Profiles {
		for _, n := range ms {
			if _, ok := catalog.Find(n); !ok {
				t.Errorf("profile %s names %q, which is not in the catalogue", p, n)
			}
		}
	}
	for v, need := range catalog.Variants {
		for _, n := range need.Needs {
			if _, ok := catalog.Find(n); !ok {
				t.Errorf("variant %s needs %q, which is not in the catalogue", v, n)
			}
		}
	}
}

// Each module, added to the minimal profile with what it requires, starts and stops cleanly, and
// can be started again afterwards.
func TestEveryModule(t *testing.T) {
	for _, cm := range catalog.Modules {
		if cm.Status != "ready" {
			continue
		}
		t.Run(cm.Name, func(t *testing.T) {
			names := resolve(t, manifest.Manifest{App: "conformance", Variant: "service", Profiles: []string{"minimal"}, Modules: manifest.Modules{Add: []string{cm.Name}}})
			pos := map[string]int{}
			for i, n := range names {
				pos[n] = i
			}
			for _, r := range cm.Requires {
				if i, ok := pos[r]; !ok || i > pos[cm.Name] {
					t.Errorf("requirement %s is not ordered before %s in %v", r, cm.Name, names)
				}
			}
			startStop(t, names)
			startStop(t, names) // restart: Stop released everything Start acquired
		})
	}
}

// Each profile starts and stops under every variant it is valid for.
func TestEveryProfile(t *testing.T) {
	for p := range catalog.Profiles {
		for v := range catalog.Variants {
			t.Run(p+"/"+v, func(t *testing.T) {
				m := manifest.Manifest{App: "conformance", Variant: v, Profiles: []string{p}}
				if errs := m.Validate(); len(errs) > 0 {
					t.Skipf("not a valid combination: %v", errs[0])
				}
				names := resolve(t, m)
				for _, want := range catalog.Profiles[p] {
					if !contains(names, want) {
						t.Errorf("profile %s did not resolve %s: %v", p, want, names)
					}
				}
				startStop(t, names)
			})
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
