package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/folio/internal/manifest"
	"github.com/hollis-labs/substrate/folio/service"
)

// syncRules marks domain/** as authored (skip) and lint.yml as the preset's canonical
// copy (overwrite). Everything else falls to the default, prompt.
const syncRules = `sync:
  default: prompt
  rules:
    - glob: "domain/**"
      policy: skip
    - glob: "lint.yml"
      policy: overwrite
`

func syncTree(t *testing.T) *presetTree {
	return newPresetTree(t, "app", "1.0.0", "inputs:\n  - name: name\n    type: string\n    default: demo\n"+syncRules, map[string]string{
		"README.md.tmpl":      "# {{ .inputs.name }}\nv1\n",
		"config.yml":          "level: 1\n",
		"lint.yml":            "strict: false\n",
		"domain/model.go":     "package domain // v1\n",
		"domain/untouched.go": "package domain // untouched v1\n",
	})
}

// upgrade turns the fixture into version 1.1.0: every file changes, one is added in and
// one outside the skip rule, and a required input is added.
func upgrade(pt *presetTree) {
	pt.write("app/preset.yaml", "folio_version: \"0.1\"\nid: app\nversion: 1.1.0\ndescription: sync test\nauthor: t\n"+
		"inputs:\n  - name: name\n    type: string\n    default: demo\n  - name: license\n    type: string\n    required: true\n"+
		syncRules+"files:\n  source: ./files\n  template_suffix: .tmpl\n")
	pt.write("app/files/README.md.tmpl", "# {{ .inputs.name }}\nv2, {{ .inputs.license }}\n")
	pt.write("app/files/config.yml", "level: 2\n")
	pt.write("app/files/lint.yml", "strict: true\n")
	pt.write("app/files/domain/model.go", "package domain // v2\n")
	pt.write("app/files/domain/untouched.go", "package domain // untouched v2\n")
	pt.write("app/files/domain/new.go", "package domain // new upstream\n")
	pt.write("app/files/NEW.md", "new upstream\n")
}

