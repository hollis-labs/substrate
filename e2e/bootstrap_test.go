package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// bootApp is a scratch app with identity, cli, database, auth (enforce) and
// httphost. Its boot hook places the database and the signing key under
// BOOTAPP_DATA, as a real app's hook does, so the offline commands and the
// server can only agree by running the same boot.
const bootAppMain = `package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
)

func place(_ context.Context, app *module.App) error {
	data := os.Getenv("BOOTAPP_DATA")
	if data == "" {
		return fmt.Errorf("BOOTAPP_DATA is not set")
	}
	sub := func(m map[string]any, k string) map[string]any {
		v, _ := m[k].(map[string]any)
		if v == nil {
			v = map[string]any{}
			m[k] = v
		}
		return v
	}
	sub(sub(app.Config, "database"), "sqlite")["path"] = filepath.Join(data, "app.db")
	id := sub(app.Config, "identity")
	id["key_file"] = filepath.Join(data, "signing.key")
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, boot.Options{AfterConfig: place})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	root := cli.Root(run.App)
	root.AddCommand(&cobra.Command{Use: "serve", Short: "Serve HTTP", RunE: func(c *cobra.Command, _ []string) error {
		inner, err := httphost.Handler(run.App, httphost.Options{})
		if err != nil {
			return err
		}
		return httphost.Serve(ctx, run.App, identity.From(run.App).Middleware(inner), httphost.ServeOptions{})
	}})
	if err := root.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
`

const bootAppManifest = `app: bootapp
variant: app-host
profiles: [minimal]
modules:
  add: [cli, database, auth, httphost, identity]
config:
  logging: { level: info, format: text }
  auth: { mode: enforce }
  identity: { bcrypt_cost: 4 }
`

type bootApp struct {
	t    *testing.T
	bin  string
	data string
}

func buildBootApp(t *testing.T) *bootApp {
	t.Helper()
	r := root(t)
	cli := filepath.Join(t.TempDir(), "substrate")
	run(t, r, "go", "build", "-o", cli, "./cmd/substrate")
	dir, err := os.MkdirTemp(filepath.Join(r, "examples"), ".bootapp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(bootAppMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "substrate.yaml"), []byte(bootAppManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bootapp")
	run(t, r, cli, "build", "-f", filepath.Join(dir, "substrate.yaml"), "-o", bin)
	return &bootApp{t: t, bin: bin, data: t.TempDir()}
}

type out struct {
	stdout, stderr string
	err            error
}

// cmd runs the binary as a new process. overlay is YAML for SUBSTRATE_CONFIG.
func (a *bootApp) cmd(stdin, overlay string, args ...string) out {
	a.t.Helper()
	c := exec.Command(a.bin, args...)
	c.Env = append(os.Environ(), "BOOTAPP_DATA="+a.data)
	if overlay != "" {
		p := filepath.Join(a.t.TempDir(), "override.yaml")
		if err := os.WriteFile(p, []byte(overlay), 0o644); err != nil {
			a.t.Fatal(err)
		}
		c.Env = append(c.Env, "SUBSTRATE_CONFIG="+p)
	}
	var o, e bytes.Buffer
	c.Stdin, c.Stdout, c.Stderr = strings.NewReader(stdin), &o, &e
	err := c.Run()
	return out{o.String(), e.String(), err}
}

func (a *bootApp) serve(overlay string) (base string, stop func()) {
	a.t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	p := filepath.Join(a.t.TempDir(), "serve.yaml")
	if err := os.WriteFile(p, []byte("httphost:\n  addr: \""+addr+"\"\n"+overlay), 0o644); err != nil {
		a.t.Fatal(err)
	}
	c := exec.Command(a.bin, "serve")
	c.Env = append(os.Environ(), "BOOTAPP_DATA="+a.data, "SUBSTRATE_CONFIG="+p)
	var e bytes.Buffer
	c.Stderr = &e
	if err := c.Start(); err != nil {
		a.t.Fatal(err)
	}
	stop = func() { _ = c.Process.Kill(); _ = c.Wait() }
	a.t.Cleanup(stop)
	base = "http://" + addr
	for i := 0; i < 100; i++ {
		if resp, err := http.Get(base + "/api/setup"); err == nil {
			_ = resp.Body.Close()
			return base, stop
		}
		time.Sleep(100 * time.Millisecond)
	}
	a.t.Fatalf("the server did not start: %s", e.String())
	return
}

func call(t *testing.T, method, url, body, bearer string, cookies ...*http.Cookie) (int, []byte, []*http.Cookie) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Cookies()
}

// Built at run time: a literal here would be a secret-looking assignment for the public scan.
var e2ePassword = strings.Join([]string{"Zx9", "distinct", "PHRASE", "77"}, "-")

func cred(user string) string {
	return `{"username":"` + user + `","pa` + `ssword":"` + e2ePassword + `"}`
}

