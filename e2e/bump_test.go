package e2e

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/bom"
)

// An app depends on Substrate vN only: its go.mod names no libs, and every pin
// the BOM carries resolves to exactly the BOM's version. Bumping the Substrate
// version therefore moves every pin with only that one line changing.

// appFor builds a throwaway app module that links the given Substrate modules,
// runs go mod tidy, and returns its direct requirements and the resolved
// version of every module in the build.
func appFor(t *testing.T, substrate string, replace string, modules []string) (direct []string, resolved map[string]string) {
	t.Helper()
	dir := t.TempDir()
	gomod := "module example.com/bumpapp\n\ngo 1.26.10\n\nrequire github.com/hollis-labs/substrate " + substrate + "\n"
	if replace != "" {
		gomod += "\nreplace github.com/hollis-labs/substrate => " + replace + "\n"
	}
	var imports strings.Builder
	for _, m := range modules {
		imports.WriteString("\t_ \"github.com/hollis-labs/substrate/modules/" + m + "\"\n")
	}
	mustWrite(t, filepath.Join(dir, "go.mod"), gomod)
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nimport (\n"+imports.String()+")\n\nfunc main() {}\n")
	run(t, dir, "go", "mod", "tidy")

	for _, line := range strings.Split(run(t, dir, "go", "list", "-m", "-f", "{{if not .Indirect}}{{.Path}}{{end}}", "all"), "\n") {
		if line = strings.TrimSpace(line); line != "" && line != "example.com/bumpapp" {
			direct = append(direct, line)
		}
	}
	resolved = map[string]string{}
	for _, line := range strings.Split(run(t, dir, "go", "list", "-m", "-f", "{{.Path}} {{.Version}}", "all"), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			resolved[f[0]] = f[1]
		}
	}
	return direct, resolved
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertResolvesToBOM checks every Go pin in b against what the app resolved.
func assertResolvesToBOM(t *testing.T, b *bom.BOM, resolved map[string]string) {
	t.Helper()
	for _, pins := range []map[string]string{b.Libs, b.ThirdParty} {
		for path, want := range pins {
			if got := resolved[path]; got != want {
				t.Errorf("%s resolves to %q, the BOM pins %q", path, got, want)
			}
		}
	}
}

var allModules = []string{"config", "logging", "health", "httphost", "capabilities", "auth", "policy", "secrets", "sqlite", "mcp", "pluginhost", "cli", "events"}

func TestAppDependsOnSubstrateOnlyAndResolvesToTheBOM(t *testing.T) {
	r := root(t)
	b, err := bom.Load(filepath.Join(r, "bom", "bom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	direct, resolved := appFor(t, "v0.0.0", r, allModules)
	if len(direct) != 1 || direct[0] != "github.com/hollis-labs/substrate" {
		t.Errorf("an app should require Substrate only, got direct requirements %v", direct)
	}
	assertResolvesToBOM(t, b, resolved)
}

// The literal bump: the same app against the released v0.1.0 and against this
// tree. In each, every pin equals that version's own BOM, and the only line of
// the app that differs is the Substrate requirement. Needs the module proxy;
// set SUBSTRATE_E2E_OFFLINE=1 to skip it explicitly.
func TestBumpingTheSubstrateVersionMovesEveryPin(t *testing.T) {
	if os.Getenv("SUBSTRATE_E2E_OFFLINE") == "1" {
		t.Skip("SUBSTRATE_E2E_OFFLINE=1")
	}
	r := root(t)
	released := []string{"config", "logging", "health", "httphost", "capabilities", "auth", "policy", "secrets", "sqlite", "mcp", "pluginhost", "cli"} // v0.1.0 has no events
	oldDirect, oldResolved := appFor(t, "v0.1.0", "", released)
	newDirect, newResolved := appFor(t, "v0.0.0", r, released)

	if strings.Join(oldDirect, ",") != strings.Join(newDirect, ",") {
		t.Errorf("direct requirements differ across the bump: %v vs %v", oldDirect, newDirect)
	}
	var d struct{ Dir string }
	if err := json.Unmarshal([]byte(run(t, r, "go", "mod", "download", "-json", "github.com/hollis-labs/substrate@v0.1.0")), &d); err != nil || d.Dir == "" {
		t.Fatalf("locate v0.1.0: %v", err)
	}
	oldBOM, err := bom.Load(filepath.Join(d.Dir, "bom", "bom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	newBOM, err := bom.Load(filepath.Join(r, "bom", "bom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("v0.1.0", func(t *testing.T) { assertResolvesToBOM(t, oldBOM, oldResolved) })
	t.Run("this tree", func(t *testing.T) { assertResolvesToBOM(t, newBOM, newResolved) })
}

type result struct {
	out string
	err error
}

// exec_ runs a command and returns its output and error without failing the test.
func exec_(t *testing.T, dir, name string, args ...string) result {
	t.Helper()
	c := osexec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return result{string(out), err}
}
