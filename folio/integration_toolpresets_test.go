package folio_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/folio/service"
)

// substrate-cli and substrate-service render class 2 tools (project/1 class model, RC1): project.yaml,
// deploy.yaml, a Cerberus manifest for services only (Option B), the class 2 CI and, for a service, the
// loopback-dev auth guard. The render tests run everywhere; TestToolPresets_BuildAndCheck builds the rendered
// trees against the real framework and is a visible SKIP unless FOLIO_SUBSTRATE_VERSION is set.

func renderTool(t *testing.T, preset, name string, extra map[string]any) (string, error) {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	inputs := map[string]any{"repo_name": name, "substrate_version": "v0.1.1-0.20261112164520-da9f59bb59b7"}
	for k, v := range extra {
		inputs[k] = v
	}
	_, err := newGoLibService(t).New(service.NewOptions{PresetID: preset, TargetDir: target, Inputs: inputs})
	return target, err
}

func TestToolPresets_RenderTrees(t *testing.T) {
	common := []string{
		".folio.yaml", ".gitignore", ".github/workflows/check.yml", ".github/workflows/build.yml", "AGENTS.md", "README.md",
		"deploy.yaml", "project.yaml", "go.mod", "scan-public.allow", "internal/descriptor/descriptor.go", "internal/descriptor/descriptor_test.go",
	}
	cases := []struct {
		preset, name string
		want, absent []string
	}{
		{"substrate-cli", "tool-one", append([]string{"cmd/tool-one/main.go", "cmd/tool-one/substrate_wiring.go"}, common...),
			[]string{"tool-one.cerberus.yaml", "internal/authmode/authmode.go", "cmd/tool-one/serve.go"}},
		{"substrate-service", "svc-one", append([]string{"svc-one.cerberus.yaml", "internal/authmode/authmode.go", "cmd/svc-one/serve.go", "cmd/svc-one/serve_test.go"}, common...),
			nil},
	}
	for _, c := range cases {
		t.Run(c.preset, func(t *testing.T) {
			dir, err := renderTool(t, c.preset, c.name, nil)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, p := range c.want {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
					t.Errorf("expected %s: %v", p, err)
				}
			}
			for _, p := range c.absent {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
					t.Errorf("%s must not be rendered for %s", p, c.preset)
				}
			}
			project := readText(t, filepath.Join(dir, "project.yaml"))
			for _, s := range []string{"schema: project/1", "id: " + c.name, "class: 2", "owner: user:owner"} {
				if !strings.Contains(project, s) {
					t.Errorf("project.yaml missing %q", s)
				}
			}
			check := readText(t, filepath.Join(dir, ".github", "workflows", "check.yml"))
			for _, s := range []string{"go vet ./...", "pins check --app .", "go build ./...", "go test -race ./...", "scan-public --ref HEAD", "actions/checkout@v5", "actions/setup-go@v6"} {
				if !strings.Contains(check, s) {
					t.Errorf("check.yml missing %q", s)
				}
			}
			if strings.Contains(check, "golangci") {
				t.Errorf("class 2 check.yml must not run lint (class bar: vet, test, build, scan-public, pins check)")
			}
			if !strings.Contains(readText(t, filepath.Join(dir, ".github", "workflows", "build.yml")), "go-app-build.yml@") {
				t.Errorf("build.yml must call the shared reusable workflow")
			}
		})
	}
}

func TestToolPresets_ServiceDescriptorsAgree(t *testing.T) {
	dir, err := renderTool(t, "substrate-service", "svc-two", map[string]any{"http_port": "18222", "auth": "loopback_open"})
	if err != nil {
		t.Fatal(err)
	}
	c := readText(t, filepath.Join(dir, "svc-two.cerberus.yaml"))
	for _, s := range []string{"kind: cerberus-project/v1", "owner: svc-two", "id: svc-two", "run_from: workspace", "supervisor: auto", "port: 18222", "url: http://127.0.0.1:18222/healthz", `- "~/.local/bin/svc-two"`} {
		if !strings.Contains(c, s) {
			t.Errorf("cerberus manifest missing %q", s)
		}
	}
	for _, s := range []string{"build_strategy:", "artifact_path:"} {
		if strings.Contains(c, s) {
			t.Errorf("Option B: cerberus manifest must not contain %q", s)
		}
	}
	d := readText(t, filepath.Join(dir, "deploy.yaml"))
	for _, s := range []string{"schema: fleet-deploy/v1", `path: "~/.local/bin/svc-two"`, "via: cerberus", "resource: svc-two", "http://127.0.0.1:18222/healthz"} {
		if !strings.Contains(d, s) {
			t.Errorf("deploy.yaml missing %q", s)
		}
	}
	if !strings.Contains(readText(t, filepath.Join(dir, "project.yaml")), "auth: loopback_open") {
		t.Errorf("project.yaml must declare the chosen auth mode")
	}
	if !strings.Contains(readText(t, filepath.Join(dir, "internal", "authmode", "authmode.go")), "const Default = LoopbackOpen") {
		t.Errorf("authmode.Default must match the declared mode")
	}
}

