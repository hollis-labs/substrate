package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Upgrades a real app from the released v0.1.0 to the module proxy's main and
// checks the command's three promises: a dry run changes nothing, a real run
// moves the one requirement and leaves an app that validates and builds, and a
// target that may break is refused until the operator says otherwise. Needs the
// network; SUBSTRATE_E2E_OFFLINE=1 skips it explicitly. "main" is whatever is
// pushed, so assertions are about behaviour, not exact versions.
func TestUpgradeAnAppFromTheReleaseToMain(t *testing.T) {
	if os.Getenv("SUBSTRATE_E2E_OFFLINE") == "1" {
		t.Skip("SUBSTRATE_E2E_OFFLINE=1")
	}
	r := root(t)
	cli := filepath.Join(t.TempDir(), "substrate")
	run(t, r, "go", "build", "-o", cli, "./cmd/substrate")

	app := t.TempDir()
	mainSrc, err := os.ReadFile(filepath.Join(r, "examples", "hello-service", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(app, "main.go"), string(mainSrc))
	mustWrite(t, filepath.Join(app, "substrate.yaml"), "app: upg\nvariant: service\nprofiles: [minimal]\n")
	mustWrite(t, filepath.Join(app, "go.mod"), "module example.com/upg\n\ngo 1.26.10\n\nrequire github.com/hollis-labs/substrate v0.1.0\n")
	run(t, app, "go", "run", "github.com/hollis-labs/substrate/cmd/substrate@v0.1.0", "generate", "-f", "substrate.yaml")
	run(t, app, "go", "mod", "tidy")    // after generate: the wiring file is what imports the modules
	run(t, app, "go", "build", "./...") // the starting point is healthy

	before, _ := os.ReadFile(filepath.Join(app, "go.mod"))
	out := run(t, app, cli, "upgrade", "-to", "main", "-dry-run", "-allow-unmigrated")
	if !strings.Contains(out, "v0.1.0 ->") {
		t.Errorf("dry run report: %s", out)
	}
	if after, _ := os.ReadFile(filepath.Join(app, "go.mod")); string(after) != string(before) {
		t.Fatal("a dry run changed go.mod")
	}

	// main is unreleased, so the policy refuses it without the flag.
	c := exec_(t, app, cli, "upgrade", "-to", "main")
	if c.err == nil || !strings.Contains(c.out, "refusing") {
		t.Fatalf("an unreleased target must be refused without -allow-unmigrated: %v\n%s", c.err, c.out)
	}
	if after, _ := os.ReadFile(filepath.Join(app, "go.mod")); string(after) != string(before) {
		t.Fatal("a refused upgrade changed go.mod")
	}

	out = run(t, app, cli, "upgrade", "-to", "main", "-allow-unmigrated")
	if !strings.Contains(out, "upgraded and verified") {
		t.Errorf("upgrade output: %s", out)
	}
	after, _ := os.ReadFile(filepath.Join(app, "go.mod"))
	if strings.Contains(string(after), "substrate v0.1.0") {
		t.Errorf("the Substrate requirement did not move:\n%s", after)
	}
	run(t, app, "go", "build", "./...")
}
