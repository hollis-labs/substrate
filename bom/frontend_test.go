package bom

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	dtok  = "@hollis-labs/design-tokens"
	dcomp = "@hollis-labs/design-components"
	ksets = "@hollis-labs/kit-settings"
	kchat = "@hollis-labs/kit-chat"
)

func kitBOM() *BOM {
	return &BOM{
		Kits: map[string]string{dtok: "0.5.0", dcomp: "0.5.1", ksets: "0.2.0", kchat: "0.8.0"},
		KitExceptions: map[string]string{
			ksets + ">" + dtok:  "kit-settings still depends on design-tokens ^0.4",
			ksets + ">" + dcomp: "kit-settings still depends on design-components ^0.4",
		},
	}
}

type pk = map[string]any

func nm(n string) string { return "node_modules/" + n }

// cleanLock is a lockfile that matches kitBOM, exactly as npm would write it.
func cleanLock() map[string]pk {
	return map[string]pk{
		"":         {"name": "app", "dependencies": map[string]string{kchat: "0.8.0", ksets: "0.2.0", dtok: "0.5.0", dcomp: "0.5.1"}},
		nm(dtok):   {"version": "0.5.0"},
		nm(dcomp):  {"version": "0.5.1", "peerDependencies": map[string]string{dtok: "^0.5.0"}},
		nm(kchat):  {"version": "0.8.0", "dependencies": map[string]string{dtok: "^0.5.0"}, "peerDependencies": map[string]string{dcomp: "^0.5.0"}},
		nm(ksets):  {"version": "0.2.0", "dependencies": map[string]string{dtok: "^0.4.0", dcomp: "^0.4.0"}},
		nm("left"): {"version": "1.0.0"},
	}
}

