package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFolioSync_PlanTableAndJSON(t *testing.T) {
	target := scaffoldBase(t)
	if err := os.WriteFile(filepath.Join(target, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestBefore, _ := os.ReadFile(filepath.Join(target, ".folio.yaml"))

	out, _, err := runCLI(t, "sync", target, "--plan")
	if err != nil {
		t.Fatalf("sync --plan: %v", err)
	}
	for _, want := range []string{"folio sync --plan: my-proj (preset base ", "PATH", "ACTION", "Nothing was written"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(lineWith(t, out, "README.md"), "kept_local") {
		t.Errorf("README.md row = %q", lineWith(t, out, "README.md"))
	}
	if after, _ := os.ReadFile(filepath.Join(target, ".folio.yaml")); string(after) != string(manifestBefore) {
		t.Error("--plan changed .folio.yaml")
	}

	out, _, err = runCLI(t, "sync", target, "--json")
	if err != nil {
		t.Fatalf("sync --json: %v", err)
	}
	var got struct {
		Preset    string `json:"preset"`
		Plan      bool   `json:"plan"`
		Conflicts int    `json:"conflicts"`
		Files     []struct {
			Path, Status, Action string
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got.Preset != "base" || got.Plan || got.Conflicts != 0 || len(got.Files) == 0 {
		t.Fatalf("json = %+v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "README.md")); string(b) != "edited\n" {
		t.Error("sync overwrote a local edit")
	}
	if after, _ := os.ReadFile(filepath.Join(target, ".folio.yaml")); !strings.Contains(string(after), "operation: sync") {
		t.Error("no sync_history entry")
	}
}

func TestFolioSync_BadInputFlag(t *testing.T) {
	target := scaffoldBase(t)
	if _, _, err := runCLI(t, "sync", target, "--input", "novalue"); err == nil {
		t.Fatal("a malformed --input was taken")
	}
}