func read(t *testing.T, target, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func edit(t *testing.T, target, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(target, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rowOf(t *testing.T, res service.SyncResult, path string) service.SyncFile {
	t.Helper()
	for _, f := range res.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no row for %s in %+v", path, res.Files)
	return service.SyncFile{}
}

func TestSync_KeepsAuthoredWorkAndReportsConflicts(t *testing.T) {
	pt := syncTree(t)
	svc := pt.service(tenAM, "1.0.0")
	target := generate(t, svc, "app", map[string]any{"name": "demo"})

	// The person writes their domain code and edits the README and lint.yml.
	authored := "package domain\n\n// Board is the person's own model.\ntype Board struct{}\n"
	edit(t, target, "domain/model.go", authored)
	edit(t, target, "README.md", "# demo\nmy own words\n")
	edit(t, target, "lint.yml", "strict: false # mine\n")
	upgrade(pt)
	before, _ := os.ReadFile(filepath.Join(target, manifest.ManifestFilename))

	// The new version needs an input the project does not record.
	_, err := svc.Sync(service.SyncOptions{TargetDir: target})
	var se *service.Error
	if !errors.As(err, &se) || se.Code != service.ErrInputMissing || !strings.Contains(se.Error(), "--input") {
		t.Fatalf("sync without the new input: %v", err)
	}

	// --plan: the same report, nothing written.
	plan, err := svc.Sync(service.SyncOptions{TargetDir: target, Plan: true, Inputs: map[string]any{"license": "MIT"}, TheirsTo: ".folio-new"})
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(t, plan, "config.yml").Action != service.SyncUpdated || rowOf(t, plan, "README.md").Action != service.SyncConflict {
		t.Fatalf("plan: %+v", plan.Files)
	}
	after, _ := os.ReadFile(filepath.Join(target, manifest.ManifestFilename))
	if string(after) != string(before) || read(t, target, "config.yml") != "level: 1\n" {
		t.Fatal("--plan wrote something")
	}
	if _, serr := os.Stat(filepath.Join(target, "README.md.folio-new")); !os.IsNotExist(serr) {
		t.Fatal("--plan wrote the theirs file")
	}

	res, err := svc.Sync(service.SyncOptions{TargetDir: target, Inputs: map[string]any{"license": "MIT"}, TheirsTo: ".folio-new"})
	if err != nil {
		t.Fatal(err)
	}
	if res.FromVersion != "1.0.0" || res.ToVersion != "1.1.0" {
		t.Errorf("versions %s -> %s", res.FromVersion, res.ToVersion)
	}

	// Authored work under the skip rule is byte-identical, edited or not, and nothing new
	// is written under it.
	if read(t, target, "domain/model.go") != authored {
		t.Error("the authored domain file was overwritten")
	}
	if read(t, target, "domain/untouched.go") != "package domain // untouched v1\n" {
		t.Error("an unedited file under the skip rule was written")
	}
	if _, serr := os.Stat(filepath.Join(target, "domain", "new.go")); !os.IsNotExist(serr) {
		t.Error("a new upstream file under the skip rule was written")
	}
	for _, p := range []string{"domain/model.go", "domain/untouched.go", "domain/new.go"} {
		if r := rowOf(t, res, p); r.Action != service.SyncSkipped || r.Policy != "skip" {
			t.Errorf("%s: %+v", p, r)
		}
	}
	// The untouched generated file is updated; the new one is added.
	if read(t, target, "config.yml") != "level: 2\n" || rowOf(t, res, "config.yml").Action != service.SyncUpdated {
		t.Errorf("config.yml: %q %+v", read(t, target, "config.yml"), rowOf(t, res, "config.yml"))
	}
	if read(t, target, "NEW.md") != "new upstream\n" || rowOf(t, res, "NEW.md").Action != service.SyncAdded {
		t.Error("NEW.md was not added")
	}
	// The conflict is reported and left; the preset's version is beside it.
	readme := rowOf(t, res, "README.md")
	if readme.Action != service.SyncConflict || readme.Theirs != "README.md.folio-new" || len(res.Conflicts()) != 1 {
		t.Errorf("README.md: %+v (conflicts %d)", readme, len(res.Conflicts()))
	}
	if read(t, target, "README.md") != "# demo\nmy own words\n" || read(t, target, "README.md.folio-new") != "# demo\nv2, MIT\n" {
		t.Error("the conflict was not left as it was, or its new rendering is wrong")
	}
	// The overwrite rule takes the preset's copy.
	if read(t, target, "lint.yml") != "strict: true\n" || rowOf(t, res, "lint.yml").Action != service.SyncOverwritten {
		t.Errorf("lint.yml: %q", read(t, target, "lint.yml"))
	}

	// The manifest moved on: the version, the new input, the history, the digests.
	m, err := manifest.Read(target)
	if err != nil {
		t.Fatal(err)
	}
	if m.Presets[len(m.Presets)-1].Version != "1.1.0" || m.Inputs["license"] != "MIT" || len(m.SyncHistory) != 2 || m.SyncHistory[1].Operation != "sync" {
		t.Errorf("manifest: presets %+v inputs %v history %+v", m.Presets, m.Inputs, m.SyncHistory)
	}
	if _, ok := m.Files["domain/new.go"]; ok {
		t.Error("a skipped new file was recorded")
	}

	// A second sync has nothing more to write; the conflict and the authored file stay.
	res2, err := svc.Sync(service.SyncOptions{TargetDir: target})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res2.Files {
		switch f.Path {
		case "README.md":
			if f.Action != service.SyncConflict {
				t.Errorf("second sync README.md: %+v", f)
			}
		case "domain/model.go", "domain/untouched.go", "domain/new.go":
			if f.Action != service.SyncSkipped {
				t.Errorf("second sync %s: %+v", f.Path, f)
			}
		default:
			if f.Action != service.SyncUnchanged {
				t.Errorf("second sync %s: %+v", f.Path, f)
			}
		}
	}
	if read(t, target, "domain/model.go") != authored {
		t.Error("the authored domain file was overwritten on the second sync")
	}
}

func TestSync_NewUpstreamFileDoesNotOverwriteAnUntrackedOne(t *testing.T) {
	pt := basicTree(t)
	svc := pt.service(tenAM, "1.0.0")
	target := generate(t, svc, "pp", map[string]any{"name": "demo"})
	edit(t, target, "CHANGELOG.md", "my changelog\n")
	pt.write("pp/files/CHANGELOG.md", "preset changelog\n")
	pt.write("pp/files/LICENSE", "license\n")

	res, err := svc.Sync(service.SyncOptions{TargetDir: target})
	if err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, res, "CHANGELOG.md"); r.Action != service.SyncConflict || read(t, target, "CHANGELOG.md") != "my changelog\n" {
		t.Errorf("CHANGELOG.md: %+v %q", r, read(t, target, "CHANGELOG.md"))
	}
	if read(t, target, "LICENSE") != "license\n" {
		t.Error("LICENSE was not added")
	}
	m, _ := manifest.Read(target)
	if _, ok := m.Files["CHANGELOG.md"]; ok {
		t.Error("the person's own CHANGELOG.md was recorded as generated")
	}
}

func TestSync_LeavesLocalEditsRemovedAndMissingFiles(t *testing.T) {
	pt := basicTree(t)
	svc := pt.service(tenAM, "1.0.0")
	target := generate(t, svc, "pp", map[string]any{"name": "demo"})
	edit(t, target, "README.md", "# mine\n")
	if err := os.Remove(filepath.Join(target, "go.mod")); err != nil {
		t.Fatal(err)
	}
	pt.remove("pp/files/NOTES.txt")

	res, err := svc.Sync(service.SyncOptions{TargetDir: target})
	if err != nil {
		t.Fatal(err)
	}
	if rowOf(t, res, "README.md").Action != service.SyncKeptLocal || read(t, target, "README.md") != "# mine\n" {
		t.Error("a local edit was not kept")
	}
	if rowOf(t, res, "go.mod").Action != service.SyncMissingLocally {
		t.Error("go.mod")
	}
	if _, err := os.Stat(filepath.Join(target, "go.mod")); !os.IsNotExist(err) {
		t.Error("a file deleted here was restored")
	}
	if rowOf(t, res, "NOTES.txt").Action != service.SyncRemovedUpstream || read(t, target, "NOTES.txt") != "static\n" {
		t.Error("a file the preset dropped was touched")
	}
}

func TestSync_RefusesASymlinkOutOfTheProject(t *testing.T) {
	pt := basicTree(t)
	svc := pt.service(tenAM, "1.0.0")
	target := generate(t, svc, "pp", map[string]any{"name": "demo"})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(target, "docs")); err != nil {
		t.Fatal(err)
	}
	pt.write("pp/files/docs/guide.md", "guide\n")

	if _, err := svc.Sync(service.SyncOptions{TargetDir: target}); err == nil {
		t.Fatal("a write through a symlink out of the project was not refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "guide.md")); !os.IsNotExist(err) {
		t.Fatal("sync wrote outside the project")
	}
}

func TestSync_TheirsToMustBeASuffix(t *testing.T) {
	pt := basicTree(t)
	svc := pt.service(tenAM, "1.0.0")
	target := generate(t, svc, "pp", map[string]any{"name": "demo"})
	for _, bad := range []string{"/x", "../x", "."} {
		if _, err := svc.Sync(service.SyncOptions{TargetDir: target, TheirsTo: bad}); err == nil {
			t.Errorf("--theirs-to %q was taken", bad)
		}
	}
}