func writeFE(t *testing.T, pkgDeps map[string]string, lock map[string]pk) string {
	t.Helper()
	dir := t.TempDir()
	pj, _ := json.Marshal(map[string]any{"name": "app", "version": "0.0.0", "dependencies": pkgDeps})
	lj, _ := json.Marshal(map[string]any{"name": "app", "version": "0.0.0", "lockfileVersion": 3, "requires": true, "packages": lock})
	if err := os.WriteFile(filepath.Join(dir, "package.json"), pj, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), lj, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func cleanDeps() map[string]string {
	return map[string]string{kchat: "0.8.0", ksets: "0.2.0", dtok: "0.5.0", dcomp: "0.5.1"}
}

func run(t *testing.T, dir string) ([]Diff, []string, error) {
	t.Helper()
	return CheckFrontend(kitBOM(), FrontendOptions{Dir: dir, SkipNPMLs: true})
}

func joined(ds []Diff) string {
	var s []string
	for _, d := range ds {
		s = append(s, d.String())
	}
	return strings.Join(s, "\n")
}

func TestFrontendCleanPasses(t *testing.T) {
	ds, acc, err := run(t, writeFE(t, cleanDeps(), cleanLock()))
	if err != nil || len(ds) != 0 {
		t.Fatalf("clean lockfile: diffs=%v err=%v", joined(ds), err)
	}
	// kit-settings needs ^0.4 and resolves to 0.5: reported as accepted, not hidden.
	if len(acc) != 2 || !strings.Contains(acc[0], "accepted by kit_exceptions") {
		t.Fatalf("want the two kit-settings exceptions accepted, got %v", acc)
	}
}

func TestFrontendExceptionWithNestedCopyPasses(t *testing.T) {
	l := cleanLock()
	l[nm(ksets)+"/"+nm(dtok)] = pk{"version": "0.4.0"}
	l[nm(ksets)+"/"+nm(dcomp)] = pk{"version": "0.4.1"}
	ds, acc, err := run(t, writeFE(t, cleanDeps(), l))
	if err != nil || len(ds) != 0 {
		t.Fatalf("documented nested copies must pass: %v %v", joined(ds), err)
	}
	var nested int
	for _, a := range acc {
		if strings.Contains(a, "installed a second time") {
			nested++
		}
	}
	if nested != 2 {
		t.Fatalf("want both nested copies reported as accepted, got %v", acc)
	}
}

func TestFrontendStaleNestedCopyFails(t *testing.T) {
	l := cleanLock()
	l[nm("@hollis-labs/kit-code")] = pk{"version": "0.0.0", "dependencies": map[string]string{dtok: "^0.4.0"}}
	l[nm("@hollis-labs/kit-code")+"/"+nm(dtok)] = pk{"version": "0.4.0"}
	ds, _, err := run(t, writeFE(t, cleanDeps(), l))
	if err != nil {
		t.Fatal(err)
	}
	got := joined(ds)
	for _, want := range []string{"duplicate:", "node_modules/@hollis-labs/kit-code/node_modules/" + dtok, "0.4.0", "BOM pins 0.5.0", "nested under @hollis-labs/kit-code"} {
		if !strings.Contains(got, want) {
			t.Errorf("message lacks %q:\n%s", want, got)
		}
	}
	if len(ds) != 1 {
		t.Errorf("want exactly one finding, got:\n%s", got)
	}
}

func TestFrontendEveryDuplicateListed(t *testing.T) {
	l := cleanLock()
	for _, k := range []string{"a", "b"} {
		l[nm("kit-"+k)] = pk{"version": "1.0.0"}
		l[nm("kit-"+k)+"/"+nm(dcomp)] = pk{"version": "0.4.0"}
	}
	ds, _, _ := run(t, writeFE(t, cleanDeps(), l))
	if len(ds) != 2 || !strings.Contains(joined(ds), "kit-a/node_modules") || !strings.Contains(joined(ds), "kit-b/node_modules") {
		t.Fatalf("want a finding per copy with its path:\n%s", joined(ds))
	}
}

func TestFrontendTopLevelMismatchFails(t *testing.T) {
	deps := cleanDeps()
	deps[dtok] = "^0.5.0"
	l := cleanLock()
	l[nm(dcomp)] = pk{"version": "0.5.0", "peerDependencies": map[string]string{dtok: "^0.5.0"}}
	ds, _, err := run(t, writeFE(t, deps, l))
	if err != nil {
		t.Fatal(err)
	}
	got := joined(ds)
	for _, want := range []string{`package.json: ` + dtok + ` is "^0.5.0"`, "no ^ or ~", "package-lock.json: node_modules/" + dcomp + " is 0.5.0, but the BOM pins 0.5.1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFrontendInvalidRangeFails(t *testing.T) {
	l := cleanLock()
	// kit-chat needs design-tokens ^0.6, the tree has 0.5.0.
	l[nm(kchat)] = pk{"version": "0.8.0", "dependencies": map[string]string{dtok: "^0.6.0"}}
	ds, _, _ := run(t, writeFE(t, cleanDeps(), l))
	got := joined(ds)
	if !strings.Contains(got, "invalid:") || !strings.Contains(got, kchat) || !strings.Contains(got, "^0.6.0") || !strings.Contains(got, "0.5.0 is installed at node_modules/"+dtok) {
		t.Fatalf("want an invalid finding naming the kit, range, version and place:\n%s", got)
	}
}

func TestFrontendMissingFails(t *testing.T) {
	l := cleanLock()
	delete(l, nm(dtok))
	deps := cleanDeps()
	delete(deps, dtok)
	l[""]["dependencies"] = deps
	ds, _, _ := run(t, writeFE(t, deps, l))
	if !strings.Contains(joined(ds), "missing:") {
		t.Fatalf("want missing finding:\n%s", joined(ds))
	}
}

func TestFrontendOptionalPeerMissingPasses(t *testing.T) {
	l := cleanLock()
	l[nm("left")] = pk{"version": "1.0.0", "peerDependencies": map[string]string{"@hollis-labs/kit-admin": "^0.2.0"}, "peerDependenciesMeta": map[string]any{"@hollis-labs/kit-admin": map[string]bool{"optional": true}}}
	b := kitBOM()
	b.Kits["@hollis-labs/kit-admin"] = "0.2.0"
	ds, _, err := CheckFrontend(b, FrontendOptions{Dir: writeFE(t, cleanDeps(), l), SkipNPMLs: true})
	if err != nil || len(ds) != 0 {
		t.Fatalf("%v %v", joined(ds), err)
	}
}

func TestFrontendUnpinnedPackagesIgnored(t *testing.T) {
	l := cleanLock()
	l[nm("left")+"/"+nm("@hollis-labs/kit-other")] = pk{"version": "9.9.9"}
	ds, _, err := run(t, writeFE(t, cleanDeps(), l))
	if err != nil || len(ds) != 0 {
		t.Fatalf("a kit the BOM does not pin is not this check's business: %v %v", joined(ds), err)
	}
}

func TestFrontendMissingInputsAreErrors(t *testing.T) {
	_, _, err := run(t, filepath.Join(t.TempDir(), "frontend"))
	if err == nil || !strings.Contains(err.Error(), "package.json not found") {
		t.Fatalf("missing frontend dir must be a clear error, got %v", err)
	}
	dir := writeFE(t, cleanDeps(), cleanLock())
	_ = os.Remove(filepath.Join(dir, "package-lock.json"))
	if _, _, err := run(t, dir); err == nil || !strings.Contains(err.Error(), "package-lock.json not found") {
		t.Fatalf("missing lockfile must be a clear error, got %v", err)
	}
	dir = t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{"lockfileVersion":1,"dependencies":{}}`), 0o644)
	if _, _, err := run(t, dir); err == nil || !strings.Contains(err.Error(), "lockfileVersion 1") {
		t.Fatalf("lockfile v1 must be refused clearly, got %v", err)
	}
}

func TestFrontendNPMMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := CheckFrontend(kitBOM(), FrontendOptions{Dir: writeFE(t, cleanDeps(), cleanLock())})
	if !errors.Is(err, ErrNPMMissing) || !strings.Contains(err.Error(), "--no-npm-ls") {
		t.Fatalf("want ErrNPMMissing naming the skip flag, got %v", err)
	}
	// with the skip flag the same tree passes without npm.
	if _, _, err := CheckFrontend(kitBOM(), FrontendOptions{Dir: writeFE(t, cleanDeps(), cleanLock()), SkipNPMLs: true}); err != nil {
		t.Fatal(err)
	}
}

// A package nested two deep finds its dependency in the enclosing node_modules,
// as node does, before falling back to the top level.
func TestFrontendResolvesThroughEnclosingNodeModules(t *testing.T) {
	l := cleanLock()
	l[nm("a")] = pk{"version": "1.0.0"}
	l[nm("a")+"/"+nm(dtok)] = pk{"version": "0.4.0"}
	l[nm("a")+"/"+nm("b")] = pk{"version": "1.0.0", "dependencies": map[string]string{dtok: "^0.4.0"}}
	ds, _, err := run(t, writeFE(t, cleanDeps(), l))
	if err != nil {
		t.Fatal(err)
	}
	if got := joined(ds); strings.Contains(got, "invalid:") || !strings.Contains(got, "duplicate:") {
		t.Fatalf("want only the duplicate reported:\n%s", got)
	}
}

// With real npm: a consistent tree has no problems, and one with a pinned kit
// below the range another asks for is reported by npm ls itself.
func TestFrontendWithRealNPM(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm is not installed")
	}
	good := cleanLock()
	good[nm(ksets)] = pk{"version": "0.2.0", "dependencies": map[string]string{dtok: "^0.5.0", dcomp: "^0.5.0"}}
	ds, _, err := CheckFrontend(kitBOM(), FrontendOptions{Dir: writeFE(t, cleanDeps(), good)})
	if err != nil || len(ds) != 0 {
		t.Fatalf("npm ls on a clean tree: %v %v", joined(ds), err)
	}
	bad := cleanLock()
	bad[nm(kchat)] = pk{"version": "0.8.0", "dependencies": map[string]string{dtok: "^0.6.0"}}
	ds, _, err = CheckFrontend(kitBOM(), FrontendOptions{Dir: writeFE(t, cleanDeps(), bad)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined(ds), "npm ls:") {
		t.Fatalf("want a finding from npm ls itself:\n%s", joined(ds))
	}
}
