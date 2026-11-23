package folio_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/folio"
	"github.com/hollis-labs/tools/folio/service"
)

func renderSurface(t *testing.T, preset string, inputs map[string]any) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "app")
	svc := service.New(service.Options{BundledFS: folio.BundledPresets, BundledRoot: "presets", UserDir: t.TempDir(), FolioVersion: folio.Version, Now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)})
	if _, err := svc.New(service.NewOptions{PresetID: preset, TargetDir: target, Inputs: inputs}); err != nil {
		t.Fatalf("render %s: %v", preset, err)
	}
	return target
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Everything a Surface app gets is one release: the registry ref, every @hollis-labs/surface-* package
// (exact) and, for a desk app, the Go module. No old names, no unpinned registry.
func TestSurfacePresets_PinOneRelease(t *testing.T) {
	for _, preset := range []string{"surface-app", "surface-desk"} {
		t.Run(preset, func(t *testing.T) {
			dir := renderSurface(t, preset, map[string]any{"repo_name": "pin-check"})

			var lock struct {
				Registry, Ref, UIDir string
				Items                map[string]any
			}
			if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "ui.lock.json"))), &lock); err != nil {
				t.Fatal(err)
			}
			if lock.Ref != "v0.1.0" || lock.UIDir != "src/components/ui" ||
				lock.Registry != "https://raw.githubusercontent.com/hollis-labs/surface/{ref}/registry/r/{name}.json" {
				t.Errorf("ui.lock.json: %+v", lock)
			}

			var pkg struct{ Dependencies, DevDependencies map[string]string }
			if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "package.json"))), &pkg); err != nil {
				t.Fatal(err)
			}
			all := map[string]string{}
			for k, v := range pkg.Dependencies {
				all[k] = v
			}
			for k, v := range pkg.DevDependencies {
				all[k] = v
			}
			want := []string{"@hollis-labs/surface-ui"}
			if preset == "surface-desk" {
				want = append(want, "@hollis-labs/surface-page", "@hollis-labs/surface-chat", "@hollis-labs/surface-cards", "@hollis-labs/surface-genui")
			}
			for _, name := range want {
				if all[name] != "0.1.0" {
					t.Errorf("%s = %q, want exactly 0.1.0", name, all[name])
				}
			}
			for name, v := range all {
				if strings.ContainsAny(v, "^~*><") || v == "latest" {
					t.Errorf("%s is not pinned exactly: %q", name, v)
				}
			}

			err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				s := readFile(t, p)
				for _, stale := range []string{`"@surface/`, "scripts/ui.mjs", "surface/main/", "{ref}/main"} {
					if strings.Contains(s, stale) {
						t.Errorf("%s mentions %q", p, stale)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			if preset == "surface-desk" {
				if gomod := readFile(t, filepath.Join(dir, "go.mod")); !strings.Contains(gomod, "require github.com/hollis-labs/surface v0.1.0") {
					t.Errorf("go.mod:\n%s", gomod)
				}
			}
		})
	}
}

// The raw-control rule covers the views and leaves the installed components alone; CI runs verify.
func TestSurfacePresets_LintAndCI(t *testing.T) {
	for _, preset := range []string{"surface-app", "surface-desk"} {
		dir := renderSurface(t, preset, map[string]any{"repo_name": "lint-check"})
		biome := readFile(t, filepath.Join(dir, "biome.json"))
		for _, want := range []string{`"noRestrictedElements"`, `"button"`, `"input"`, `"select"`, `"textarea"`, `"!src/components/ui"`} {
			if !strings.Contains(biome, want) {
				t.Errorf("%s biome.json lacks %s", preset, want)
			}
		}
		if ci := readFile(t, filepath.Join(dir, ".github/workflows/ci.yml")); !strings.Contains(ci, "npx surface-ui verify") {
			t.Errorf("%s CI does not run surface-ui verify", preset)
		}
		if agents := readFile(t, filepath.Join(dir, "AGENTS.md")); !strings.Contains(agents, "docs/gui.md") {
			t.Errorf("%s AGENTS.md does not point at docs/gui.md", preset)
		}
	}
}

// TestSurfacePresets_Acceptance installs, builds and checks rendered apps against the published Surface
// release: npm, GitHub and the Go proxy. Opt in with FOLIO_SURFACE_ACCEPTANCE=1 (plain) or =all (and desk).
func TestSurfacePresets_Acceptance(t *testing.T) {
	mode := os.Getenv("FOLIO_SURFACE_ACCEPTANCE")
	if mode == "" {
		t.Skip("set FOLIO_SURFACE_ACCEPTANCE=1 (plain app) or all (and the desk app); needs npm and network")
	}
	presets := []string{"surface-app"}
	if mode == "all" {
		presets = append(presets, "surface-desk")
	}
	for _, preset := range presets {
		t.Run(preset, func(t *testing.T) {
			dir := renderSurface(t, preset, map[string]any{"repo_name": "accept-app"})
			run := func(name string, args ...string) (string, error) {
				cmd := exec.Command(name, args...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				return string(out), err
			}
			must := func(name string, args ...string) {
				t.Helper()
				if out, err := run(name, args...); err != nil {
					t.Fatalf("%s %v: %v\n%s", name, args, err, out)
				}
			}
			must("make", "setup")
			must("make", "check")

			// A raw <button> in a view fails lint.
			view := filepath.Join(dir, "src/views/home.tsx")
			if preset == "surface-desk" {
				view = filepath.Join(dir, "src/modules/start.tsx")
			}
			orig := readFile(t, view)
			raw := strings.Replace(orig, "<CardContent", `<button type="button">raw</button><CardContent`, 1)
			if raw == orig {
				t.Fatal("no place to put a raw button")
			}
			if err := os.WriteFile(view, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := run("npm", "run", "lint"); err == nil || !strings.Contains(out, "noRestrictedElements") {
				t.Errorf("a raw <button> passed lint: %v\n%s", err, out)
			}
			if err := os.WriteFile(view, []byte(orig), 0o644); err != nil {
				t.Fatal(err)
			}

			// An edit to an installed component fails verify.
			button := filepath.Join(dir, "src/components/ui/button.tsx")
			b := readFile(t, button)
			if err := os.WriteFile(button, []byte(b+"// edited in place\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := run("npx", "surface-ui", "verify"); err == nil || !strings.Contains(out, "edited in place") {
				t.Errorf("an edited component passed verify: %v\n%s", err, out)
			}
			if err := os.WriteFile(button, []byte(b), 0o644); err != nil {
				t.Fatal(err)
			}
			must("npx", "surface-ui", "verify")
			must("npm", "run", "lint")
		})
	}
}
