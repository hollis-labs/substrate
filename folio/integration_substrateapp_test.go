package folio_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/tools/folio/service"
)

// substrate-app renders a Substrate app-host: manifest, generated wiring, boot, identity with an
// offline-created owner, SQLite through the database module, scoped routes, CI and a deploy
// descriptor. The first tests render and assert the tree. TestSubstrateApp_BuildsAndRuns renders,
// builds against the real framework and drives the real binary; it needs network and Go, so it is a
// visible SKIP (a failure under FOLIO_REQUIRE_TOOLS=1) unless FOLIO_SUBSTRATE_VERSION names the
// framework version to build against (a tag or pseudo-version; there is no default on purpose).
//
// The port is FOLIO_TEST_PORT, default 18210.

func renderSubstrateApp(t *testing.T, name string, extra map[string]any) (string, error) {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	inputs := map[string]any{
		"repo_name":         name,
		"substrate_version": "v0.1.1-0.20261112164520-da9f59bb59b7",
	}
	for k, v := range extra {
		inputs[k] = v
	}
	_, err := newGoLibService(t).New(service.NewOptions{
		PresetID:  "substrate-app",
		TargetDir: target,
		Inputs:    inputs,
	})
	return target, err
}

func TestSubstrateApp_RendersTree(t *testing.T) {
	target, err := renderSubstrateApp(t, "myapp", nil)
	if err != nil {
		t.Fatalf("service.New substrate-app: %v", err)
	}
	want := []string{
		".folio.yaml", ".gitignore", ".golangci.yml", ".github/workflows/check.yml", ".github/workflows/build.yml",
		"AGENTS.md", "CHANGELOG.md", "LICENSE", "README.md", "deploy.yaml", "go.mod", "scan-public.allow",
		"cmd/myapp/main.go", "cmd/myapp/config.go", "cmd/myapp/serve.go", "cmd/myapp/substrate.yaml", "cmd/myapp/substrate_wiring.go", "cmd/myapp/plan_test.go",
		"docs/identity-bootstrap.md",
		"internal/api/api.go", "internal/api/api_test.go", "internal/service/service.go", ".golangci.transport.yml", "docs/transport-boundary.md", "internal/hostguard/hostguard.go", "internal/hostguard/hostguard_test.go",
		"internal/settings/settings.go", "internal/settings/settings_test.go", "internal/webui/webui.go", "internal/webui/dist/.gitkeep",
		"internal/storage/migrations.go", "internal/storage/migrations/sqlite/00001_init.sql",
	}
	for _, p := range want {
		if _, statErr := os.Stat(filepath.Join(target, filepath.FromSlash(p))); statErr != nil {
			t.Errorf("expected %s, missing: %v", p, statErr)
		}
	}
	// The Substrate CI replaces go-baseline's; no go-webui or Chimera material at all.
	check := readText(t, filepath.Join(target, ".github", "workflows", "check.yml"))
	for _, s := range []string{"substrate/cmd/substrate pins check --app .", "scan-public --ref HEAD", "version: v2.11.4", "actions/checkout@v5", "actions/setup-go@v6", "go-version-file: go.mod", "go test -race ./..."} {
		if !strings.Contains(check, s) {
			t.Errorf("check.yml missing %q", s)
		}
	}
	if strings.Contains(check, "setup-node") {
		t.Error("the backend preset renders no frontend step")
	}
	_ = filepath.WalkDir(target, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || strings.HasSuffix(path, ".folio.yaml") {
			return walkErr
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read %s: %v", path, readErr)
			return nil
		}
		rel, _ := filepath.Rel(target, path)
		s := string(b)
		if strings.Contains(s, "<no value>") || (!strings.HasSuffix(rel, "check.yml") && !strings.HasSuffix(rel, "build.yml") && strings.Contains(s, "{{")) {
			t.Errorf("%s has unresolved template text", rel)
		}
		for _, banned := range []string{"chimera", "go-webui", "myapp.example.com"} {
			if strings.Contains(strings.ToLower(s), banned) {
				t.Errorf("%s mentions %q", rel, banned)
			}
		}
		return nil
	})
	gomod := readText(t, filepath.Join(target, "go.mod"))
	for _, s := range []string{"module github.com/hollis-labs/myapp\n", "\ngo 1.26.11\n", "github.com/hollis-labs/substrate v0.1.1-0.20261112164520-da9f59bb59b7"} {
		if !strings.Contains(gomod, s) {
			t.Errorf("go.mod missing %q:\n%s", s, gomod)
		}
	}
	manifest := readText(t, filepath.Join(target, "cmd", "myapp", "substrate.yaml"))
	for _, s := range []string{"app: myapp", "variant: app-host", "auth: { mode: enforce }", `"127.0.0.1:8080"`, "identity"} {
		if !strings.Contains(manifest, s) {
			t.Errorf("substrate.yaml missing %q", s)
		}
	}
	if strings.Contains(manifest, "setup: http") {
		t.Error("the manifest must not open first-run setup over HTTP")
	}
}

