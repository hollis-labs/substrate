// Package e2e holds the MVP acceptance tests that build real binaries.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func root(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Dir(wd)
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, errb.String())
	}
	return out.String()
}

// buildProfile copies hello-app-host's source, swaps in a manifest for profile,
// and builds it with the substrate CLI. Returns the binary and linked packages.
func buildProfile(t *testing.T, profile string) (bin string, pkgs []string, size int64) {
	t.Helper()
	r := root(t)
	cli := filepath.Join(t.TempDir(), "substrate")
	run(t, r, "go", "build", "-o", cli, "./cmd/substrate")

	dir, err := os.MkdirTemp(filepath.Join(r, "examples"), ".flag-"+profile+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	src, _ := os.ReadFile(filepath.Join(r, "examples/hello-app-host/main.go"))
	if err := os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "app: flag-" + profile + "\nvariant: app-host\nprofiles: [" + profile + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "substrate.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "app")
	run(t, r, cli, "build", "-f", filepath.Join(dir, "substrate.yaml"), "-o", bin)
	deps := run(t, dir, "go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	for _, p := range strings.Fields(deps) {
		if strings.HasPrefix(p, "github.com/hollis-labs/substrate/modules/") {
			pkgs = append(pkgs, strings.TrimPrefix(p, "github.com/hollis-labs/substrate/modules/"))
		}
	}
	st, _ := os.Stat(bin)
	return bin, pkgs, st.Size()
}

func get(t *testing.T, bin, path string) (int, []byte) {
	return request(t, bin, "GET", path, "")
}

func request(t *testing.T, bin, method, path, body string) (int, []byte) {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	c := exec.Command(bin)
	c.Env = append(os.Environ(), "SUBSTRATE_CONFIG="+addrConfig(t, addr))
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		req, _ := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, b.Bytes()
}

// TestFlag is the MVP flag test: minimal and agentic build from the same source;
// the minimal binary excludes the agentic-only module.
func TestFlag(t *testing.T) {
	minBin, minPkgs, minSize := buildProfile(t, "minimal")
	agBin, agPkgs, agSize := buildProfile(t, "agentic")
	t.Logf("minimal: %v %d bytes; agentic: %v %d bytes", minPkgs, minSize, agPkgs, agSize)

	has := func(ps []string, n string) bool {
		for _, p := range ps {
			if p == n {
				return true
			}
		}
		return false
	}
	agenticOnly := []string{"capabilities", "secrets", "auth", "mcp"}
	for _, m := range agenticOnly {
		if has(minPkgs, m) {
			t.Errorf("minimal links %s", m)
		}
		if !has(agPkgs, m) {
			t.Errorf("agentic does not link %s", m)
		}
	}
	// Modules in neither profile must not be linked by either.
	for _, m := range []string{"policy", "database", "pluginhost", "cli"} {
		if has(minPkgs, m) || has(agPkgs, m) {
			t.Errorf("%s linked but selected by neither profile", m)
		}
	}
	if agSize <= minSize {
		t.Errorf("agentic binary (%d) not larger than minimal (%d)", agSize, minSize)
	}

	if code, _ := get(t, minBin, "/api/capabilities"); code != http.StatusNotFound {
		t.Errorf("minimal /api/capabilities = %d, want 404", code)
	}
	// The SPA fallback answers unmapped paths, so absence of MCP shows as no JSON-RPC reply.
	rpc := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if _, b := request(t, minBin, "POST", "/mcp", rpc); strings.Contains(string(b), `"jsonrpc"`) {
		t.Errorf("minimal answered MCP: %s", b)
	}
	if _, b := request(t, agBin, "POST", "/mcp", rpc); !strings.Contains(string(b), `"jsonrpc"`) {
		t.Errorf("agentic did not answer MCP: %s", b)
	}
	code, body := get(t, agBin, "/api/capabilities")
	if code != 200 {
		t.Fatalf("agentic /api/capabilities = %d", code)
	}
	var rep struct{ Started []string }
	if err := json.Unmarshal(body, &rep); err != nil || !has(rep.Started, "mcp") {
		t.Errorf("bad report %s", body)
	}
	if code, _ := get(t, agBin, "/healthz"); code != 200 {
		t.Errorf("/healthz = %d", code)
	}
}

// TestHelloAgentic builds the real example from its manifest, then lists and
// calls its one tool over MCP.
func TestHelloAgentic(t *testing.T) {
	r := root(t)
	cli := filepath.Join(t.TempDir(), "substrate")
	run(t, r, "go", "build", "-o", cli, "./cmd/substrate")
	bin := filepath.Join(t.TempDir(), "hello-agentic")
	run(t, r, cli, "build", "-f", "examples/hello-agentic/substrate.yaml", "-o", bin)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	c := exec.Command(bin)
	c.Env = append(os.Environ(), "SUBSTRATE_CONFIG="+addrConfig(t, addr))
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()

	post := func(body string) string {
		req, _ := http.NewRequest("POST", "http://"+addr+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		var resp *http.Response
		var err error
		for i := 0; i < 50; i++ {
			resp, err = http.DefaultClient.Do(req.Clone(context.Background()))
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return b.String()
	}
	if out := post(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); !strings.Contains(out, `"hello"`) {
		t.Errorf("tools/list missing hello: %s", out)
	}
	if out := post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"hello","arguments":{}}}`); !strings.Contains(out, "hello") {
		t.Errorf("tools/call: %s", out)
	}
}

// addrConfig writes the standard config-file override that sets httphost.addr
// and returns its path, which is how every Substrate app is told where to listen.
func addrConfig(t *testing.T, addr string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "addr.yaml")
	if err := os.WriteFile(p, []byte("httphost:\n  addr: \""+addr+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
