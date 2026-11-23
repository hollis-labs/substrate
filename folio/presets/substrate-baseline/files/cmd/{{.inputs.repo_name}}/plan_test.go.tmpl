package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// Only serve and the offline identity commands start the full module set; everything that only
// prints starts no module that opens a file, so it creates no directory.
func TestPlan(t *testing.T) {
	full := []struct {
		args []string
	}{
		{[]string{"serve"}},
		{[]string{"serve", "--managed", "--addr", "127.0.0.1:8089"}},
		{[]string{"identity", "setup", "--username", "owner"}},
		{[]string{"identity", "key", "create", "--name", "x"}},
	}
	for _, c := range full {
		if p := plan(c.args); !p.prepare || !slices.Equal(p.modules, substrateModules) {
			t.Errorf("plan(%v) = %+v, want the full set with prepare", c.args, p)
		}
	}
	for _, args := range [][]string{nil, {"--help"}, {"-h"}, {"help"}, {"--version"}, {"version"}, {"config"}, {"health"}, {"nope"}, {"--bad-flag"}, {"identity"}, {"identity", "key"}, {"serve", "--help"}, {"serve", "--no-such-flag"}, {"serve", "extra"}} {
		p := plan(args)
		if p.prepare {
			t.Errorf("plan(%v) prepares the data directory", args)
		}
		for _, m := range p.modules {
			if !slices.Contains(printModules, m) {
				t.Errorf("plan(%v) starts %q", args, m)
			}
		}
	}
}

// plan counts every argument that does not start with "-" as a command word, which is right only
// while the root command has no global flag that takes a value.
func TestRootHasNoValueFlags(t *testing.T) {
	started, err := start(t.Context(), plan(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = started.Stop(t.Context()) }()
	root := newRoot(t.Context(), started.App)
	check := func(f *pflag.Flag) {
		if f.Value.Type() != "bool" && f.NoOptDefVal == "" {
			t.Errorf("global flag --%s takes a value: plan() would read its argument as a command word; teach plan() first", f.Name)
		}
	}
	root.PersistentFlags().VisitAll(check)
	root.Flags().VisitAll(check)
}

func TestXDGConfigCannotDisableClass1Auth(t *testing.T) {
	t.Setenv("SUBSTRATE_CONFIG", "")
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(dataDirEnv, t.TempDir())
	dir := filepath.Join(configHome, substrateApp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("auth: {mode: off}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, err := useConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	started, err := start(t.Context(), plan([]string{"serve", "--managed"}))
	if err == nil {
		_ = started.Stop(t.Context())
		t.Fatal("class1 auth downgrade accepted")
	}
	if !strings.Contains(err.Error(), "require auth.mode enforce") {
		t.Fatalf("unexpected refusal: %v", err)
	}
}