// The framework version has no default: a render without it fails loudly instead of using a stale one.
func TestSubstrateApp_SubstrateVersionIsRequired(t *testing.T) {
	target := filepath.Join(t.TempDir(), "myapp")
	_, err := newGoLibService(t).New(service.NewOptions{
		PresetID:  "substrate-app",
		TargetDir: target,
		Inputs:    map[string]any{"repo_name": "myapp"},
	})
	if err == nil || !strings.Contains(err.Error(), "substrate_version") {
		t.Fatalf("want an error naming substrate_version, got %v", err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Error("a failed render must write nothing")
	}
}

func TestSubstrateApp_RejectsBadInputs(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"port":    {"http_port": "80a"},
		"version": {"substrate_version": "main"},
		"ref":     {"build_workflow_ref": "main"},
	} {
		if _, err := renderSubstrateApp(t, "myapp", extra); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
	if _, err := renderSubstrateApp(t, "My_App", nil); err == nil {
		t.Error("repo_name with capitals or underscores must be refused")
	}
}

func TestSubstrateApp_BuildsAndRuns(t *testing.T) {
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
	port := os.Getenv("FOLIO_TEST_PORT")
	if port == "" {
		port = "18210"
	}
	// An app name that sorts before the first module key ("auth") exercises the wiring's key order.
	for _, name := range []string{"myapp", "app-one"} {
		t.Run(name, func(t *testing.T) {
			dir, err := renderSubstrateApp(t, name, map[string]any{"substrate_version": version, "http_port": port})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			substrate := []string{"run", "github.com/hollis-labs/substrate/cmd/substrate"}
			sub := func(args ...string) string {
				return mustRun(t, dir, "go", append(append([]string{}, substrate...), args...)...)
			}
			mustRun(t, dir, "go", "mod", "tidy")
			sub("pins", "sync", "--app", ".")
			mustRun(t, dir, "go", "mod", "tidy")
			sub("pins", "check", "--app", ".")
			sub("validate", "-f", "cmd/"+name+"/substrate.yaml")

			// The committed wiring is exactly what the generator writes.
			wiring := filepath.Join(dir, "cmd", name, "substrate_wiring.go")
			rendered := readText(t, wiring)
			sub("generate", "-f", "cmd/"+name+"/substrate.yaml")
			if got := readText(t, wiring); got != rendered {
				t.Fatalf("rendered substrate_wiring.go differs from `substrate generate`:\n--- rendered\n%s\n--- generated\n%s", rendered, got)
			}

			if out := mustRun(t, dir, "gofmt", "-l", "."); strings.TrimSpace(out) != "" {
				t.Errorf("gofmt -l:\n%s", out)
			}
			mustRun(t, dir, "go", "vet", "./...")
			mustRun(t, dir, "go", "test", "-race", "./...")
			bin := filepath.Join(dir, name)
			mustRun(t, dir, "go", "build", "-o", bin, "./cmd/"+name)
			checkTransportGate(t, dir)
			checkPrintOnlyCommands(t, bin, name)
			driveSubstrateBinary(t, bin, name, port)
		})
	}
}

// driveSubstrateBinary runs the real binary on a throwaway data directory: offline owner creation,
// setup closed over HTTP, sign-in, a write, a read, refused requests, SIGTERM.
func driveSubstrateBinary(t *testing.T, bin, name, port string) {
	t.Helper()
	data := t.TempDir()
	cfg := filepath.Join(data, "override.yaml") // an empty override: the manifest's defaults rule
	if err := os.WriteFile(cfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prefix := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	env := append(os.Environ(), prefix+"_DATA_DIR="+data, "SUBSTRATE_CONFIG="+cfg)
	cred := randomHex(t) // generated per run: no literal credential in the tree

	setup := exec.Command(bin, "identity", "setup", "--username", "owner", "--display-name", "Owner")
	setup.Env, setup.Stdin = env, strings.NewReader(cred+"\n")
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("identity setup: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	srv := exec.CommandContext(ctx, bin, "serve")
	srv.Env = env
	stderr, _ := srv.StderrPipe()
	var logBuf bytes.Buffer
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(&logBuf, bufio.NewReader(stderr)) }()
	stopped := false
	defer func() {
		if !stopped {
			_ = srv.Process.Kill()
			_ = srv.Wait()
		}
	}()
	base := "http://127.0.0.1:" + port
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	var up bool
	for i := 0; i < 100 && !up; i++ {
		if resp, err := client.Get(base + "/healthz"); err == nil {
			_ = resp.Body.Close()
			up = resp.StatusCode == http.StatusOK
		}
		if !up {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !up {
		t.Fatalf("service did not come up on %s:\n%s", base, logBuf.String())
	}
	do := func(method, path, body string, headers map[string]string) (int, string) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if h := headers["Host"]; h != "" {
			req.Host = h
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	csrf := map[string]string{"Content-Type": "application/json", "X-Requested-With": "test", "Origin": base}

	if code, body := do("GET", "/api/setup", "", nil); code != 200 || !strings.Contains(body, `"setup_required":false`) {
		t.Errorf("GET /api/setup = %d %s, want setup_required false", code, body)
	}
	if code, _ := do("POST", "/api/setup", "{}", csrf); code != http.StatusForbidden {
		t.Errorf("POST /api/setup = %d, want 403 (setup is offline)", code)
	}
	if code, _ := do("GET", "/api/items", "", nil); code != http.StatusUnauthorized {
		t.Errorf("signed-out GET /api/items = %d, want 401", code)
	}
	if code, body := do("POST", "/api/login", `{"username":"owner","password":"`+cred+`"}`, csrf); code != 200 {
		t.Fatalf("login = %d %s", code, body)
	}
	if code, _ := do("POST", "/api/items", `{"title":"first"}`, map[string]string{"Content-Type": "application/json"}); code != http.StatusForbidden {
		t.Errorf("write without the CSRF header = %d, want 403", code)
	}
	if code, body := do("POST", "/api/items", `{"title":"first"}`, csrf); code != http.StatusCreated {
		t.Fatalf("write = %d %s", code, body)
	}
	code, body := do("GET", "/api/items", "", nil)
	var list struct {
		Items []struct{ Title string } `json:"items"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &list) != nil || len(list.Items) != 1 || list.Items[0].Title != "first" {
		t.Errorf("read = %d %s, want the one item", code, body)
	}
	if code, _ := do("GET", "/healthz", "", map[string]string{"Host": "evil.example"}); code != http.StatusForbidden {
		t.Errorf("unknown Host = %d, want 403", code)
	}

	if err := srv.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Wait() }()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Errorf("exit after SIGTERM: %v\n%s", err, logBuf.String())
		}
	case <-time.After(20 * time.Second):
		t.Errorf("no exit within 20s of SIGTERM\n%s", logBuf.String())
	}
	for _, f := range []string{name + ".db", "identity-signing.key"} {
		if _, err := os.Stat(filepath.Join(data, f)); err != nil {
			t.Errorf("data directory lacks %s: %v", f, err)
		}
	}
}

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// A command that only prints starts no module and creates no file or directory: run with an empty
// HOME and no data directory, the binary leaves HOME empty and exits as a CLI should.
func checkPrintOnlyCommands(t *testing.T, bin, name string) {
	t.Helper()
	home := t.TempDir()
	prefix := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_") || strings.HasPrefix(kv, prefix+"_DATA_DIR=") || strings.HasPrefix(kv, "SUBSTRATE_CONFIG=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home)
	for _, c := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"--help"}, true},
		{[]string{"help"}, true},
		{[]string{"version"}, true},
		{[]string{"identity", "--help"}, true},
		{nil, true}, // bare: prints help, never serves
		{[]string{"nope"}, false},
		{[]string{"--no-such-flag"}, false},
		{[]string{"serve", "--no-such-flag"}, false},
		{[]string{"serve", "extra"}, false},
	} {
		cmd := exec.Command(bin, c.args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if (err == nil) != c.ok {
			t.Errorf("%s %v: err=%v, want ok=%v\n%s", name, c.args, err, c.ok, out)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("print-only commands created files in HOME: %v", names)
	}
}

// bomKits reads the pinned kit versions from the bom.yaml of the Substrate version under test, so the
// kit inputs (which have no default) come from the framework and not from a list kept here.
func bomKits(t *testing.T, version string) map[string]string {
	t.Helper()
	out, err := exec.Command("go", "mod", "download", "-json", "github.com/hollis-labs/substrate@"+version).Output()
	if err != nil {
		t.Fatalf("go mod download substrate@%s: %v", version, err)
	}
	var info struct{ Dir string }
	if err := json.Unmarshal(out, &info); err != nil || info.Dir == "" {
		t.Fatalf("go mod download: no module directory: %v", err)
	}
	bom := readText(t, filepath.Join(info.Dir, "bom", "bom.yaml"))
	kits := map[string]string{}
	re := regexp.MustCompile(`(?m)^\s+"@hollis-labs/([a-z-]+)":\s+([0-9]+\.[0-9]+\.[0-9]+)\s*(?:#.*)?$`)
	for _, m := range re.FindAllStringSubmatch(bom, -1) {
		kits[m[1]] = m[2]
	}
	for _, k := range []string{"app-shell", "design-app-runtime", "design-bindings", "design-components", "design-tokens", "kit-dashboard"} {
		if kits[k] == "" {
			t.Fatalf("bom.yaml pins no @hollis-labs/%s", k)
		}
	}
	return kits
}

func renderSubstrateWebapp(t *testing.T, name, version string, kits map[string]string, extra map[string]any) (string, error) {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	inputs := map[string]any{
		"repo_name":              name,
		"substrate_version":      version,
		"kit_app_shell":          kits["app-shell"],
		"kit_design_app_runtime": kits["design-app-runtime"],
		"kit_design_bindings":    kits["design-bindings"],
		"kit_design_components":  kits["design-components"],
		"kit_design_tokens":      kits["design-tokens"],
		"kit_dashboard":          kits["kit-dashboard"],
	}
	for k, v := range extra {
		inputs[k] = v
	}
	_, err := newGoLibService(t).New(service.NewOptions{PresetID: "substrate-webapp", TargetDir: target, Inputs: inputs})
	return target, err
}

func TestSubstrateWebapp_RendersTree(t *testing.T) {
	kits := map[string]string{"app-shell": "0.1.0", "design-app-runtime": "0.5.0", "design-bindings": "0.2.0", "design-components": "0.5.1", "design-tokens": "0.5.0", "kit-dashboard": "0.6.0"}
	target, err := renderSubstrateWebapp(t, "webby", "v0.1.1-0.20261112164520-da9f59bb59b7", kits, nil)
	if err != nil {
		t.Fatalf("service.New substrate-webapp: %v", err)
	}
	for _, p := range []string{
		"frontend/package.json", "frontend/index.html", "frontend/src/App.tsx", "frontend/src/main.tsx", "frontend/src/manifest.ts",
		"frontend/src/identity/entry.tsx", "frontend/src/pages/items.tsx", "cmd/webby/ui/app.json", "Makefile", "internal/webui/dist/.gitkeep",
	} {
		if _, statErr := os.Stat(filepath.Join(target, filepath.FromSlash(p))); statErr != nil {
			t.Errorf("expected %s, missing: %v", p, statErr)
		}
	}
	pkg := readText(t, filepath.Join(target, "frontend", "package.json"))
	var parsed struct{ Dependencies, DevDependencies map[string]string }
	if jsonErr := json.Unmarshal([]byte(pkg), &parsed); jsonErr != nil {
		t.Fatalf("package.json is not JSON: %v\n%s", jsonErr, pkg)
	}
	for name, v := range kits {
		if got := parsed.Dependencies["@hollis-labs/"+name]; got != v {
			t.Errorf("@hollis-labs/%s = %q, want the input %q", name, got, v)
		}
	}
	for dep, v := range parsed.Dependencies {
		if strings.ContainsAny(v, "^~*><") || v == "latest" {
			t.Errorf("dependency %s is not an exact version: %q", dep, v)
		}
	}
	manifest := readText(t, filepath.Join(target, "cmd", "webby", "substrate.yaml"))
	if !strings.Contains(manifest, "frontend: { contributions: ui/app.json }") {
		t.Errorf("manifest does not name the contributions file:\n%s", manifest)
	}
	check := readText(t, filepath.Join(target, ".github", "workflows", "check.yml"))
	if !strings.Contains(check, "npm run typecheck") || !strings.Contains(check, "npm run build") {
		t.Error("check.yml lacks the frontend steps")
	}
	// The rendered check fails with the instruction when the lock file is missing, and passes with it.
	const lockMsg = "frontend/package-lock.json is missing: run npm install in frontend/ and commit package-lock.json"
	if !strings.Contains(check, lockMsg) {
		t.Fatal("check.yml lacks the lock-file instruction")
	}
	step := check[strings.Index(check, "Frontend lock file is committed"):]
	step = step[strings.Index(step, "run: |\n")+len("run: |\n"):]
	script := strings.TrimSpace(strings.SplitN(step, "\n      #", 2)[0])
	if !strings.Contains(script, "package-lock.json") || strings.Contains(script, "name:") {
		t.Fatalf("could not extract the lock-file step:\n%s", script)
	}
	if out, runErr := run(t, filepath.Join(target, "frontend"), "sh", "-c", script); runErr == nil || !strings.Contains(out, lockMsg) {
		t.Errorf("without a lock file the step must fail with the instruction: err=%v out=%q", runErr, out)
	}
	if wErr := os.WriteFile(filepath.Join(target, "frontend", "package-lock.json"), []byte("{}\n"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if out, runErr := run(t, filepath.Join(target, "frontend"), "sh", "-c", script); runErr != nil {
		t.Errorf("with a lock file the step must pass: %v %s", runErr, out)
	}
	// A backend-only render has none of this.
	plain, err := renderSubstrateApp(t, "plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readText(t, filepath.Join(plain, "cmd", "plain", "substrate.yaml")), "frontend") || strings.Contains(readText(t, filepath.Join(plain, ".github", "workflows", "check.yml")), "npm") {
		t.Error("substrate-app must not render frontend steps")
	}
}

func TestSubstrateWebapp_KitInputsAreRequired(t *testing.T) {
	kits := map[string]string{"app-shell": "0.1.0", "design-app-runtime": "0.5.0", "design-bindings": "0.2.0", "design-components": "0.5.1", "design-tokens": "0.5.0", "kit-dashboard": "0.6.0"}
	for _, drop := range []string{"kit_app_shell", "kit_dashboard"} {
		target := filepath.Join(t.TempDir(), "webby")
		inputs := map[string]any{
			"repo_name": "webby", "substrate_version": "v0.1.1-0.20261112164520-da9f59bb59b7",
			"kit_app_shell": kits["app-shell"], "kit_design_app_runtime": kits["design-app-runtime"], "kit_design_bindings": kits["design-bindings"],
			"kit_design_components": kits["design-components"], "kit_design_tokens": kits["design-tokens"], "kit_dashboard": kits["kit-dashboard"],
		}
		delete(inputs, drop)
		_, err := newGoLibService(t).New(service.NewOptions{PresetID: "substrate-webapp", TargetDir: target, Inputs: inputs})
		if err == nil || !strings.Contains(err.Error(), drop) {
			t.Errorf("without %s: want an error naming it, got %v", drop, err)
		}
	}
}

// TestSubstrateWebapp_BuildsAndServesUI renders the app with a UI against the real framework and the
// kit versions from its bom.yaml, then runs npm install, typecheck, lint and build, builds the Go
// binary (which embeds the built UI) and checks the binary serves the SPA. It needs network, Go and
// Node; a missing tool is a visible SKIP (a failure under FOLIO_REQUIRE_TOOLS=1).
func TestSubstrateWebapp_BuildsAndServesUI(t *testing.T) {
	version := os.Getenv("FOLIO_SUBSTRATE_VERSION")
	if version == "" {
		if os.Getenv("FOLIO_REQUIRE_TOOLS") == "1" {
			t.Fatal("FOLIO_SUBSTRATE_VERSION is required (FOLIO_REQUIRE_TOOLS=1)")
		}
		t.Skip("SKIPPED, not passed: FOLIO_SUBSTRATE_VERSION names no framework version to build against")
	}
	if !needTool(t, "go") || !needTool(t, "npm") {
		return
	}
	port := os.Getenv("FOLIO_TEST_PORT")
	if port == "" {
		port = "18210"
	}
	kits := bomKits(t, version)
	dir, err := renderSubstrateWebapp(t, "webby", version, kits, map[string]any{"http_port": port})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustRun(t, dir, "go", "mod", "tidy")
	mustRun(t, dir, "go", "run", "github.com/hollis-labs/substrate/cmd/substrate", "pins", "sync", "--app", ".")
	mustRun(t, dir, "go", "mod", "tidy")
	mustRun(t, dir, "go", "run", "github.com/hollis-labs/substrate/cmd/substrate", "pins", "check", "--app", ".")
	wiring := filepath.Join(dir, "cmd", "webby", "substrate_wiring.go")
	rendered := readText(t, wiring)
	mustRun(t, dir, "go", "run", "github.com/hollis-labs/substrate/cmd/substrate", "generate", "-f", "cmd/webby/substrate.yaml")
	if got := readText(t, wiring); got != rendered {
		t.Fatalf("rendered substrate_wiring.go differs from `substrate generate`")
	}
	fe := filepath.Join(dir, "frontend")
	mustRun(t, fe, "npm", "install", "--no-audit", "--no-fund")
	mustRun(t, fe, "npm", "run", "typecheck")
	mustRun(t, fe, "npm", "run", "lint")
	mustRun(t, fe, "npm", "run", "build")
	mustRun(t, dir, "go", "vet", "./...")
	mustRun(t, dir, "go", "test", "-race", "./...")
	bin := filepath.Join(dir, "webby")
	mustRun(t, dir, "go", "build", "-o", bin, "./cmd/webby")
	checkTransportGate(t, dir)
	checkPrintOnlyCommands(t, bin, "webby")
	serveAndFetchSPA(t, bin, "webby", port)
}

// serveAndFetchSPA starts the binary on a throwaway data directory and checks it serves the built SPA
// at the root (the embedded index, not the empty embed) and that an unknown Host is refused.
func serveAndFetchSPA(t *testing.T, bin, name, port string) {
	t.Helper()
	data := t.TempDir()
	cfg := filepath.Join(data, "override.yaml")
	if err := os.WriteFile(cfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "WEBBY_DATA_DIR="+data, "SUBSTRATE_CONFIG="+cfg)
	setup := exec.Command(bin, "identity", "setup", "--username", "owner")
	setup.Env, setup.Stdin = env, strings.NewReader(randomHex(t)+"\n")
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("identity setup: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	srv := exec.CommandContext(ctx, bin, "serve")
	srv.Env = env
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = srv.Process.Signal(syscall.SIGTERM)
		_ = srv.Wait()
	}()
	base := "http://127.0.0.1:" + port
	var body string
	for i := 0; i < 100; i++ {
		resp, err := http.Get(base + "/")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body = string(b)
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(body, `<div id="root">`) || !strings.Contains(body, "/assets/") {
		t.Fatalf("GET / did not serve the built SPA: %q", body)
	}
	req, _ := http.NewRequest("GET", base+"/healthz", nil)
	req.Host = "evil.example"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("unknown Host not refused: %v %v", err, resp)
	}
}

// checkTransportGate runs the rendered second lint config: clean on the render, and a positive control
// per rule: a handler that imports database/sql, the database module or the storage package is reported.
func checkTransportGate(t *testing.T, dir string) {
	t.Helper()
	lint := func() (string, error) {
		return run(t, dir, "go", "run", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4", "run", "--config", ".golangci.transport.yml", "./...")
	}
	if out, err := lint(); err != nil {
		t.Fatalf("transport lint is not clean on the render: %v\n%s", err, out)
	}
	module := strings.Fields(strings.SplitN(readText(t, filepath.Join(dir, "go.mod")), "\n", 2)[0])[1]
	bad := filepath.Join(dir, "internal", "api", "zz_violation.go")
	for name, imp := range map[string]string{
		"database/sql":    "database/sql",
		"database module": "github.com/hollis-labs/substrate/modules/database",
		"storage package": module + "/internal/storage",
	} {
		src := "package api\n\nimport _ \"" + imp + "\"\n"
		if err := os.WriteFile(bad, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := lint()
		if err == nil || !strings.Contains(out, "transport-boundary") {
			t.Errorf("a handler importing the %s must be reported: err=%v\n%s", name, err, out)
		}
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
}
