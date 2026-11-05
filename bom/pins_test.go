package bom

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bomYAML = `
toolchain: {go_floor: "1.26.6", go_min: "1.26.10"}
ci:
  actions_checkout: "v5"
  actions_setup_go: "v6"
  golangci_lint_action: "v7"
  golangci_lint_version: "v2.11.4"
  govulncheck_version: "v1.8.0"
  actions_upload_artifact: "v4"
  postgres_image: "postgres:17"
libs:
  github.com/hollis-labs/libs/core/store: v0.1.0
third_party:
  github.com/spf13/cobra: v1.10.2
`

const goMod = `module example.com/x

go 1.26.10

require (
	github.com/hollis-labs/libs/core/store v0.1.0
	github.com/spf13/cobra v1.10.2
)

require github.com/other/indirect v1.0.0 // indirect
`

const workflow = `steps:
  - uses: actions/checkout@v5
  - uses: actions/setup-go@v6
  - uses: golangci/golangci-lint-action@v7
    with:
      version: v2.11.4
  - uses: actions/upload-artifact@v4
  - image: postgres:17
`

const folio = `# header
go:
  version: "1.26.6"
ci:
  actions_checkout: "v5"
  actions_setup_go: "v6"
  golangci_lint_action: "v7"
  golangci_lint_version: "v2.11.4"
  govulncheck_version: "v1.8.0"

# Opt-in maintained host recipe; legacy go-webui presets are unchanged.
chimera:
  version: "keep-me"
`

func fixture(t *testing.T, mod, wf string) (*BOM, Targets) {
	t.Helper()
	dir := t.TempDir()
	put := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	b, err := Load(put("bom.yaml", bomYAML))
	if err != nil {
		t.Fatal(err)
	}
	return b, Targets{GoMod: put("go.mod", mod), Workflows: []string{put("ci.yml", wf)}, Folio: put("platform-pins.yaml", folio)}
}

func mustCheck(t *testing.T, b *BOM, tg Targets) []Diff {
	t.Helper()
	ds, err := Check(b, tg)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestInStepMirrorMatches(t *testing.T) {
	b, tg := fixture(t, goMod, workflow)
	// Folio's fixture is hand-written, so first make it the rendered form; after
	// that everything agrees and sync is a no-op.
	if _, err := Sync(b, tg); err != nil {
		t.Fatal(err)
	}
	if ds := mustCheck(t, b, tg); len(ds) != 0 {
		t.Fatalf("diffs after sync: %v", ds)
	}
	changed, err := Sync(b, tg)
	if err != nil || len(changed) != 0 {
		t.Fatalf("second sync changed %v, err %v", changed, err)
	}
	cur, _ := os.ReadFile(tg.Folio)
	if !strings.Contains(string(cur), `version: "keep-me"`) {
		t.Error("Folio-owned chimera block was not carried over")
	}
}

func TestDriftIsDetectedAndFixed(t *testing.T) {
	for _, c := range []struct {
		name     string
		mod, wf  string
		wantDiff string
	}{
		{"lib bump", strings.Replace(goMod, "core/store v0.1.0", "core/store v0.2.0", 1), workflow, "core/store is v0.2.0, bom pins v0.1.0"},
		{"go directive", strings.Replace(goMod, "go 1.26.10", "go 1.26.9", 1), workflow, "go directive is 1.26.9"},
		{"action pin", goMod, strings.Replace(workflow, "checkout@v5", "checkout@v4", 1), "ci.actions_checkout"},
		{"upload-artifact", goMod, strings.Replace(workflow, "upload-artifact@v4", "upload-artifact@v3", 1), "ci.actions_upload_artifact"},
		{"postgres image", goMod, strings.Replace(workflow, "postgres:17", "postgres:16", 1), "ci.postgres_image"},
		{"lint version", goMod, strings.Replace(workflow, "version: v2.11.4", "version: v2.9.0", 1), "ci.golangci_lint_version"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, tg := fixture(t, c.mod, c.wf)
			if _, err := Sync(b, Targets{Folio: tg.Folio}); err != nil { // start with Folio in step
				t.Fatal(err)
			}
			ds := mustCheck(t, b, tg)
			if len(ds) != 1 || !strings.Contains(ds[0].String(), c.wantDiff) {
				t.Fatalf("diffs = %v, want one containing %q", ds, c.wantDiff)
			}
			if _, err := Sync(b, tg); err != nil {
				t.Fatal(err)
			}
			if ds := mustCheck(t, b, tg); len(ds) != 0 {
				t.Fatalf("still drifting after sync: %v", ds)
			}
		})
	}
}

