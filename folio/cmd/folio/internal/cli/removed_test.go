package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/folio/cmd/folio/internal/cli"
	"github.com/hollis-labs/substrate/folio/internal/manifest"
)

// writeBreadcrumb writes a minimal .folio.yaml naming one root preset, as an older folio would have.
func writeBreadcrumb(t *testing.T, id, version string) string {
	t.Helper()
	dir := t.TempDir()
	m := manifest.Manifest{
		FolioVersion: "0.1",
		GeneratedAt:  time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Generator:    "folio/v0.0.1",
		Presets:      []manifest.PresetRef{{ID: id, Version: version, Source: "bundled"}},
		Inputs:       map[string]any{"project_name": "old_project"},
	}
	if err := manifest.Write(dir, m); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A project generated from a preset this folio no longer ships fails with a sentence naming the preset
// and the release that last had it: no stack trace, no render, and never a silent re-render onto the
// current preset of the same name.
func TestFolioInspect_RemovedPresetBreadcrumb(t *testing.T) {
	for _, c := range []struct{ id, version string }{
		{"go-service-app", "0.2.0"},
		{"chat-app", "1.1.0"},
		{"app-dashboard-chimera-plugin", "1.0.0"},
		{"chat-app-chimera-plugin", "1.0.0"},
		{"chimera-plugin-fixture", "1.0.0"},
		{"app-dashboard", "2.1.0"}, // the go-webui/Chimera dashboard
		{"app-dashboard", "1.2.0"},
		{"sysop-ui", "1.2.0"}, // the old alias of the same preset
	} {
		t.Run(c.id+"@"+c.version, func(t *testing.T) {
			isolateHome(t)
			dir := writeBreadcrumb(t, c.id, c.version)
			out, _, err := runCLI(t, "inspect", dir)
			if err == nil {
				t.Fatalf("inspect of a %s %s breadcrumb must fail; output:\n%s", c.id, c.version, out)
			}
			msg := err.Error()
			for _, want := range []string{"preset_removed", `"` + c.id + `"`, c.version, "v0.0.1", "folio/v0.0.1", "was removed from folio"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error lacks %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, "goroutine") || strings.Contains(msg, "panic") {
				t.Errorf("error looks like a stack trace: %s", msg)
			}
			if code := cli.Run([]string{"inspect", dir}, loadBundledFS(t), "0.0.0-test"); code == cli.ExitOK {
				t.Error("exit must be non-zero")
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(manifest.ManifestFilename) {
				t.Errorf("inspect must write nothing; directory holds %v (%v)", entries, readErr)
			}
		})
	}
}

// Retired frontend breadcrumbs and commands fail explicitly and write no files.
func TestFolio_RetiredFrontendPresets(t *testing.T) {
	for _, id := range []string{"app-ade", "app-chat", "app-dashboard", "substrate-frontend", "sysop-ui"} {
		t.Run(id, func(t *testing.T) {
			isolateHome(t)
			dir := writeBreadcrumb(t, id, "3.0.0")
			original, err := os.ReadFile(filepath.Join(dir, manifest.ManifestFilename))
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"inspect", dir}, {"new", id, filepath.Join(dir, "new"), "--non-interactive"}, {"plan", id, filepath.Join(dir, "new"), "--non-interactive"}} {
				_, _, err = runCLI(t, args...)
				if err == nil || !strings.Contains(err.Error(), "preset_removed") || !strings.Contains(err.Error(), "2bf3f4951c219") {
					t.Fatalf("%v: want retired preset recovery error, got %v", args, err)
				}
			}
			after, err := os.ReadFile(filepath.Join(dir, manifest.ManifestFilename))
			if err != nil || string(after) != string(original) {
				t.Fatal("retirement check changed breadcrumb")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatal("retirement check wrote files")
			}
		})
	}
}

// `folio new` and `folio plan` of a removed preset say so instead of "not found".
func TestFolioNew_RemovedPreset(t *testing.T) {
	for _, id := range []string{"go-service-app", "chat-app", "app-dashboard-chimera-plugin", "chat-app-chimera-plugin", "chimera-plugin-fixture"} {
		isolateHome(t)
		target := filepath.Join(t.TempDir(), "proj")
		for _, cmd := range []string{"new", "plan"} {
			_, _, err := runCLI(t, cmd, id, target, "--non-interactive")
			if err == nil || !strings.Contains(err.Error(), "preset_removed") || !strings.Contains(err.Error(), "v0.0.1") {
				t.Errorf("folio %s %s: want a preset_removed error naming v0.0.1, got %v", cmd, id, err)
			}
		}
		if _, statErr := os.Stat(target); statErr == nil {
			t.Errorf("%s: nothing may be written", id)
		}
	}
}
