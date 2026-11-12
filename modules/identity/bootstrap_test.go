package identity_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/auth"
	_ "github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
)

var secretPassword = strings.Join([]string{"S3cret", "phrase", "PHRASE", "0123"}, "-")

// result is what one run of the app binary would have shown the operator.
type result struct {
	stdout, stderr string // the command's writers
	logs           string // everything the process wrote to os.Stderr, boot logging included
	err            error
}

func (r result) all() string { return r.stdout + r.stderr + r.logs }

// cliApp is the module list a CLI-variant app with identity has; cli before or
// after identity must make no difference.
func cliModules(cliFirst bool) []string {
	m := []string{"health", "logging", "auth", "database", "httphost"}
	if cliFirst {
		return append(m, "cli", "identity")
	}
	return append(m, "identity", "cli")
}

func cliConfig(dir, extra string) string {
	return fmt.Sprintf(`{
		"database": {"sqlite": {"path": %q}},
		"auth": {"mode": "enforce"},
		"identity": {"key_file": %q, "bcrypt_cost": 4%s}
	}`, filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"), extra)
}

// runCLI boots the app (a fresh boot, as a new process would) and runs one
// command with stdin, capturing the process's stderr as well.
func runCLI(t *testing.T, dir string, stdin io.Reader, args ...string) result {
	t.Helper()
	return runCLIWith(t, dir, "", true, stdin, args...)
}

func runCLIWith(t *testing.T, dir, extra string, cliFirst bool, stdin io.Reader, args ...string) result {
	t.Helper()
	errf, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = errf
	defer func() { os.Stderr = orig }()

	var res result
	run, err := boot.Start(context.Background(), "demo", cliModules(cliFirst), cliConfig(dir, extra))
	if err != nil {
		t.Fatal(err)
	}
	root := cliRoot(t, run)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetIn(stdin)
	root.SetArgs(args)
	res.err = root.ExecuteContext(context.Background())
	_ = run.Stop(context.Background())
	res.stdout, res.stderr = out.String(), errb.String()
	b, _ := os.ReadFile(errf.Name())
	res.logs = string(b)
	return res
}

func cliRoot(t *testing.T, run *boot.Running) *cobra.Command {
	t.Helper()
	v, ok := run.App.Value("cli.root")
	if !ok {
		t.Fatal("no cli root")
	}
	return v.(*cobra.Command)
}

func pw(p string) io.Reader { return strings.NewReader(p + "\n") }

func setupArgs() []string {
	return []string{"identity", "setup", "--username", "owner", "--display-name", "Owner"}
}

// bootstrapped makes a database and key file with an owner created offline.
func bootstrapped(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if r := runCLI(t, dir, pw(secretPassword), setupArgs()...); r.err != nil {
		t.Fatalf("setup: %v %s", r.err, r.all())
	}
	return dir
}

// serve starts the service as the real app would: identity.setup left at its default.
func serve(t *testing.T, dir string) *app { return startIn(t, dir, "enforce", `, "setup": "offline"`) }

func TestOwnerCreatedOfflineSignsInAfterTheServiceStarts(t *testing.T) {
	for _, cliFirst := range []bool{true, false} {
		dir := t.TempDir()
		r := runCLIWith(t, dir, "", cliFirst, pw(secretPassword), setupArgs()...)
		if r.err != nil || r.stdout != "owner owner created\n" {
			t.Fatalf("cliFirst=%v: %v %q %s", cliFirst, r.err, r.stdout, r.logs)
		}
		a := serve(t, dir)
		var st struct {
			SetupRequired bool `json:"setup_required"`
		}
		rep := a.do("GET", "/api/setup", nil)
		rep.json(&st)
		if st.SetupRequired {
			t.Fatal("setup_required is still true after the offline setup")
		}
		code(t, a.do("POST", "/api/setup", map[string]string{"username": "evil", "password": password}), 403)
		w := a.login("owner", secretPassword)
		code(t, w, 200)
		code(t, a.do("GET", "/api/me", nil, cookie(w.cookie("demo_session"))), 200)
		code(t, a.login("owner", "not the password"), 401)
		code(t, a.login("evil", password), 401)
	}
}

func TestOfflineSetupMakesTheOwnerWithEveryScope(t *testing.T) {
	dir := bootstrapped(t)
	a := serve(t, dir)
	w := a.login("owner", secretPassword)
	var prof struct {
		Scopes      []string
		DisplayName string `json:"display_name"`
	}
	r := a.do("GET", "/api/identity/profile", nil, cookie(w.cookie("demo_session")))
	code(t, r, 200)
	r.json(&prof)
	if len(prof.Scopes) != 1 || prof.Scopes[0] != "*" || prof.DisplayName != "Owner" {
		t.Fatalf("owner = %+v", prof)
	}
}

func TestOfflineSetupClosesForGood(t *testing.T) {
	dir := bootstrapped(t)
	r := runCLI(t, dir, pw("another-long-password-1"), "identity", "setup", "--username", "second")
	if r.err == nil || !strings.Contains(r.err.Error(), "already done") {
		t.Fatalf("a second setup was not refused: %v", r.err)
	}
	a := serve(t, dir)
	code(t, a.login("second", "another-long-password-1"), 401) // nothing was created
	code(t, a.login("owner", secretPassword), 200)             // and the owner is unchanged
	// Under http mode too, after bootstrap, HTTP setup is refused (setup_closed).
	b := startIn(t, dir, "enforce", `, "setup": "http"`)
	code(t, b.do("POST", "/api/setup", map[string]string{"username": "x2", "password": password}), 409)
}

func TestOfflineSetupRefusesAfterAnHTTPSetup(t *testing.T) {
	dir := t.TempDir()
	a := startIn(t, dir, "enforce", "") // http mode
	a.owner()
	_ = a.running.Stop(context.Background())
	r := runCLI(t, dir, pw(secretPassword), setupArgs()...)
	if r.err == nil || !strings.Contains(r.err.Error(), "already done") {
		t.Fatalf("setup beside an HTTP-made owner: %v", r.err)
	}
}

func TestOfflineModeRefusesHTTPSetupAlwaysAndSaysHow(t *testing.T) {
	dir := t.TempDir()
	a := serve(t, dir) // fresh instance, no owner, identity.setup default
	var st struct {
		SetupRequired bool `json:"setup_required"`
		OverHTTP      bool `json:"setup_over_http"`
	}
	r := a.do("GET", "/api/setup", nil)
	code(t, r, 200)
	r.json(&st)
	if !st.SetupRequired || st.OverHTTP {
		t.Fatalf("GET /api/setup = %+v, want setup_required true and setup_over_http false", st)
	}
	p := a.do("POST", "/api/setup", map[string]string{"display_name": "Owner", "username": "owner", "password": password})
	code(t, p, 403)
	var e struct{ Code, Message string }
	p.json(&e)
	if e.Code != "setup_offline" || !strings.Contains(e.Message, "demo identity setup") {
		t.Fatalf("error = %+v", e)
	}
	if p.cookie("demo_session") != nil {
		t.Error("a refused setup set a session cookie")
	}
	// Still no owner: a claim attempt changed nothing, and the offline command still works.
	r = a.do("GET", "/api/setup", nil)
	r.json(&st)
	if !st.SetupRequired {
		t.Fatal("the refused POST created an owner")
	}
	_ = a.running.Stop(context.Background())
	if res := runCLI(t, dir, pw(secretPassword), setupArgs()...); res.err != nil {
		t.Fatal(res.err)
	}
}

func TestDefaultIsOfflineAndTheSettingIsValidated(t *testing.T) {
	// No setup key at all: closed.
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"database": {"sqlite": {"path": %q}}, "auth": {"mode": "enforce"}, "identity": {"key_file": %q}}`, filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"))
	run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	inner, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := identity.From(run.App).Middleware(inner)
	req := httptest.NewRequest("POST", "/api/setup", strings.NewReader(`{"username":"owner","password":"correct horse battery"}`))
	req.Header.Set("Content-Type", "application/json")
	rep := httptest.NewRecorder()
	h.ServeHTTP(rep, req)
	if rep.Code != 403 {
		t.Fatalf("default config: POST /api/setup = %d, want 403", rep.Code)
	}
	_ = run.Stop(context.Background())
	bad := strings.Replace(cfg, `"key_file"`, `"setup": "open", "key_file"`, 1)
	if r, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, bad); err == nil {
		_ = r.Stop(context.Background())
		t.Fatal(`identity.setup: "open" was accepted`)
	}
}

func TestKeyCreatedOfflineAuthenticatesOverHTTPAndAuthVerify(t *testing.T) {
	dir := bootstrapped(t)
	keyFile := filepath.Join(dir, "svc.key")
	r := runCLI(t, dir, nil, "identity", "key", "create", "--name", "station-a", "--scope", "demo.read", "--scope", "demo.write", "--audience", "demo", "--ttl", "24h", "--out", keyFile)
	if r.err != nil {
		t.Fatalf("%v %s", r.err, r.all())
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(key, "key_") || !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("key file = %q", raw)
	}
	if strings.Contains(r.all(), key) || strings.Contains(r.all(), strings.TrimPrefix(key, "key_")) {
		t.Fatal("the key reached stdout, stderr or the logs")
	}
	id := strings.TrimSpace(r.stdout)
	if !strings.HasPrefix(id, "k_") {
		t.Fatalf("stdout should carry only the credential id, got %q", r.stdout)
	}

	a := serve(t, dir)
	var me struct{ Principal libauth.Principal }
	rep := a.do("GET", "/api/me", nil, bearer(key))
	code(t, rep, 200)
	rep.json(&me)
	if me.Principal.Kind != "service" || me.Principal.Display != "station-a" || !me.Principal.HasScope("demo.write") || me.Principal.HasScope("demo.admin") {
		t.Fatalf("principal = %+v", me.Principal)
	}
	code(t, a.do("GET", "/api/me", nil, bearer(key+"x")), 401)
	// The stdio path verifies a token file's content with auth.Verify.
	p, err := auth.Verify(context.Background(), a.running.App, key)
	if err != nil || p.ID != "key:"+id || !p.Verified {
		t.Fatalf("auth.Verify = %+v, %v", p, err)
	}
	if _, err := auth.Verify(context.Background(), a.running.App, "key_nope"); err == nil {
		t.Fatal("auth.Verify accepted an unknown key")
	}

	// Recorded as created by the local operator, with its audience and expiry.
	owner := a.login("owner", secretPassword).cookie("demo_session")
	var list struct {
		Keys []struct {
			ID, Name  string
			CreatedBy string `json:"created_by"`
			Audiences []string
			ExpiresAt time.Time `json:"expires_at"`
		}
	}
	rep = a.do("GET", "/api/identity/keys", nil, cookie(owner))
	code(t, rep, 200)
	rep.json(&list)
	if len(list.Keys) != 1 || list.Keys[0].CreatedBy != identity.LocalOperator || len(list.Keys[0].Audiences) != 1 || list.Keys[0].Audiences[0] != "demo" {
		t.Fatalf("keys = %+v", list.Keys)
	}
	if d := time.Until(list.Keys[0].ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("expiry in %v, want about 24h", d)
	}
}

func TestOfflineKeyAcceptsAnyScopeAndExpires(t *testing.T) {
	dir := bootstrapped(t)
	r := runCLI(t, dir, nil, "identity", "key", "create", "--name", "root", "--scope", "*", "--stdout")
	if r.err != nil {
		t.Fatal(r.err)
	}
	key := strings.TrimSpace(r.stdout)
	if r.stdout != key+"\n" || !strings.HasPrefix(key, "key_") {
		t.Fatalf("--stdout must print exactly the key and a newline, got %q", r.stdout)
	}
	if strings.Contains(r.stderr+r.logs, key) {
		t.Fatal("the key reached stderr or the logs")
	}
	a := serve(t, dir)
	var me struct{ Principal libauth.Principal }
	rep := a.do("GET", "/api/me", nil, bearer(key))
	code(t, rep, 200)
	rep.json(&me)
	if !me.Principal.HasScope("anything.at.all") {
		t.Fatalf("scope * not granted: %+v", me.Principal)
	}
	// An expired key is refused.
	short, _, err := a.svc.CreateKey(context.Background(), identity.KeyInput{Name: "brief", Scopes: []string{"demo.read"}, TTL: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	code(t, a.do("GET", "/api/me", nil, bearer(short)), 200)
	time.Sleep(2100 * time.Millisecond) // expiry is stored in whole seconds
	code(t, a.do("GET", "/api/me", nil, bearer(short)), 401)
}

func TestKeyCreateNeedsAnOwnerAndValidInput(t *testing.T) {
	dir := t.TempDir()
	r := runCLI(t, dir, nil, "identity", "key", "create", "--name", "x", "--scope", "a", "--stdout")
	if !errors.Is(r.err, identity.ErrNoOwner) || r.stdout != "" {
		t.Fatalf("before setup: %v %q", r.err, r.stdout)
	}
	if r := runCLI(t, dir, pw(secretPassword), setupArgs()...); r.err != nil {
		t.Fatal(r.err)
	}
	for name, args := range map[string][]string{
		"no name":             {"--scope", "a", "--stdout"},
		"no scope":            {"--name", "x", "--stdout"},
		"neither out nor std": {"--name", "x", "--scope", "a"},
		"both out and std":    {"--name", "x", "--scope", "a", "--stdout", "--out", filepath.Join(dir, "k")},
		"negative ttl":        {"--name", "x", "--scope", "a", "--ttl", "-1h", "--stdout"},
		"blank scope":         {"--name", "x", "--scope", "a b", "--stdout"},
	} {
		r := runCLI(t, dir, nil, append([]string{"identity", "key", "create"}, args...)...)
		if r.err == nil || strings.Contains(r.stdout, "key_") {
			t.Errorf("%s: accepted (%v, %q)", name, r.err, r.stdout)
		}
	}
	// A rejected request leaves no key file behind.
	out := filepath.Join(dir, "never.key")
	if r := runCLI(t, dir, nil, "identity", "key", "create", "--name", "x", "--ttl", "-1h", "--scope", "a", "--out", out); r.err == nil {
		t.Fatal("negative ttl accepted")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a failed create left the key file")
	}
	a := serve(t, dir)
	owner := a.login("owner", secretPassword).cookie("demo_session")
	var list struct{ Keys []any }
	rep := a.do("GET", "/api/identity/keys", nil, cookie(owner))
	rep.json(&list)
	if len(list.Keys) != 0 {
		t.Fatalf("rejected requests created %d keys", len(list.Keys))
	}
}

func TestKeyOutFileIsPrivateAndNeverOverwritten(t *testing.T) {
	dir := bootstrapped(t)
	out := filepath.Join(dir, "k.key")
	if r := runCLI(t, dir, nil, "identity", "key", "create", "--name", "x", "--scope", "a", "--out", out); r.err != nil {
		t.Fatal(r.err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v %v, want 0600", st.Mode(), err)
	}
	before, _ := os.ReadFile(out)
	r := runCLI(t, dir, nil, "identity", "key", "create", "--name", "y", "--scope", "a", "--out", out)
	if r.err == nil {
		t.Fatal("an existing file was overwritten")
	}
	if after, _ := os.ReadFile(out); !bytes.Equal(before, after) {
		t.Fatal("the existing key file changed")
	}
	// The refused request made no second key.
	a := serve(t, dir)
	owner := a.login("owner", secretPassword).cookie("demo_session")
	var list struct{ Keys []any }
	rep := a.do("GET", "/api/identity/keys", nil, cookie(owner))
	rep.json(&list)
	if len(list.Keys) != 1 {
		t.Fatalf("%d keys, want 1", len(list.Keys))
	}
	// A symlink at the path is not followed either.
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), link); err != nil {
		t.Fatal(err)
	}
	if r := runCLI(t, dir, nil, "identity", "key", "create", "--name", "z", "--scope", "a", "--out", link); r.err == nil {
		t.Fatal("a dangling symlink was followed")
	}
	if _, err := os.Stat(filepath.Join(dir, "elsewhere")); err == nil {
		t.Fatal("wrote through the symlink")
	}
}

func TestNothingSecretReachesArgvLogsOrStderr(t *testing.T) {
	dir := t.TempDir()
	// Error paths first: a too-short password, a bad username, an empty stdin.
	short := "Zq7-short"
	for name, c := range map[string]struct {
		in   io.Reader
		args []string
	}{
		"short password": {pw(short), setupArgs()},
		"bad username":   {pw(secretPassword), []string{"identity", "setup", "--username", "Bad Name!"}},
		"empty stdin":    {strings.NewReader(""), setupArgs()},
		"no username":    {pw(secretPassword), []string{"identity", "setup"}},
	} {
		r := runCLI(t, dir, c.in, c.args...)
		if r.err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if strings.Contains(r.all()+r.err.Error(), short) || strings.Contains(r.all()+r.err.Error(), secretPassword) {
			t.Fatalf("%s: the password reached output: %s", name, r.all())
		}
	}
	r := runCLI(t, dir, pw(secretPassword), setupArgs()...)
	if r.err != nil {
		t.Fatal(r.err)
	}
	key := runCLI(t, dir, nil, "identity", "key", "create", "--name", "x", "--scope", "a", "--stdout")
	for name, res := range map[string]result{"setup": r, "key": key} {
		if strings.Contains(res.all(), secretPassword) {
			t.Fatalf("%s: the password reached output", name)
		}
	}
	if strings.Contains(key.stderr+key.logs, strings.TrimSpace(key.stdout)) {
		t.Fatal("the key reached stderr or logs")
	}
	// A repeat setup fails without echoing the password either.
	r = runCLI(t, dir, pw(secretPassword), setupArgs()...)
	if r.err == nil || strings.Contains(r.all()+r.err.Error(), secretPassword) {
		t.Fatalf("repeat setup: %v %s", r.err, r.all())
	}

	// No flag carries the password or the key, and the commands read no environment
	// variable (the source is checked in this repo's review; here the flag set).
	run, err := boot.Start(context.Background(), "demo", cliModules(true), cliConfig(dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	flagRe := regexp.MustCompile(`--([a-z-]+)`)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, m := range flagRe.FindAllStringSubmatch(c.LocalFlags().FlagUsages(), -1) {
			if n := m[1]; strings.Contains(n, "pass") || strings.Contains(n, "secret") || n == "key" || n == "token" {
				t.Errorf("%s has a flag --%s that could carry a secret", c.CommandPath(), n)
			}
		}
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	idCmd, _, err := cliRoot(t, run).Find([]string{"identity"})
	if err != nil || idCmd.Name() != "identity" {
		t.Fatalf("no identity command: %v", err)
	}
	walk(idCmd)
}

func TestPasswordPromptOnATerminalIsReadTwiceWithoutEcho(t *testing.T) {
	var prompts []int
	answers := [][]byte{[]byte(secretPassword), []byte(secretPassword)}
	restore := identity.SetTerminalForTest(
		func(io.Reader) (int, bool) { return 7, true },
		func(fd int) ([]byte, error) {
			prompts = append(prompts, fd)
			a := answers[0]
			answers = answers[1:]
			return a, nil
		},
	)
	defer restore()
	dir := t.TempDir()
	// stdin has nothing: the password can only have come from the prompt.
	r := runCLI(t, dir, strings.NewReader(""), setupArgs()...)
	if r.err != nil || len(prompts) != 2 || prompts[0] != 7 {
		t.Fatalf("%v prompts=%v %s", r.err, prompts, r.all())
	}
	if !strings.Contains(r.stderr, "Password:") || strings.Contains(r.all(), secretPassword) {
		t.Fatalf("prompt text goes to stderr and never shows the password: %q", r.stderr)
	}
	restore()
	a := serve(t, dir)
	code(t, a.login("owner", secretPassword), 200)

	// A mismatch creates nothing.
	dir2 := t.TempDir()
	answers = [][]byte{[]byte(secretPassword), []byte(secretPassword + "x")}
	restore = identity.SetTerminalForTest(func(io.Reader) (int, bool) { return 7, true }, func(int) ([]byte, error) { a := answers[0]; answers = answers[1:]; return a, nil })
	defer restore()
	r = runCLI(t, dir2, strings.NewReader(""), setupArgs()...)
	if r.err == nil || !strings.Contains(r.err.Error(), "do not match") {
		t.Fatalf("mismatched passwords: %v", r.err)
	}
	restore()
	b := startIn(t, dir2, "enforce", `, "setup": "offline"`)
	var st struct {
		SetupRequired bool `json:"setup_required"`
	}
	rep := b.do("GET", "/api/setup", nil)
	rep.json(&st)
	if !st.SetupRequired {
		t.Fatal("a mismatched prompt created an owner")
	}
}

func TestPasswordFromStdinTakesTheFirstLineOnly(t *testing.T) {
	dir := t.TempDir()
	r := runCLI(t, dir, strings.NewReader(secretPassword+"\r\nsecond line\n"), setupArgs()...)
	if r.err != nil {
		t.Fatal(r.err)
	}
	a := serve(t, dir)
	code(t, a.login("owner", secretPassword), 200)
}

func TestKeyOnTheAppsOwnDatabaseNotAnother(t *testing.T) {
	// A key made against one database does not exist in another: the command writes where
	// the config points, which is where the service reads.
	d1, d2 := bootstrapped(t), bootstrapped(t)
	r := runCLI(t, d1, nil, "identity", "key", "create", "--name", "x", "--scope", "a", "--stdout")
	if r.err != nil {
		t.Fatal(r.err)
	}
	key := strings.TrimSpace(r.stdout)
	code(t, serve(t, d1).do("GET", "/api/me", nil, bearer(key)), 200)
	code(t, serve(t, d2).do("GET", "/api/me", nil, bearer(key)), 401)
}