func TestFolioDriftIsDetected(t *testing.T) {
	b, tg := fixture(t, goMod, workflow)
	if _, err := Sync(b, tg); err != nil {
		t.Fatal(err)
	}
	cur, _ := os.ReadFile(tg.Folio)
	if err := os.WriteFile(tg.Folio, []byte(strings.Replace(string(cur), `"1.26.6"`, `"1.26.5"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if ds := mustCheck(t, b, tg); len(ds) != 1 {
		t.Fatalf("diffs = %v, want exactly the Folio mirror", ds)
	}
}

func TestUnpinnedAndMissingNeedAHuman(t *testing.T) {
	b, tg := fixture(t, goMod+"\nrequire github.com/new/dep v1.0.0\n", workflow)
	ds := mustCheck(t, b, tg)
	found := false
	for _, d := range ds {
		found = found || strings.Contains(d.Msg, "unpinned: go.mod requires github.com/new/dep")
	}
	if !found {
		t.Fatalf("direct requirement the bom does not pin was not reported: %v", ds)
	}
	if _, err := Sync(b, Targets{GoMod: tg.GoMod}); err == nil {
		t.Error("sync must not paper over an unpinned requirement")
	}

	b2, tg2 := fixture(t, strings.Replace(goMod, "\tgithub.com/spf13/cobra v1.10.2\n", "", 1), workflow)
	if ds := mustCheck(t, b2, tg2); !strings.Contains(ds[0].String(), "missing:") {
		t.Fatalf("pinned module absent from go.mod was not reported: %v", ds)
	}
}

func TestKitPinsMustBeExactScopedVersions(t *testing.T) {
	for name, kits := range map[string]string{
		"range":    "kits:\n  \"@s/a\": ^1.0.0\n",
		"unscoped": "kits:\n  a: 1.0.0\n",
	} {
		p := filepath.Join(t.TempDir(), "bom.yaml")
		_ = os.WriteFile(p, []byte(bomYAML+kits), 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	good := filepath.Join(t.TempDir(), "bom.yaml")
	_ = os.WriteFile(good, []byte(bomYAML+"kits:\n  \"@s/a\": 1.0.0\n"), 0o644)
	if _, err := Load(good); err != nil {
		t.Errorf("exact scoped pin refused: %v", err)
	}
}

func TestCheckNPMReportsUnpublishedVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@s/a/1.0.0" {
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	b := &BOM{Kits: map[string]string{"@s/a": "1.0.0", "@s/b": "2.0.0"}}
	ds, err := CheckNPM(b, srv.URL)
	if err != nil || len(ds) != 1 || !strings.Contains(ds[0].Msg, "@s/b@2.0.0 is not published") {
		t.Fatalf("diffs = %v, err = %v", ds, err)
	}
}

func TestBOMValidation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bom.yaml")
	bad := strings.Replace(bomYAML, `go_min: "1.26.10"`, `go_min: "1.26.5"`, 1)
	_ = os.WriteFile(p, []byte(bad), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "below go_floor") {
		t.Fatalf("err = %v", err)
	}
}

const appBOM = bomYAML + `
app_libs:
  github.com/hollis-labs/libs/workflow/engine: v0.2.0
`

func appFixture(t *testing.T, goMod string) (*BOM, string) {
	t.Helper()
	dir := t.TempDir()
	bp := filepath.Join(dir, "bom.yaml")
	if err := os.WriteFile(bp, []byte(appBOM), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(bp)
	if err != nil {
		t.Fatal(err)
	}
	gp := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(gp, []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	return b, gp
}

const appGoMod = `module example.com/app

go 1.26.10

require (
	github.com/hollis-labs/substrate v0.2.0
	github.com/hollis-labs/libs/workflow/engine v0.1.0
	github.com/spf13/cobra v1.9.0
	github.com/some/other v1.0.0
)

require github.com/hollis-labs/libs/core/store v0.0.9 // indirect
`

func TestCheckAppHoldsLibsAndPinnedThirdPartyToTheBOM(t *testing.T) {
	b, gp := appFixture(t, appGoMod)
	ds, err := CheckApp(b, gp)
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, d := range ds {
		all = append(all, d.Msg)
	}
	got := strings.Join(all, "\n")
	for _, want := range []string{"workflow/engine is v0.1.0, bom pins v0.2.0", "cobra is v1.9.0, bom pins v1.10.2", "core/store is v0.0.9, bom pins v0.1.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "some/other") || strings.Contains(got, "substrate") {
		t.Errorf("the app's own modules and the substrate requirement must not be reported:\n%s", got)
	}
	changed, err := SyncApp(b, gp)
	if err != nil || !changed {
		t.Fatalf("sync: changed=%v err=%v", changed, err)
	}
	if ds, _ := CheckApp(b, gp); len(ds) != 0 {
		t.Fatalf("still drifting after sync: %v", ds)
	}
	if src, _ := os.ReadFile(gp); !strings.Contains(string(src), "github.com/some/other v1.0.0") {
		t.Error("sync touched a module the BOM does not pin")
	}
}

func TestCheckAppRefusesAnUnpinnedLibsModule(t *testing.T) {
	b, gp := appFixture(t, "module example.com/app\n\ngo 1.26.10\n\nrequire github.com/hollis-labs/libs/mesh/core v0.8.0\n")
	ds, _ := CheckApp(b, gp)
	if len(ds) != 1 || !strings.Contains(ds[0].Msg, "unpinned: github.com/hollis-labs/libs/mesh/core") {
		t.Fatalf("diffs = %v", ds)
	}
	if _, err := SyncApp(b, gp); err == nil {
		t.Error("sync must not paper over an unpinned libs module")
	}
}

func TestAppLibsMustBeLibsAndNotDuplicated(t *testing.T) {
	for name, extra := range map[string]string{
		"not a libs module": "app_libs:\n  github.com/other/x: v1.0.0\n",
		"duplicate":         "app_libs:\n  github.com/hollis-labs/libs/core/store: v0.1.0\n",
		"range":             "app_libs:\n  github.com/hollis-labs/libs/x/y: ^1.0.0\n",
	} {
		p := filepath.Join(t.TempDir(), "bom.yaml")
		_ = os.WriteFile(p, []byte(bomYAML+extra), 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
