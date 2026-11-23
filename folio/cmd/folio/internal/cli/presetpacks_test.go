package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/folio/cmd/folio/internal/cli"
)

func TestPresetPackFlagSelectsCatalogAndRejectsDuplicates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pack := t.TempDir()
	preset := filepath.Join(pack, "presets", "external-probe")
	if err := os.MkdirAll(preset, 0o755); err != nil {
		t.Fatal(err)
	}
	data := "folio_version: \"0.1\"\nid: external-probe\nversion: 0.1.0\ndescription: External probe\nauthor: tester\nfiles:\n  source: ./files\n"
	if err := os.WriteFile(filepath.Join(preset, "preset.yaml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	root := cli.NewRootCmd(loadBundledFS(t), "test")
	root.SetArgs([]string{"preset", "list", "--preset-pack", pack})
	var out bytes.Buffer
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "external-probe") || !strings.Contains(out.String(), "go-lib") {
		t.Fatalf("expected both external and bundled presets: %s", out.String())
	}
	// Repeating a pack must fail at command startup, before generation.
	root = cli.NewRootCmd(loadBundledFS(t), "test")
	target := filepath.Join(t.TempDir(), "must-not-exist")
	root.SetArgs([]string{"new", "external-probe", target, "--non-interactive", "--preset-pack", pack, "--preset-pack", pack})
	root.SilenceUsage = true
	root.SilenceErrors = true
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "duplicate preset") {
		t.Fatalf("expected duplicate error: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("failed pack selection wrote target")
	}
}
