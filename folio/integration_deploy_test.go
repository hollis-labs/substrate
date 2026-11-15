package folio_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/folio/service"
	"gopkg.in/yaml.v3"
)

func TestDeploymentRegeneratesFromAuthoredDescriptor(t *testing.T) {
	dir, err := renderTool(t, "substrate-service", "regenerated", nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "project.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "http_port: 8080", "http_port: 18245", 1))
	data = append(data, []byte("  unrelated_owner: {feature: retained} # authored extension\n")...)
	if err = os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newGoLibService(t)
	plan, err := svc.RenderDeploy(service.DeployOptions{TargetDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Written {
		t.Fatal("plan wrote files")
	}
	// Exercise the returned deployment behavior rather than source template text.
	var supervision struct {
		Resources []struct {
			Config struct {
				Port    int
				Command []string
			}
		}
	}
	for _, f := range plan.Files {
		if strings.HasSuffix(f.Path, ".cerberus.yaml") {
			if err = yaml.Unmarshal(f.Content, &supervision); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(supervision.Resources) == 0 || supervision.Resources[0].Config.Port != 18245 {
		t.Fatalf("authored port not projected: %+v", supervision)
	}
	command := supervision.Resources[0].Config.Command
	if command[len(command)-1] != "127.0.0.1:18245" {
		t.Fatalf("listener command does not use authored port: %v", command)
	}
	result, err := svc.RenderDeploy(service.DeployOptions{TargetDir: dir, Write: true})
	if err != nil || !result.Written {
		t.Fatalf("write: %+v %v", result, err)
	}
	preserved, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(preserved, data) {
		t.Fatal("authored descriptor/comments/extensions changed")
	}
	again, err := svc.RenderDeploy(service.DeployOptions{TargetDir: dir, Write: true})
	if err != nil || again.Written {
		t.Fatalf("regeneration is not idempotent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "regenerated.cerberus.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentRefusesAuthoredConflictsAndInvalidRuntime(t *testing.T) {
	dir, err := renderTool(t, "substrate-service", "conflict", nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "deploy.yaml")
	edited := []byte("# operator owns this edit\n")
	if err := os.WriteFile(file, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newGoLibService(t)
	if _, err := svc.RenderDeploy(service.DeployOptions{TargetDir: dir, Write: true}); err == nil {
		t.Fatal("authored conflict overwritten")
	}
	remaining, _ := os.ReadFile(file)
	if !bytes.Equal(remaining, edited) {
		t.Fatal("failed regeneration changed authored output")
	}
	if _, err := svc.RenderDeploy(service.DeployOptions{TargetDir: dir, Write: true, Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	descriptor := filepath.Join(dir, "project.yaml")
	data, _ := os.ReadFile(descriptor)
	data = []byte(strings.Replace(string(data), "http_port: 8080", "http_port: 70000", 1))
	if err := os.WriteFile(descriptor, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RenderDeploy(service.DeployOptions{TargetDir: dir, Write: true}); err == nil || !strings.Contains(err.Error(), "http_port") {
		t.Fatalf("invalid port accepted: %v", err)
	}
}

func TestDeploymentQuotesMultilineNames(t *testing.T) {
	dir, err := renderTool(t, "substrate-service", "multiline", nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "project.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var authored map[string]any
	if err = yaml.Unmarshal(data, &authored); err != nil {
		t.Fatal(err)
	}
	authored["name"] = "First\nSecond"
	data, err = yaml.Marshal(authored)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := newGoLibService(t).RenderDeploy(service.DeployOptions{TargetDir: dir, Write: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range result.Files {
		var parsed map[string]any
		if parseErr := yaml.Unmarshal(f.Content, &parsed); parseErr != nil {
			t.Fatalf("%s: %v", f.Path, parseErr)
		}
		if strings.HasSuffix(f.Path, ".cerberus.yaml") {
			project := parsed["project"].(map[string]any)
			if project["name"] != authored["name"] {
				t.Fatalf("name changed: %v", project["name"])
			}
		}
	}
}

func TestDeploymentRefusesDescriptorOutputAliases(t *testing.T) {
	for _, link := range []string{"symlink", "hardlink"} {
		t.Run(link, func(t *testing.T) {
			dir, err := renderTool(t, "substrate-service", "alias", nil)
			if err != nil {
				t.Fatal(err)
			}
			descriptor := filepath.Join(dir, "project.yaml")
			output := filepath.Join(dir, "deploy.yaml")
			authored, err := os.ReadFile(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(output); err != nil {
				t.Fatal(err)
			}
			if link == "symlink" {
				if err = os.Rename(descriptor, output); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink("deploy.yaml", descriptor)
			} else {
				err = os.Link(descriptor, output)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = newGoLibService(t).RenderDeploy(service.DeployOptions{TargetDir: dir, Write: true, Overwrite: true})
			if err == nil {
				t.Fatal("descriptor/output alias accepted")
			}
			preserved, readErr := os.ReadFile(descriptor)
			if readErr != nil || !bytes.Equal(authored, preserved) {
				t.Fatal("authored descriptor changed")
			}
		})
	}
}
