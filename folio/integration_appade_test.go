package folio_test

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/folio"
	"github.com/hollis-labs/tools/folio/internal/manifest"
	"github.com/hollis-labs/tools/folio/service"
)

// app-ade renders a Substrate app where a person and an agent work over the same surface: the
// agent host mounted at /api/agent, the workspace page, and a starter domain (a notes board) that
// is the person's to replace. The render tests run everywhere. TestAppADE_BuildsAndRuns builds and
// drives the binary; it is a visible SKIP unless FOLIO_SUBSTRATE_VERSION, FOLIO_ADE_AGENTHOST and
// FOLIO_ADE_PACKAGES name versions that resolve (the ADE packages are not published yet).

// Render-only fixture versions; the build test sources real ones from the environment.
func adeInputs(name string) map[string]any {
	inputs := frontendInputs(name)
	inputs["ade_agenthost"] = "v0.1.0"
	inputs["ade_core"] = "0.1.0"
	inputs["ade_agent"] = "0.1.1"
	inputs["ade_react"] = "0.1.2"
	return inputs
}

func renderAppADE(t *testing.T, svc *service.Service, inputs map[string]any) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), inputs["repo_name"].(string))
	if _, err := svc.New(service.NewOptions{PresetID: "app-ade", TargetDir: target, Inputs: inputs}); err != nil {
		t.Fatalf("service.New app-ade: %v", err)
	}
	return target
}

func TestAppADE_RendersTree(t *testing.T) {
	target := renderAppADE(t, newGoLibService(t), adeInputs("notes-app"))
	for _, p := range []string{
		"README.md", "go.mod", "cmd/notes-app/agent.go", "cmd/notes-app/prompt.go", "cmd/notes-app/serve.go",
		"cmd/notes-app/ui/app.json", "internal/settings/settings.go", "frontend/package.json",
		"frontend/src/App.tsx", "frontend/src/pages/workspace.tsx", "frontend/src/ade/setup.ts",
		"frontend/src/ade/workspace.css", "frontend/src/domain/index.ts", "frontend/src/domain/notes.ts",
		"frontend/src/domain/board.tsx", "frontend/src/domain/notes.css",
	} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
	// Exact pins from the inputs, and nothing local.
	gomod := readText(t, filepath.Join(target, "go.mod"))
	if !strings.Contains(gomod, "require github.com/hollis-labs/ade/go/agenthost v0.1.0\n") || strings.Contains(gomod, "replace") {
		t.Errorf("go.mod:\n%s", gomod)
	}
	var pkg struct{ Dependencies map[string]string }
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(target, "frontend", "package.json"))), &pkg); err != nil {
		t.Fatal(err)
	}
	for dep, want := range map[string]string{"@hollis-labs/ade-core": "0.1.0", "@hollis-labs/ade-agent": "0.1.1", "@hollis-labs/ade-react": "0.1.2", "@hollis-labs/app-shell": "0.1.0"} {
		if pkg.Dependencies[dep] != want {
			t.Errorf("package.json %s = %q, want %q", dep, pkg.Dependencies[dep], want)
		}
	}
	for dep, v := range pkg.Dependencies {
		if strings.ContainsAny(v, "^~:/") {
			t.Errorf("package.json %s = %q: not an exact registry version", dep, v)
		}
	}
	// The wiring: the agent host behind the guard, the owner from the verified principal, its
	// routes on the scope table, its model from the app's settings.
	agent := readText(t, filepath.Join(target, "cmd", "notes-app", "agent.go"))
	for _, s := range []string{`"github.com/hollis-labs/ade/go/agenthost"`, "libauth.FromContext", "p.Verified", "scopes.Route(", `"/api/agent/runs/{run}/tool-results"`, "agenthost.OpenSQLite", "System:  agentPrompt"} {
		if !strings.Contains(agent, s) {
			t.Errorf("agent.go lacks %q", s)
		}
	}
	serve := readText(t, filepath.Join(target, "cmd", "notes-app", "serve.go"))
	if !strings.Contains(serve, "mountAgent(ctx, auth.ScopeTable(app), routes, cfg.Agent)") {
		t.Errorf("serve.go does not mount the agent:\n%s", serve)
	}
	if !strings.Contains(readText(t, filepath.Join(target, "internal", "settings", "settings.go")), `fields := map[string]*string{"model": &a.Model, "base_url": &a.BaseURL, "key_file": &a.KeyFile}`) {
		t.Error("settings.go does not read the agent block")
	}
	app := readText(t, filepath.Join(target, "frontend", "src", "App.tsx"))
	if !strings.Contains(app, `import { WorkspacePage } from "./pages/workspace"`) || strings.Contains(app, "ItemsPage") {
		t.Errorf("App.tsx does not serve the workspace:\n%s", app)
	}
	// The workspace reaches the domain only through its index.
	for _, f := range []string{"pages/workspace.tsx", "ade/setup.ts"} {
		src := readText(t, filepath.Join(target, "frontend", "src", filepath.FromSlash(f)))
		if strings.Contains(src, "/domain/") || !strings.Contains(src, `from "../domain"`) {
			t.Errorf("%s reaches into the domain past index.ts", f)
		}
	}
	readme := readText(t, filepath.Join(target, "README.md"))
	if !strings.Contains(readme, "## Your domain") || !strings.Contains(readme, "frontend/src/domain/**") || !strings.Contains(readme, "cmd/notes-app/prompt.go") {
		t.Error("README.md does not say which files are the person's")
	}
	_ = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(path, ".github") {
			return err
		}
		if s := readText(t, path); strings.Contains(s, "<no value>") || strings.Contains(s, "file:") && strings.HasSuffix(path, "package.json") {
			t.Errorf("%s has unresolved template text or a local dependency", path)
		}
		return nil
	})
}

