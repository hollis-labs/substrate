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
		"cmd/myapp/main.go", "cmd/myapp/config.go", "cmd/myapp/serve.go", "cmd/myapp/substrate.yaml", "cmd/myapp/substrate_wiring.go",
		"docs/identity-bootstrap.md",
		"internal/api/api.go", "internal/api/api_test.go", "internal/hostguard/hostguard.go", "internal/hostguard/hostguard_test.go",
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
	srv := exec.CommandContext(ctx, bin)
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
