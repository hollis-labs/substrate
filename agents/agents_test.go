package agents_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/substrate/agents"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	_ "github.com/hollis-labs/substrate/modules/config"
)

func TestResolve(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "config", "app.yaml")
	for _, tc := range []struct {
		name   string
		config map[string]any
		want   string
	}{
		{"default", nil, filepath.Join(root, "config", "agents")},
		{"relative override", map[string]any{"agents": map[string]any{"dir": "../custom"}}, filepath.Join(root, "custom")},
		{"absolute override", map[string]any{"agents": map[string]any{"dir": filepath.Join(root, "absolute")}}, filepath.Join(root, "absolute")},
		{"null default", map[string]any{"agents": map[string]any{"dir": nil}}, filepath.Join(root, "config", "agents")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := agents.Resolve(tc.config, configFile, filepath.Join(root, "state"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Dir() != tc.want || !filepath.IsAbs(got.Dir()) {
				t.Fatalf("Dir = %q, want %q", got.Dir(), tc.want)
			}
		})
	}
}

func TestNamesAndDataDirs(t *testing.T) {
	root := t.TempDir()
	layout, err := agents.Resolve(nil, filepath.Join(root, "app.yaml"), filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"beta", "alpha"} {
		dir, err := layout.AgentDir(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(layout.Dir(), "ignore.txt"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	names, err := layout.Names()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"alpha", "beta"}) {
		t.Fatalf("Names = %v", names)
	}
	for _, name := range names {
		data, err := layout.DataDir(name)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(root, "state", "agents", name)
		if data != want {
			t.Fatalf("DataDir = %q, want %q", data, want)
		}
		if _, err := os.Stat(data); !os.IsNotExist(err) {
			t.Fatalf("DataDir created a path: %v", err)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape", "a/b", `a\b`, "/absolute", "nul\x00name"} {
		if _, err := layout.DataDir(name); err == nil {
			t.Errorf("accepted data name %q", name)
		}
		if _, err := layout.AgentDir(name); err == nil {
			t.Errorf("accepted authored name %q", name)
		}
	}
}

func TestConfigOverrideAtBoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "runtime.yaml")
	if err := os.WriteFile(file, []byte("agents:\n  dir: moved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", file)
	var layout agents.Layout
	app, err := boot.StartWith(context.Background(), "demo", []string{"config"}, `{"agents":{"dir":"original"}}`, boot.Options{AfterConfig: func(_ context.Context, app *module.App) error {
		var err error
		layout, err = agents.Resolve(app.Config, file, filepath.Join(root, "state"))
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if layout.Dir() != filepath.Join(root, "moved") {
		t.Fatalf("override ignored: %s", layout.Dir())
	}
}

func TestInvalidConfigAndMissingDirectory(t *testing.T) {
	for _, config := range []map[string]any{
		{"agents": 42}, {"agents": map[string]any{"dir": 42}}, {"agents": map[string]any{"dir": ""}},
	} {
		if _, err := agents.Resolve(config, "app.yaml", "state"); err == nil {
			t.Errorf("accepted %v", config)
		}
	}
	for _, paths := range [][2]string{{"", "state"}, {"app.yaml", ""}} {
		if _, err := agents.Resolve(nil, paths[0], paths[1]); err == nil {
			t.Errorf("accepted paths %v", paths)
		}
	}
	layout, err := agents.Resolve(nil, filepath.Join(t.TempDir(), "app.yaml"), "state")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layout.Names(); !os.IsNotExist(err) {
		t.Fatalf("missing directory error = %v", err)
	}
}
