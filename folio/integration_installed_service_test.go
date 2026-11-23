package folio_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/folio/service"
	"gopkg.in/yaml.v3"
)

// Exercise the installed process's actual chdir/exec boundary, without registering
// a live supervisor. The finite probe replaces the service body, reports its cwd
// and arguments, and exits; only the rendered deployment controls its launch.
func TestInstalledServiceWorkingDirectory(t *testing.T) {
	buildDir := t.TempDir()
	source := filepath.Join(buildDir, "main.go")
	if err := os.WriteFile(source, []byte(`package main
import ("encoding/json"; "os")
func main() {
  cwd, err := os.Getwd(); if err != nil { panic(err) }
  if err := json.NewEncoder(os.Stdout).Encode(struct { Dir string; Args []string }{cwd, os.Args[1:]}); err != nil { panic(err) }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(buildDir, "probe")
	build := exec.Command("go", "build", "-o", probe, source)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build installed probe: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, custom := range []bool{false, true} {
		name := "home-relative"
		if custom {
			name = "absolute-with-spaces"
		}
		t.Run(name, func(t *testing.T) {
			checkout, err := renderTool(t, "substrate-service", "installed-svc", nil)
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			if custom {
				file := filepath.Join(checkout, "project.yaml")
				data := readText(t, file)
				data = strings.ReplaceAll(data, "~/.local/share/fleet/releases/installed-svc", filepath.ToSlash(filepath.Join(home, "release root")))
				if err = os.WriteFile(file, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err = newGoLibService(t).RenderDeploy(service.DeployOptions{TargetDir: checkout, Write: true}); err != nil {
					t.Fatal(err)
				}
			}
			var fleet struct {
				Targets []struct {
					InstallRoot string `yaml:"install_root"`
					Symlinks    []struct{ Path string }
				}
			}
			var manifest struct {
				Resources []struct {
					Config struct {
						Dir     string
						Command []string
					}
				}
			}
			if err = yaml.Unmarshal([]byte(readText(t, filepath.Join(checkout, "deploy.yaml"))), &fleet); err != nil {
				t.Fatal(err)
			}
			if err = yaml.Unmarshal([]byte(readText(t, filepath.Join(checkout, "installed-svc.cerberus.yaml"))), &manifest); err != nil {
				t.Fatal(err)
			}
			if len(fleet.Targets) != 1 || len(fleet.Targets[0].Symlinks) != 1 || len(manifest.Resources) != 1 {
				t.Fatal("expected one installed service")
			}
			config := manifest.Resources[0].Config
			if config.Dir == "" || len(config.Command) == 0 {
				t.Fatal("installed service requires explicit dir and command")
			}
			// Both installers expand ~/ against the target service account.
			expand := func(p string) string {
				if strings.HasPrefix(p, "~/") {
					return filepath.Join(home, p[2:])
				}
				return p
			}
			root := expand(fleet.Targets[0].InstallRoot)
			link := expand(fleet.Targets[0].Symlinks[0].Path)
			// Model fleet-install's version placement and stable symlink. Do not
			// create config.Dir separately: installation must already provision it.
			installed := filepath.Join(root, "v1.0.0", "installed-svc")
			for _, dir := range []string{filepath.Dir(installed), filepath.Dir(link)} {
				if err = os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(installed, binary, 0o755); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(installed, link); err != nil {
				t.Fatal(err)
			}
			if err = os.RemoveAll(checkout); err != nil {
				t.Fatal(err)
			}
			launch := func(dir string) ([]byte, error) {
				cmd := exec.Command(expand(config.Command[0]), config.Command[1:]...)
				cmd.Dir = dir
				return cmd.CombinedOutput()
			}
			if _, err = launch(checkout); err == nil {
				t.Fatal("negative control: nonexistent checkout should fail at chdir")
			}
			out, err := launch(expand(config.Dir))
			if err != nil {
				t.Fatalf("installed launch without checkout: %v\n%s", err, out)
			}
			var got struct {
				Dir  string
				Args []string
			}
			if err = json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			wantDir, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			if got.Dir != wantDir || !reflect.DeepEqual(got.Args, []string{"serve", "--managed", "--addr", "127.0.0.1:8080"}) {
				t.Fatalf("installed process: %+v; want cwd %s and managed listener args", got, wantDir)
			}
		})
	}
}