func TestToolPresets_Refusals(t *testing.T) {
	for _, preset := range []string{"substrate-cli", "substrate-service"} {
		target := filepath.Join(t.TempDir(), "x")
		// substrate_version has no default.
		if _, err := newGoLibService(t).New(service.NewOptions{PresetID: preset, TargetDir: target, Inputs: map[string]any{"repo_name": "x"}}); err == nil {
			t.Errorf("%s: rendered without substrate_version", preset)
		}
	}
	if _, err := renderTool(t, "substrate-service", "svc-bad", map[string]any{"auth": "open"}); err == nil {
		t.Error("an unknown auth mode was accepted")
	}
	if _, err := renderTool(t, "substrate-service", "mcp-bad", map[string]any{"with_mcp": true, "auth": "loopback_open"}); err == nil || !strings.Contains(err.Error(), "with_mcp requires auth=enforce") {
		t.Fatalf("contradictory MCP auth inputs: %v", err)
	}
	for _, port := range []string{"0", "00", "99999", "65536", "100000", "80a"} {
		if _, err := renderTool(t, "substrate-service", "svc-port", map[string]any{"http_port": port}); err == nil {
			t.Errorf("http_port %q was accepted", port)
		}
	}
	for _, port := range []string{"1", "8080", "65535"} {
		if _, err := renderTool(t, "substrate-service", "svc-port", map[string]any{"http_port": port}); err != nil {
			t.Errorf("http_port %q refused: %v", port, err)
		}
	}
}

func TestToolPresets_BuildAndCheck(t *testing.T) {
	version := os.Getenv("FOLIO_SUBSTRATE_VERSION")
	if version == "" {
		if os.Getenv("FOLIO_REQUIRE_TOOLS") == "1" {
			t.Fatal("FOLIO_SUBSTRATE_VERSION is required (FOLIO_REQUIRE_TOOLS=1)")
		}
		t.Skip("SKIPPED, not passed: FOLIO_SUBSTRATE_VERSION names no framework version to build against")
	}
	if !needTool(t, "go") {
		return
	}
	for _, c := range []struct {
		preset, name string
		mcp          bool
	}{
		{"substrate-cli", "hello-cli", false}, {"substrate-service", "hello-svc", false},
		{"substrate-cli", "mcp-cli", true}, {"substrate-service", "mcp-svc", true},
	} {
		preset, name := c.preset, c.name
		t.Run(name, func(t *testing.T) {
			dir, err := renderTool(t, preset, name, map[string]any{"substrate_version": version, "with_mcp": c.mcp})
			if err != nil {
				t.Fatal(err)
			}
			sub := func(args ...string) string {
				return mustRun(t, dir, "go", append([]string{"run", "github.com/hollis-labs/substrate/cmd/substrate"}, args...)...)
			}
			mustRun(t, dir, "go", "mod", "tidy")
			sub("pins", "sync", "--app", ".")
			mustRun(t, dir, "go", "mod", "tidy")
			sub("pins", "check", "--app", ".")
			wiring := filepath.Join(dir, "cmd", name, "substrate_wiring.go")
			rendered := readText(t, wiring)
			sub("generate", "-f", "cmd/"+name+"/substrate.yaml")
			if got := readText(t, wiring); got != rendered {
				t.Fatalf("rendered substrate_wiring.go differs from `substrate generate`")
			}
			if out := mustRun(t, dir, "gofmt", "-l", "."); strings.TrimSpace(out) != "" {
				t.Errorf("gofmt -l:\n%s", out)
			}
			mustRun(t, dir, "go", "vet", "./...")
			mustRun(t, dir, "go", "build", "./...")
			// Includes the descriptor and drift tests and, for a service, the loopback refusal.
			mustRun(t, dir, "go", "test", "-race", "-count=1", "./...")
			if _, err := exec.LookPath("cerberus"); err == nil && preset == "substrate-service" {
				mustRun(t, dir, "cerberus", "validate", name+".cerberus.yaml")
			}
		})
	}
}
