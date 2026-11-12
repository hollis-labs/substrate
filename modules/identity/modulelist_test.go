package identity_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/cli"
	_ "github.com/hollis-labs/substrate/modules/config"
	_ "github.com/hollis-labs/substrate/modules/health"
	_ "github.com/hollis-labs/substrate/modules/logging"
	_ "github.com/hollis-labs/substrate/modules/secrets"
)

// offlineModules is the list `identity setup` and `identity key create` need, as
// docs/adopting-substrate.md states it: the app's normal boot minus what only a
// server uses (httphost, mcp, queue and the rest). Order is the manifest's.
var offlineModules = []string{"config", "logging", "health", "auth", "database", "identity", "cli"}

// mustHave are the modules the two commands cannot do without. config, logging
// and health are part of the list because the commands run inside the app's
// normal boot (the SUBSTRATE_CONFIG merge, the log line format, the health
// checks `<app> health` runs), not because setup or key create call them.
var mustHave = []string{"auth", "database", "identity", "cli"}

// bootAndRun boots exactly mods and runs setup and then key create. It returns
// the error of whichever step failed, so a dropped module cannot pass quietly.
func bootAndRun(t *testing.T, mods []string, cfg string, opts boot.Options) error {
	t.Helper()
	run, err := boot.StartWith(context.Background(), "demo", mods, cfg, opts)
	if err != nil {
		return fmt.Errorf("boot: %w", err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	root := cli.Root(run.App)
	if root == nil {
		return errors.New("no cli root")
	}
	exec := func(stdin string, args ...string) error {
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetIn(strings.NewReader(stdin))
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
	if err := exec(secretPassword+"\n", setupArgs()...); err != nil {
		return err
	}
	return exec("", "identity", "key", "create", "--name", "k", "--scope", "demo.read", "--stdout")
}

func listConfig(dir string) string {
	return fmt.Sprintf(`{"database":{"sqlite":{"path":%q}},"auth":{"mode":"enforce"},"identity":{"key_file":%q,"bcrypt_cost":4}}`,
		filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"))
}

func without(mods []string, drop string) []string {
	return slices.DeleteFunc(slices.Clone(mods), func(m string) bool { return m == drop })
}

// TestOfflineCommandsStartOnlyTheDocumentedModules starts only offlineModules and
// runs both commands, then drops each required module in turn: every drop must
// fail. If this test and docs/adopting-substrate.md (section 5, "Modules the
// offline commands need") disagree, fix the doc.
func TestOfflineCommandsStartOnlyTheDocumentedModules(t *testing.T) {
	t.Setenv("SUBSTRATE_CONFIG", "")
	dir := t.TempDir()
	if err := bootAndRun(t, offlineModules, listConfig(dir), boot.Options{}); err != nil {
		t.Fatalf("the documented list does not run the commands: %v", err)
	}
	// The commands created the database file and the signing key.
	for _, f := range []string{"t.db", "id.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s was not created: %v", f, err)
		}
	}
	for _, drop := range mustHave {
		t.Run("without "+drop, func(t *testing.T) {
			if err := bootAndRun(t, without(offlineModules, drop), listConfig(t.TempDir()), boot.Options{}); err == nil {
				t.Fatalf("the commands ran without %s", drop)
			}
		})
	}
}

// TestOfflineListWithoutConfigIgnoresTheOverride: dropping config does not stop the
// commands, it makes them disagree with the server. SUBSTRATE_CONFIG moves the
// database; without the config module the command writes the manifest's path.
func TestOfflineListWithoutConfigIgnoresTheOverride(t *testing.T) {
	manifestDir, overrideDir := t.TempDir(), t.TempDir()
	override := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(override, []byte("database:\n  sqlite:\n    path: "+filepath.Join(overrideDir, "t.db")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", override)
	for _, tc := range []struct {
		name       string
		mods       []string
		wantInside string // where the database must appear
	}{
		{"with config", offlineModules, overrideDir},
		{"without config", without(offlineModules, "config"), manifestDir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(filepath.Join(manifestDir, "t.db"))
			_ = os.Remove(filepath.Join(overrideDir, "t.db"))
			_ = os.Remove(filepath.Join(manifestDir, "id.key"))
			if err := bootAndRun(t, tc.mods, listConfig(manifestDir), boot.Options{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(tc.wantInside, "t.db")); err != nil {
				t.Fatalf("the database is not under %s: %v", tc.wantInside, err)
			}
			other := overrideDir
			if tc.wantInside == overrideDir {
				other = manifestDir
			}
			if _, err := os.Stat(filepath.Join(other, "t.db")); err == nil {
				t.Fatalf("a database also appeared under %s", other)
			}
		})
	}
}

// TestSecretsMustBeStartedWhenAHookPlacesItsDirectory: an app whose boot hook is
// keyed to "secrets" fails to boot when the list omits it, so the commands need
// secrets exactly when the app's hooks name it.
func TestSecretsMustBeStartedWhenAHookPlacesItsDirectory(t *testing.T) {
	t.Setenv("SUBSTRATE_CONFIG", "")
	var placed bool
	hook := boot.Options{Before: map[string]module.Hook{"secrets": func(context.Context, *module.App) error { placed = true; return nil }}}
	if err := bootAndRun(t, offlineModules, listConfig(t.TempDir()), hook); err == nil {
		t.Fatal("a Before hook for a module that is not in the list was accepted")
	}
	withSecrets := slices.Insert(slices.Clone(offlineModules), 1, "secrets")
	if err := bootAndRun(t, withSecrets, listConfig(t.TempDir()), hook); err != nil || !placed {
		t.Fatalf("with secrets: err=%v hook ran=%v", err, placed)
	}
}
