package connectmatrix_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/auth"

	cm "github.com/hollis-labs/tools/connect-matrix"
)

func gridRunner(t *testing.T, iss *httptest.Server, targets []cm.Pair, callers []string) *cm.Runner {
	t.Helper()
	cfg := cm.Config{Issuer: cm.IssuerConfig{Type: "mock", URL: iss.URL}, RevokeBoundSeconds: 5, PollIntervalMillis: 20, Callers: callers, Targets: targets}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	is, err := cm.NewIssuer(cfg.Issuer, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &cm.Runner{Issuer: is, Cfg: cfg}
}

func tpl(name, url string) cm.Pair {
	return cm.Pair{Name: name, Target: url, Audience: "torque", Scopes: []string{"torque.read"}, Probe: cm.Probe{Path: "/api/v1/tasks"}}
}

func TestMatrixStatuses(t *testing.T) {
	iss := mockIssuer(t)
	enf := target(t, iss.URL, auth.ModeEnforce, true)
	open := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	t.Cleanup(open.Close)
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	t.Cleanup(deny.Close)
	na := tpl("cerberus", "http://127.0.0.1:1")
	na.NA = "unix socket only"

	r := gridRunner(t, iss, []cm.Pair{tpl("enforced", enf.URL), tpl("open", open.URL), tpl("gate", deny.URL), na}, []string{"tachyon", "enforced"})
	rs := r.Run(context.Background(), nil)
	got := map[string]string{}
	for _, p := range rs {
		got[p.Name] = p.Status
	}
	want := map[string]string{
		"tachyon->enforced": "pass", "enforced->enforced": "n-a",
		"tachyon->open": "not-enforced", "enforced->open": "not-enforced",
		"tachyon->gate": "fail", "enforced->gate": "fail",
		"tachyon->cerberus": "n-a", "enforced->cerberus": "n-a",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	if cm.AllPassed(rs) {
		t.Error("a failing pair must fail the run")
	}
	var buf bytes.Buffer
	cm.WriteMatrix(&buf, rs)
	out := buf.String()
	for _, s := range []string{"CALLER\\TARGET", "tachyon", "not-enforced", "pass=1 fail=2 not-enforced=2 n-a=3"} {
		if !strings.Contains(out, s) {
			t.Errorf("matrix missing %q:\n%s", s, out)
		}
	}
	buf.Reset()
	if err := cm.WriteJSON(&buf, rs); err != nil || !strings.Contains(buf.String(), `"not-enforced": 2`) {
		t.Errorf("json summary: %v %s", err, buf.String())
	}
}

func TestWriteProbeAndHostHeader(t *testing.T) {
	iss := mockIssuer(t)
	var host, wrote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		if r.Method == "POST" {
			wrote = r.URL.Path
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	p := tpl("app", srv.URL)
	p.Probe.Host = "app.example.test"
	p.WriteProbe = &cm.Probe{Path: "/api/v1/ping"}
	rs := gridRunner(t, iss, []cm.Pair{p}, []string{"tachyon"}).Run(context.Background(), nil)
	if host != "app.example.test" || wrote != "/api/v1/ping" {
		t.Errorf("host=%q wrote=%q", host, wrote)
	}
	if c := result(rs[0], "write_accepted"); c.Result != cm.Pass {
		t.Errorf("write: %+v", c)
	}
}

// A fake tether CLI proves the cli issuer: argv shape, token read from stdout,
// revoke by jti, and no token in errors.
func TestCLIIssuer(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := filepath.Join(dir, "tether")
	body := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"if [ \"$2\" = issue ]; then echo '{\"token\":\"SECRET-TOKEN-VALUE\",\"jti\":\"jti-1\"}'; exit 0; fi\n" +
		"if [ \"$2\" = revoke ]; then echo 'revoked' ; exit 0; fi\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	is, err := cm.NewIssuer(cm.IssuerConfig{Type: "cli", Command: script, GlobalArgs: []string{"--catalog", "/c"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := is.Mint(context.Background(), cm.MintRequest{Name: "x->y", App: "tachyon", Audience: "torque", Scopes: []string{"torque.read"}})
	if err != nil || m.ID != "jti-1" || m.Token == "" {
		t.Fatalf("mint: %v %+v", err, m)
	}
	if err := is.Revoke(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	for _, s := range []string{"service-token issue --catalog /c --app tachyon --audience torque", "--scope torque.read", "service-token revoke jti-1 --catalog /c"} {
		if !strings.Contains(string(b), s) {
			t.Errorf("argv log missing %q:\n%s", s, b)
		}
	}
	bad, _ := cm.NewIssuer(cm.IssuerConfig{Type: "cli", Command: filepath.Join(dir, "missing")}, nil)
	if _, err := bad.Mint(context.Background(), cm.MintRequest{Name: "n", Audience: "a", Scopes: []string{"a.read"}}); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("mint with a missing command: %v", err)
	}
}

func TestCLIConfigRequiresCommand(t *testing.T) {
	c := cm.Config{Issuer: cm.IssuerConfig{Type: "cli"}}
	if err := c.Validate(); err == nil {
		t.Fatal("cli issuer without a command accepted")
	}
}

func TestOtherScopesFollowTheOtherAudience(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := filepath.Join(dir, "tether")
	body := "#!/bin/sh\necho \"$@\" >> " + log + "\nif [ \"$2\" = issue ]; then echo '{\"token\":\"t\",\"jti\":\"j\"}'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	p := tpl("x", srv.URL)
	p.OtherAudience = "nanite"
	cfg := cm.Config{Issuer: cm.IssuerConfig{Type: "cli", Command: script}, RevokeBoundSeconds: 1, Callers: []string{"c"}, Targets: []cm.Pair{p}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	is, _ := cm.NewIssuer(cfg.Issuer, nil)
	(&cm.Runner{Issuer: is, Cfg: cfg}).Run(context.Background(), nil)
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "--audience nanite --label connect-matrix --json --scope nanite.read") {
		t.Errorf("other-audience mint used the wrong scopes:\n%s", b)
	}
}
