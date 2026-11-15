package folio_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/folio/service"
)

func TestRepositoryGenerationAndInspection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "review-worktree")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := newGoLibService(t)
	target := filepath.Join(root, "sample")
	opts := service.NewOptions{PresetID: "substrate-service", TargetDir: target, RepositoryRoot: root,
		Inputs: map[string]any{"repo_name": "sample", "repository_name": "tools", "substrate_version": "v0.1.1-0.20261115140033-66978e9d84cd"}}
	opts.Inputs["repository_name"] = ""
	if _, err := svc.Plan(opts); err == nil || !strings.Contains(err.Error(), "requires --input repository_name") {
		t.Fatalf("missing repository identity: %v", err)
	}
	opts.Inputs["repository_name"] = "tools"
	plan, err := svc.Plan(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("plan wrote target: %v", err)
	}
	if plan.Computed["module_path"] != "github.com/hollis-labs/tools/sample" {
		t.Fatalf("module identity: %v", plan.Computed)
	}
	result, err := svc.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.RepositoryLocation != "sample" {
		t.Fatal("module location not recorded")
	}
	for source, record := range result.Manifest.Files {
		if record.RepositoryPath == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, record.RepositoryPath)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(target, source)); !os.IsNotExist(err) {
			t.Fatalf("inactive module workflow remains: %s", source)
		}
	}
	inspected, err := svc.Inspect(service.InspectOptions{TargetDir: target, RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range inspected.Files {
		if f.Status != service.DriftUnchanged {
			t.Errorf("unexpected drift: %+v", f)
		}
	}
	if len(inspected.Warnings) != 0 {
		t.Fatalf("inspect warnings: %v", inspected.Warnings)
	}
	// A real local edit to a repository workflow must be visible through inspect.
	workflow := filepath.Join(root, ".github/workflows/sample-check.yml")
	f, err := os.OpenFile(workflow, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n# operator customization\n")
	_ = f.Close()
	inspected, err = svc.Inspect(service.InspectOptions{TargetDir: target, RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range inspected.Files {
		if f.Path == ".github/workflows/check.yml" && f.Status == service.DriftLocallyModified {
			found = true
		}
	}
	if !found {
		t.Fatal("repository workflow edit not detected")
	}
	withoutRoot, err := svc.Inspect(service.InspectOptions{TargetDir: target})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutRoot.Warnings) == 0 {
		t.Fatal("repository reads need an explicit root")
	}
}

func TestRepositoryRefusesCollisionAndEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tools")
	workflowDir := filepath.Join(root, ".github/workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	collision := filepath.Join(workflowDir, "sample-check.yml")
	if err := os.WriteFile(collision, []byte("authored workflow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := service.NewOptions{PresetID: "go-lib", TargetDir: filepath.Join(root, "sample"), RepositoryRoot: root, Inputs: map[string]any{"repo_name": "sample", "repository_name": "tools", "package_name": "sample"}}
	svc := newGoLibService(t)
	if _, err := svc.New(opts); err == nil || !strings.Contains(err.Error(), "create repository workflow") {
		t.Fatalf("workflow collision did not reach exclusive creation: %v", err)
	}
	if _, err := os.Stat(opts.TargetDir); !os.IsNotExist(err) {
		t.Fatalf("failed render left module: %v", err)
	}
	data, err := os.ReadFile(collision)
	if err != nil || string(data) != "authored workflow\n" {
		t.Fatal("authored workflow changed")
	}
	opts.TargetDir = filepath.Join(filepath.Dir(root), "escape")
	if _, err := svc.New(opts); err == nil {
		t.Fatal("outside target accepted")
	}
	opts.TargetDir = filepath.Join(root, "link", "sample")
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.New(opts); err == nil {
		t.Fatal("symlink escape accepted")
	}
}
