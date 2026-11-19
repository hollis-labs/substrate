package folio_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/folio"
	"github.com/hollis-labs/tools/folio/service"
)

func renderPreset(t *testing.T, preset string, inputs map[string]any) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "app")
	svc := service.New(service.Options{BundledFS: folio.BundledPresets, BundledRoot: "presets", UserDir: t.TempDir(), FolioVersion: folio.Version, Now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)})
	if _, err := svc.New(service.NewOptions{PresetID: preset, TargetDir: target, Inputs: inputs}); err != nil {
		t.Fatalf("render %s: %v", preset, err)
	}
	return target
}

// The presets whose apps have an agent render Nanite's directory layout; the others render none.
func TestAgentDir_RenderedOnlyForAgentPresets(t *testing.T) {
	with := map[string]map[string]any{
		"app-ade":      adeInputs("ade-agents"),
		"surface-desk": {"repo_name": "desk-agents"},
	}
	for preset, inputs := range with {
		t.Run(preset, func(t *testing.T) {
			dir := renderPreset(t, preset, inputs)
			name := inputs["repo_name"].(string)
			t.Logf("generation %s: agents/assistant/{agent.md,host.yaml}, agents.dir=agents/", preset)
			configPath := "main.go"
			if preset == "app-ade" {
				configPath = filepath.Join("cmd", name, "substrate_wiring.go")
			}
			config := readFile(t, filepath.Join(dir, configPath))
			if !strings.Contains(strings.ReplaceAll(config, `\"`, `"`), `"agents":{"dir":"agents/"}`) {
				t.Errorf("%s config lacks agents.dir", preset)
			}
			if !strings.Contains(readFile(t, filepath.Join(dir, "internal/agentdir/agentdir.go")), "agents.Resolve(app.Config, configFile,") {
				t.Error("resolver does not use Substrate helper")
			}
			if preset == "surface-desk" && !strings.Contains(config, `"agent-directory"`) {
				t.Error("resolver module not started")
			}
			if preset == "app-ade" && !strings.Contains(readFile(t, filepath.Join(dir, "cmd", name, "config.go")), "agentdir.Resolve(app)") {
				t.Error("resolver not called after config")
			}
			agent := readFile(t, filepath.Join(dir, "agents/assistant/agent.md"))
			for _, want := range []string{`schema_version: "2"`, "name: assistant", "mode: durable", "definition_id: def:" + name + "-assistant"} {
				if !strings.Contains(agent, want) {
					t.Errorf("agent.md lacks %q", want)
				}
			}
			host := readFile(t, filepath.Join(dir, "agents/assistant/host.yaml"))
			for _, want := range []string{"kind: embedded", "lifetime: application", "application_id: " + name, "runtime:"} {
				if !strings.Contains(host, want) {
					t.Errorf("host.yaml lacks %q", want)
				}
			}
			if !strings.Contains(readFile(t, filepath.Join(dir, "agents/README.md")), "Nothing in the app opens these files yet") {
				t.Error("agents/README.md does not say the files are not live")
			}
			checkWithNanite(t, filepath.Join(dir, "agents/assistant"))
		})
	}
	without := map[string]map[string]any{
		"app-chat":      chatInputs("chat-agents"),
		"surface-app":   {"repo_name": "plain-agents"},
		"substrate-app": {"repo_name": "plain-substrate", "substrate_version": "v0.1.1-0.20261119145946-6a4bb696c942"},
	}
	for preset, inputs := range without {
		dir := renderPreset(t, preset, inputs)
		if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := strings.ReplaceAll(string(b), `\"`, `"`)
			if strings.Contains(text, `"agents":`) || strings.Contains(text, "agents: { dir:") {
				t.Errorf("%s rendered agents config in %s", preset, path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "agents")); !os.IsNotExist(err) {
			t.Errorf("%s rendered an agents directory", preset)
		}
	}
}

// checkWithNanite shells out to Nanite's offline embedding example, which calls agent.Open
// on the generated directory and exercises it with a scripted provider. FOLIO_NANITE names
// that binary (go install github.com/hollis-labs/nanite/examples/embed-host@3f3eaed).
// There is no Nanite import or requirement in Folio's module.
func checkWithNanite(t *testing.T, agentDir string) {
	t.Helper()
	bin := os.Getenv("FOLIO_NANITE")
	if bin == "" {
		t.Log("FOLIO_NANITE not set: the Nanite loader check is skipped")
		return
	}
	work := filepath.Join(t.TempDir(), "assistant")
	if err := os.CopyFS(work, os.DirFS(agentDir)); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--offline", "--dir", work) //nolint:gosec // the test names the offline example binary
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Nanite offline agent.Open: %v\n%s", err, out)
	}
	t.Logf("Nanite embed-host --offline --dir <generated agent>: %s", strings.TrimSpace(string(out)))
}

func TestAgentDir_GeneratedResolver(t *testing.T) {
	for _, preset := range []string{"app-ade", "surface-desk"} {
		t.Run(preset, func(t *testing.T) {
			inputs := map[string]any{"repo_name": "resolver-check"}
			if preset == "app-ade" {
				inputs = adeInputs("resolver-check")
			}
			dir := renderPreset(t, preset, inputs)
			fixture := t.TempDir()
			if err := os.CopyFS(filepath.Join(fixture, "agentdir"), os.DirFS(filepath.Join(dir, "internal/agentdir"))); err != nil {
				t.Fatal(err)
			}
			// A test fixture module, outside the repository: only Substrate, never Nanite.
			mod := fmt.Sprintf("module resolvercheck\n\ngo 1.26.11\n\nrequire github.com/hollis-labs/substrate %s\n", "v0.1.1-0.20261119145946-6a4bb696c942")
			if err := os.WriteFile(filepath.Join(fixture, "go.mod"), []byte(mod), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "test", "-mod=mod", "-v", "./...")
			cmd.Dir = fixture
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("generated resolver: %v\n%s", err, out)
			}
			t.Logf("generated resolver: %s", out)
		})
	}
}