func TestAppADE_VersionsAreRequired(t *testing.T) {
	for _, key := range []string{"ade_agenthost", "ade_core", "ade_agent", "ade_react"} {
		inputs := adeInputs("notes-app")
		delete(inputs, key)
		target := filepath.Join(t.TempDir(), "notes-app")
		_, err := newGoLibService(t).New(service.NewOptions{PresetID: "app-ade", TargetDir: target, Inputs: inputs})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("without %s: want an error naming it, got %v", key, err)
		}
	}
	inputs := adeInputs("notes-app")
	inputs["ade_core"] = "^0.1.0"
	if _, err := newGoLibService(t).New(service.NewOptions{PresetID: "app-ade", TargetDir: filepath.Join(t.TempDir(), "x"), Inputs: inputs}); err == nil {
		t.Error("a version range was taken")
	}
}

// copyBundled copies the bundled presets into a directory the test can change, which is how the
// preset "updates" between generating a project and syncing it.
func copyBundled(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := fs.WalkDir(folio.BundledPresets, "presets", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fs.ReadFile(folio.BundledPresets, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func editFile(t *testing.T, path string, change func(string) string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(change(string(b))), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The point of the preset: regeneration does not overwrite authored domain work. The person edits
// the domain and the prompt; the preset then changes a shell file and the same domain files; sync
// updates the shell and leaves the person's files byte for byte.
func TestAppADE_SyncKeepsTheDomainAndUpdatesTheShell(t *testing.T) {
	root := copyBundled(t)
	svc := service.New(service.Options{
		BundledFS:    os.DirFS(root),
		BundledRoot:  "presets",
		UserDir:      t.TempDir(),
		FolioVersion: folio.Version,
		Now:          time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC),
	})
	target := renderAppADE(t, svc, adeInputs("notes-app"))

	// The person's work.
	domain := filepath.Join(target, "frontend", "src", "domain", "notes.ts")
	prompt := filepath.Join(target, "cmd", "notes-app", "prompt.go")
	editFile(t, domain, func(s string) string { return s + "\n// The person's own card rules.\nexport const LIMIT = 40\n" })
	editFile(t, prompt, func(s string) string { return strings.Replace(s, "a board of notes", "a board of recipes", 1) })
	mine := map[string]string{domain: readText(t, domain), prompt: readText(t, prompt)}
	untouchedDomain := filepath.Join(target, "frontend", "src", "domain", "board.tsx")
	mine[untouchedDomain] = readText(t, untouchedDomain)

	// A newer preset: the shell changes, and so do the domain files the person has.
	preset := filepath.Join(root, "presets", "app-ade")
	editFile(t, filepath.Join(preset, "preset.yaml"), func(s string) string { return strings.Replace(s, "version: 0.1.0", "version: 0.1.1", 1) })
	editFile(t, filepath.Join(preset, "files", "frontend", "src", "ade", "setup.ts.tmpl"), func(s string) string {
		return strings.Replace(s, `const GUIDE_KEY = "{{ .inputs.repo_name }}.guide"`, `const GUIDE_KEY = "{{ .inputs.repo_name }}.guide.v2"`, 1)
	})
	files := filepath.Join(preset, "files")
	editFile(t, filepath.Join(files, "frontend", "src", "domain", "notes.ts"), func(s string) string { return s + "\n// preset 0.1.1\n" })
	editFile(t, filepath.Join(files, "frontend", "src", "domain", "board.tsx"), func(s string) string { return s + "\n// preset 0.1.1\n" })
	editFile(t, filepath.Join(files, "cmd", "{{.inputs.repo_name}}", "prompt.go.tmpl"), func(s string) string { return s + "\n// preset 0.1.1\n" })

	res, err := svc.Sync(service.SyncOptions{TargetDir: target})
	if err != nil {
		t.Fatal(err)
	}
	if res.ToVersion != "0.1.1" {
		t.Errorf("synced to %s", res.ToVersion)
	}
	for path, before := range mine {
		if got := readText(t, path); got != before {
			t.Errorf("%s was rewritten by sync", path)
		}
	}
	if setup := readText(t, filepath.Join(target, "frontend", "src", "ade", "setup.ts")); !strings.Contains(setup, `"notes-app.guide.v2"`) {
		t.Error("the shell file was not updated")
	}
	actions := map[string]service.SyncAction{}
	for _, f := range res.Files {
		actions[f.Path] = f.Action
	}
	for p, want := range map[string]service.SyncAction{
		"frontend/src/domain/notes.ts":  service.SyncSkipped,
		"frontend/src/domain/board.tsx": service.SyncSkipped,
		"cmd/notes-app/prompt.go":       service.SyncSkipped,
		"frontend/src/ade/setup.ts":     service.SyncUpdated,
	} {
		if actions[p] != want {
			t.Errorf("%s: %s, want %s", p, actions[p], want)
		}
	}
	if len(res.Conflicts()) != 0 {
		t.Errorf("conflicts: %+v", res.Conflicts())
	}
	m, err := manifest.Read(target)
	if err != nil || m.Presets[len(m.Presets)-1].Version != "0.1.1" {
		t.Errorf("manifest presets: %+v %v", m.Presets, err)
	}
}

// TestAppADE_BuildsAndRuns builds the render against the real framework and the published ADE
// packages, and drives the binary: signed out the agent routes are refused, signed in the panel
// learns no model is connected.
func TestAppADE_BuildsAndRuns(t *testing.T) {
	version, host, pkgs := os.Getenv("FOLIO_SUBSTRATE_VERSION"), os.Getenv("FOLIO_ADE_AGENTHOST"), os.Getenv("FOLIO_ADE_PACKAGES")
	if version == "" || host == "" || pkgs == "" {
		if os.Getenv("FOLIO_REQUIRE_TOOLS") == "1" {
			t.Fatal("FOLIO_SUBSTRATE_VERSION, FOLIO_ADE_AGENTHOST and FOLIO_ADE_PACKAGES are required")
		}
		t.Skip("SKIPPED, not passed: FOLIO_SUBSTRATE_VERSION, FOLIO_ADE_AGENTHOST or FOLIO_ADE_PACKAGES is unset")
	}
	if !needTool(t, "go") || !needTool(t, "npm") {
		return
	}
	if out, err := exec.Command("go", "mod", "download", "-json", "github.com/hollis-labs/ade/go/agenthost@"+host).CombinedOutput(); err != nil {
		t.Skipf("SKIPPED, not passed: go/agenthost@%s does not resolve: %s", host, out)
	}
	if out, err := exec.Command("npm", "view", "@hollis-labs/ade-core@"+pkgs, "version").CombinedOutput(); err != nil {
		t.Skipf("SKIPPED, not passed: @hollis-labs/ade-core@%s does not resolve: %s", pkgs, out)
	}
	kits := bomKits(t, version)
	inputs := map[string]any{
		"repo_name": "ade-smoke", "substrate_version": version, "http_port": "18219",
		"ade_agenthost": host, "ade_core": pkgs, "ade_agent": pkgs, "ade_react": pkgs,
	}
	for _, kit := range []string{"app-shell", "design-app-runtime", "design-bindings", "design-components", "design-tokens", "kit-dashboard", "plugin-host-ui"} {
		inputs["kit_"+strings.ReplaceAll(strings.TrimPrefix(kit, "kit-"), "-", "_")] = kits[kit]
	}
	dir := renderAppADE(t, newGoLibService(t), inputs)
	mustRun(t, dir, "go", "mod", "tidy")
	mustRun(t, dir, "go", "run", "github.com/hollis-labs/substrate/cmd/substrate", "pins", "sync", "--app", ".")
	mustRun(t, dir, "go", "mod", "tidy")
	fe := filepath.Join(dir, "frontend")
	mustRun(t, fe, "npm", "install", "--no-audit", "--no-fund")
	mustRun(t, fe, "npm", "run", "typecheck")
	mustRun(t, fe, "npm", "run", "lint")
	mustRun(t, fe, "npm", "run", "build")
	mustRun(t, dir, "go", "vet", "./...")
	mustRun(t, dir, "go", "test", "-race", "./...")
	checkTransportGate(t, dir)
	bin := filepath.Join(dir, "ade-smoke")
	mustRun(t, dir, "go", "build", "-o", bin, "./cmd/ade-smoke")
	driveSubstrateBinary(t, bin, "ade-smoke", "18219", func(owner *http.Client, base string) { checkAgentRoutes(t, owner, base) })
}

func checkAgentRoutes(t *testing.T, owner *http.Client, base string) {
	t.Helper()
	call := func(client *http.Client, method, path, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "test")
		req.Header.Set("Origin", base)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}
	if code, _ := call(&http.Client{Timeout: 10 * time.Second}, "GET", "/api/agent", ""); code != http.StatusUnauthorized {
		t.Errorf("signed out GET /api/agent = %d, want 401", code)
	}
	if code, body := call(owner, "GET", "/api/agent", ""); code != 200 || !strings.Contains(body, `"ready":false`) {
		t.Errorf("GET /api/agent = %d %s", code, body)
	}
	if code, body := call(owner, "GET", "/api/agent/thread", ""); code != 200 || !strings.Contains(body, `"thread_id"`) {
		t.Errorf("GET /api/agent/thread = %d %s", code, body)
	}
	if code, body := call(owner, "POST", "/api/agent/turns", `{"text":"hi","client_id":"c1","tools":[]}`); code != http.StatusServiceUnavailable {
		t.Errorf("a turn with no model = %d %s, want 503", code, body)
	}
}
