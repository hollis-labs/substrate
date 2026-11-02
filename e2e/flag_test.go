// Package e2e holds the MVP acceptance tests that build real binaries.
package e2e

import (
	"bytes"
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
	t.Cleanup(func() { os.RemoveAll(dir) })
	src, _ := os.ReadFile(filepath.Join(r, "examples/hello-app-host/main.go"))
	os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644)
	os.WriteFile(filepath.Join(dir, "substrate.yaml"),
		[]byte("app: flag-"+profile+"\nvariant: app-host\nprofiles: ["+profile+"]\n"), 0o644)
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
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	c := exec.Command(bin)
	c.Env = append(os.Environ(), "ADDR="+addr)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { c.Process.Kill(); c.Wait() }()
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + path)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
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
	if has(minPkgs, "capabilities") {
		t.Fatal("minimal links capabilities")
	}
	if !has(agPkgs, "capabilities") {
		t.Fatal("agentic does not link capabilities")
	}
	if agSize <= minSize {
		t.Errorf("agentic binary (%d) not larger than minimal (%d)", agSize, minSize)
	}

	if code, _ := get(t, minBin, "/api/capabilities"); code != http.StatusNotFound {
		t.Errorf("minimal /api/capabilities = %d, want 404", code)
	}
	code, body := get(t, agBin, "/api/capabilities")
	if code != 200 {
		t.Fatalf("agentic /api/capabilities = %d", code)
	}
	var rep struct{ Started []string }
	if err := json.Unmarshal(body, &rep); err != nil || !has(rep.Started, "capabilities") {
		t.Errorf("bad report %s", body)
	}
	if code, _ := get(t, agBin, "/healthz"); code != 200 {
		t.Errorf("/healthz = %d", code)
	}
}