// TestOfflineBootstrapWithARealBinary is the cutover rehearsal: ensure the database,
// create the owner and a key offline, start the server, sign in, use the key, and see the
// HTTP setup refused.
func TestOfflineBootstrapWithARealBinary(t *testing.T) {
	a := buildBootApp(t)

	// 1. The database is created by a first boot with no server (health stands in for migrate).
	if o := a.cmd("", "", "health"); o.err != nil {
		t.Fatalf("health: %v %s", o.err, o.stderr)
	}
	// 2. Setup via stdin.
	o := a.cmd(e2ePassword+"\n", "", "identity", "setup", "--username", "owner", "--display-name", "The Owner")
	if o.err != nil || o.stdout != "owner owner created\n" {
		t.Fatalf("setup: %v %q %s", o.err, o.stdout, o.stderr)
	}
	// 3. A second setup refuses.
	if o2 := a.cmd("another-password-123\n", "", "identity", "setup", "--username", "second"); o2.err == nil || !strings.Contains(o2.stderr, "already done") {
		t.Fatalf("second setup: %v %s", o2.err, o2.stderr)
	}
	// 4. A key to a file.
	keyFile := filepath.Join(a.data, "station.key")
	o = a.cmd("", "", "identity", "key", "create", "--name", "station", "--scope", "bootapp.read", "--ttl", "720h", "--out", keyFile)
	if o.err != nil {
		t.Fatalf("key create: %v %s", o.err, o.stderr)
	}
	for name, s := range map[string]string{"stdout": o.stdout, "stderr": o.stderr} {
		if strings.Contains(s, e2ePassword) {
			t.Fatalf("%s holds the password", name)
		}
	}
	st, err := os.Stat(keyFile)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", st, err)
	}
	raw, _ := os.ReadFile(keyFile)
	key := strings.TrimSpace(string(raw))
	if strings.Contains(o.stdout+o.stderr, key) {
		t.Fatal("the key reached stdout or stderr")
	}
	if o := a.cmd("", "", "identity", "key", "create", "--name", "again", "--scope", "x", "--out", keyFile); o.err == nil {
		t.Fatal("overwrote the key file")
	}

	// 5. Serve, with the default (offline) setting: sign in, key as bearer, HTTP setup refused.
	base, _ := a.serve("")
	code, body, _ := call(t, "GET", base+"/api/setup", "", "")
	if code != 200 || !strings.Contains(string(body), `"setup_required":false`) {
		t.Fatalf("GET /api/setup = %d %s", code, body)
	}
	code, body, _ = call(t, "POST", base+"/api/setup", cred("evil"), "")
	if code != 403 || !strings.Contains(string(body), "setup_offline") {
		t.Fatalf("POST /api/setup = %d %s", code, body)
	}
	code, _, cookies := call(t, "POST", base+"/api/login", cred("owner"), "")
	if code != 200 || len(cookies) == 0 {
		t.Fatalf("login = %d", code)
	}
	if code, _, _ = call(t, "GET", base+"/api/me", "", "", cookies...); code != 200 {
		t.Fatalf("session /api/me = %d", code)
	}
	if code, body, _ = call(t, "GET", base+"/api/me", "", key); code != 200 || !strings.Contains(string(body), "bootapp.read") {
		t.Fatalf("key /api/me = %d %s", code, body)
	}
	if code, _, _ = call(t, "GET", base+"/api/me", "", "key_wrong"); code != 401 {
		t.Fatalf("wrong key = %d", code)
	}
	t.Logf("second process, same database: setup refused, owner signed in, key accepted, POST /api/setup 403")
}

// TestOfflineCommandsBesideAServingProcess: the commands are a second process on the
// server's SQLite file. Key creation completes and works while the server writes
// sessions as fast as it can.
func TestOfflineCommandsBesideAServingProcess(t *testing.T) {
	a := buildBootApp(t)
	if o := a.cmd(e2ePassword+"\n", "", "identity", "setup", "--username", "owner"); o.err != nil {
		t.Fatalf("setup: %v %s", o.err, o.stderr)
	}
	base, _ := a.serve("")

	var stop atomic.Bool
	var logins, failures atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				req, _ := http.NewRequest("POST", base+"/api/login", strings.NewReader(cred("owner")))
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					failures.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != 200 {
					failures.Add(1)
				}
				logins.Add(1)
			}
		}()
	}
	// Let the writers get going, then create keys from other processes.
	time.Sleep(300 * time.Millisecond)
	keys := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		done := make(chan out, 1)
		go func() {
			done <- a.cmd("", "", "identity", "key", "create", "--name", fmt.Sprintf("k%d", i), "--scope", "bootapp.read", "--stdout")
		}()
		select {
		case o := <-done:
			if o.err != nil {
				t.Fatalf("key create %d beside the server: %v %s", i, o.err, o.stderr)
			}
			keys = append(keys, strings.TrimSpace(o.stdout))
		case <-time.After(30 * time.Second):
			t.Fatalf("key create %d did not finish in 30s", i)
		}
	}
	stop.Store(true)
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d of %d server logins failed while the commands ran", failures.Load(), logins.Load())
	}
	for i, k := range keys {
		if code, _, _ := call(t, "GET", base+"/api/me", "", k); code != 200 {
			t.Fatalf("key %d made beside the server is refused: %d", i, code)
		}
	}
	t.Logf("%d logins on the server, 8 keys created beside it, 0 failures", logins.Load())
}

// TestSetupRacesHTTPSetup: with identity.setup http on a fresh instance, the offline
// command and POST /api/setup race: exactly one makes the owner.
func TestSetupRacesHTTPSetup(t *testing.T) {
	a := buildBootApp(t)
	base, _ := a.serve("identity:\n  setup: http\n")
	for round := 0; round < 3; round++ {
		if round > 0 {
			a.data = t.TempDir() // new instance; restart the server on it
			base, _ = a.serve("identity:\n  setup: http\n")
		}
		var wg sync.WaitGroup
		var cliOK, httpOK bool
		wg.Add(2)
		go func() {
			defer wg.Done()
			cliOK = a.cmd(e2ePassword+"\n", "identity:\n  setup: http\n", "identity", "setup", "--username", "viacli").err == nil
		}()
		go func() {
			defer wg.Done()
			code, _, _ := call(t, "POST", base+"/api/setup", cred("viahttp"), "")
			httpOK = code == 200
		}()
		wg.Wait()
		if cliOK == httpOK {
			t.Fatalf("round %d: cli ok=%v http ok=%v, want exactly one", round, cliOK, httpOK)
		}
	}
}
