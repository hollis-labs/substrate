package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/manifest"
)

func TestGenUIOptionalGeneratedWiringBuilds(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "selected"}[selected], func(t *testing.T) {
			m := &manifest.Manifest{App: "sample", Variant: "service", Profiles: []string{"minimal"}}
			if selected {
				m.Modules.Add = []string{"genui"}
			}
			dir := t.TempDir()
			path, err := generate(m, filepath.Join(dir, "substrate.yaml"), "main")
			if err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(source), `"github.com/hollis-labs/substrate/modules/genui"`) != selected {
				t.Fatal("module flag did not control generated linkage")
			}
			main := filepath.Join(dir, "main.go")
			if err = os.WriteFile(main, []byte("package main\nfunc main() {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "fixture"), path, main)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated standalone build: %v\n%s", err, out)
			}
		})
	}
}
