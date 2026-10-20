package connectmatrix_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"

	cm "github.com/hollis-labs/tools/connect-matrix"
)

const issName = "mock-test"

// target is a stand-in app: a go-auth guard in front of one route, trusting
// the mock issuer's JWKS and (optionally) its deny-list, exactly as Torque or
// Tesseract are configured.
func target(t *testing.T, issuerURL string, mode auth.Mode, withDenyList bool) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := auth.JWTConfig{Issuer: issName, Audience: "torque", JWKSURL: issuerURL + "/.well-known/jwks.json"}
	if withDenyList {
		dl := auth.NewDenyList()
		cfg.DenyList = dl
		go (&auth.DenyListPoller{URL: issuerURL + "/auth/denylist", List: dl, Interval: 25 * time.Millisecond}).Run(ctx)
	}
	jv, err := auth.NewJWTVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g, err := auth.New(auth.Config{Mode: mode, Verifier: jv, Scopes: auth.NewScopeTable().Route("GET", "/api/v1/tasks", "torque.read")})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })))
	t.Cleanup(ts.Close)
	return ts
}

func mockIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	m, err := cm.NewMockIssuer(issName)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(m.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func runner(t *testing.T, iss *httptest.Server, tgt *httptest.Server, enforcing bool, boundSeconds int) *cm.Runner {
	t.Helper()
	cfg := cm.Config{
		Issuer:             cm.IssuerConfig{Type: "mock", URL: iss.URL},
		RevokeBoundSeconds: boundSeconds, PollIntervalMillis: 20,
		Pairs: []cm.Pair{{Name: "torque", Target: tgt.URL, Audience: "torque", Scopes: []string{"torque.read"},
			Probe: cm.Probe{Path: "/api/v1/tasks"}, Enforcing: &enforcing}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	is, err := cm.NewIssuer(cfg.Issuer, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &cm.Runner{Issuer: is, Cfg: cfg}
}

func result(r cm.PairResult, check string) cm.CheckResult {
	for _, c := range r.Checks {
		if c.Name == check {
			return c
		}
	}
	return cm.CheckResult{Name: check, Result: "missing"}
}

func TestEnforcingTargetProvesAllChecks(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeEnforce, true)
	rs := runner(t, iss, tgt, true, 5).Run(context.Background(), nil)
	if !cm.AllPassed(rs) {
		t.Fatalf("expected a pass: %+v", rs)
	}
	for _, c := range []string{"mint", "accepted", "wrong_audience", "revoke", "revoked"} {
		if got := result(rs[0], c); got.Result != cm.Pass {
			t.Errorf("%s: %+v", c, got)
		}
	}
	if s := result(rs[0], "revoked").Seconds; s <= 0 || s > 5 {
		t.Errorf("revocation took %.2fs", s)
	}
}

func TestRevocationThatNeverTakesEffectFails(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeEnforce, false) // target ignores the deny-list
	rs := runner(t, iss, tgt, true, 1).Run(context.Background(), nil)
	if cm.AllPassed(rs) {
		t.Fatal("a target that never honors revocation passed")
	}
	if c := result(rs[0], "revoked"); c.Result != cm.Fail || !strings.Contains(c.Detail, "still answered 200") {
		t.Fatalf("revoked check: %+v", c)
	}
}

func TestNonEnforcingTargetIsReportedNotFailed(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeOff, true) // dial off: admits anyone
	rs := runner(t, iss, tgt, false, 5).Run(context.Background(), nil)
	if !cm.AllPassed(rs) {
		t.Fatalf("declared non-enforcing pair should pass its accepted check: %+v", rs)
	}
	if result(rs[0], "wrong_audience").Result != cm.Skip || result(rs[0], "revoked").Result != cm.Skip {
		t.Fatalf("checks not skipped: %+v", rs[0].Checks)
	}
	// Claiming enforcement on a target that is not enforcing must fail loudly.
	rs = runner(t, iss, tgt, true, 1).Run(context.Background(), nil)
	if c := result(rs[0], "wrong_audience"); c.Result != cm.Fail || !strings.Contains(c.Detail, "enforcing") {
		t.Fatalf("wrong_audience on an off target: %+v", c)
	}
}

func TestUnreachableTargetAndIssuerFailCleanly(t *testing.T) {
	iss := mockIssuer(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	rs := runner(t, iss, dead, true, 1).Run(context.Background(), nil)
	if c := result(rs[0], "accepted"); c.Result != cm.Fail || !strings.Contains(c.Detail, "probe error") {
		t.Fatalf("dead target: %+v", c)
	}
	downIssuer := httptest.NewServer(http.NotFoundHandler())
	downIssuer.Close()
	rs = runner(t, downIssuer, iss, true, 1).Run(context.Background(), nil)
	if c := result(rs[0], "mint"); c.Result != cm.Fail {
		t.Fatalf("dead issuer: %+v", c)
	}
}

func TestReportsNeverContainATokenOrCredential(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeEnforce, true)
	rs := runner(t, iss, tgt, true, 5).Run(context.Background(), nil)
	var table, js bytes.Buffer
	cm.WriteTable(&table, rs)
	if err := cm.WriteJSON(&js, rs); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{table.String(), js.String()} {
		if strings.Contains(out, "eyJ") || strings.Contains(out, "Bearer") {
			t.Fatalf("report leaks a token:\n%s", out)
		}
	}
	if !strings.Contains(table.String(), "1 of 1 pairs proved") {
		t.Fatalf("table: %s", table.String())
	}
}

func TestHTTPIssuerSendsCredentialFromFile(t *testing.T) {
	var gotAuth, gotKind string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/mint") {
			b := make([]byte, 512)
			n, _ := r.Body.Read(b)
			if strings.Contains(string(b[:n]), `"kind":"service"`) {
				gotKind = "service"
			}
			_, _ = w.Write([]byte(`{"token":"t","id":"i"}`))
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	cred := filepath.Join(t.TempDir(), "cred")
	if err := os.WriteFile(cred, []byte("op-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	is, err := cm.NewIssuer(cm.IssuerConfig{Type: "http", MintURL: srv.URL + "/mint", RevokeURL: srv.URL + "/revoke", CredentialFile: cred}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := is.Mint(context.Background(), cm.MintRequest{Name: "x", Audience: "a", Scopes: []string{"a.read"}})
	if err != nil || m.Token != "t" || m.ID != "i" || gotAuth != "Bearer op-secret" || gotKind != "service" {
		t.Fatalf("mint: %+v %v auth=%q kind=%q", m, err, gotAuth, gotKind)
	}
	if err := is.Revoke(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidation(t *testing.T) {
	good := `{"issuer":{"type":"mock","url":"http://127.0.0.1:9100"},"pairs":[{"name":"a","target":"http://x","audience":"a","scopes":["a.read"],"probe":{"path":"/p"}}]}`
	p := filepath.Join(t.TempDir(), "c.json")
	write := func(s string) { _ = os.WriteFile(p, []byte(s), 0o600) }
	write(good)
	c, err := cm.LoadConfig(p)
	if err != nil || c.RevokeBoundSeconds != 45 || c.Pairs[0].Probe.Method != "GET" || c.Pairs[0].Probe.OKStatus != 200 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for name, bad := range map[string]string{
		"unknown field":  strings.Replace(good, `"pairs"`, `"bogus":1,"pairs"`, 1),
		"no pairs":       `{"issuer":{"type":"mock","url":"u"},"pairs":[]}`,
		"bad issuer":     strings.Replace(good, `"mock"`, `"nope"`, 1),
		"no scopes":      strings.Replace(good, `"scopes":["a.read"]`, `"scopes":[]`, 1),
		"same other aud": strings.Replace(good, `"probe"`, `"other_audience":"a","probe"`, 1),
		"duplicate":      strings.Replace(good, `"pairs":[`, `"pairs":[{"name":"a","target":"t","audience":"a","scopes":["a.read"],"probe":{"path":"/p"}},`, 1),
	} {
		write(bad)
		if _, err := cm.LoadConfig(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// --- review findings: late refusal, secret-bearing URLs, cleanup, 2xx admission, redirects ---

// stubIssuer mints fake tokens and can be told to fail revocations.
type stubIssuer struct {
	mu         sync.Mutex
	n          int
	revoked    map[string]bool
	failRevoke bool
}

func newStubIssuer() *stubIssuer { return &stubIssuer{revoked: map[string]bool{}} }

func (s *stubIssuer) Mint(_ context.Context, r cm.MintRequest) (cm.Minted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	id := r.Name + "-" + strconv.Itoa(s.n)
	return cm.Minted{Token: "tok-" + id, ID: id}, nil
}

func (s *stubIssuer) Revoke(_ context.Context, m cm.Minted) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failRevoke {
		return errors.New("issuer refused revoke")
	}
	s.revoked[m.ID] = true
	return nil
}

func (s *stubIssuer) isRevoked(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revoked[strings.TrimPrefix(token, "tok-")]
}

func stubRunner(t *testing.T, is cm.Issuer, p cm.Pair, bound int) *cm.Runner {
	t.Helper()
	cfg := cm.Config{Issuer: cm.IssuerConfig{Type: "mock", URL: "http://127.0.0.1:1"}, RevokeBoundSeconds: bound, PollIntervalMillis: 10, Pairs: []cm.Pair{p}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return &cm.Runner{Issuer: is, Cfg: cfg}
}

func TestRefusalObservedAfterTheWaitIsAFailure(t *testing.T) {
	is := newStubIssuer()
	now := time.Unix(1_800_000_000, 0)
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if is.isRevoked(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			mu.Lock()
			now = now.Add(2 * time.Second) // the refusal takes longer than the 1s wait
			mu.Unlock()
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	enforcing := true
	r := stubRunner(t, is, cm.Pair{Name: "p", Target: srv.URL, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}, Enforcing: &enforcing}, 1)
	r.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	rs := r.Run(context.Background(), nil)
	if c := result(rs[0], "revoked"); c.Result != cm.Fail || !strings.Contains(c.Detail, "only after") {
		t.Fatalf("a refusal observed after the wait passed: %+v", c)
	}
	if cm.AllPassed(rs) {
		t.Fatal("pair passed")
	}
}

func TestRevokedWaitEndsWhenTheContextIsCanceled(t *testing.T) {
	is := newStubIssuer()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })) // never refuses
	defer srv.Close()
	enforcing := true
	r := stubRunner(t, is, cm.Pair{Name: "p", Target: srv.URL, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}, Enforcing: &enforcing}, 30)
	r.Cfg.PollIntervalMillis = 10000 // a long sleep between probes
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []cm.PairResult, 1)
	go func() { done <- r.Run(ctx, nil) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case rs := <-done:
		if result(rs[0], "revoked").Result != cm.Fail {
			t.Fatalf("a canceled wait must not pass: %+v", rs[0].Checks)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait ignored cancellation")
	}
}

func TestSecretBearingURLsNeverReachReportsOrErrors(t *testing.T) {
	const canary = "CANARY-VALUE"
	cfgFor := func(target string) cm.Config {
		return cm.Config{Issuer: cm.IssuerConfig{Type: "mock", URL: "http://127.0.0.1:1"}, Pairs: []cm.Pair{{
			Name: "p", Target: target, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}}}}
	}
	// Configuration refuses them, without echoing the value.
	for _, target := range []string{"http://user:" + canary + "@127.0.0.1:1/", "http://127.0.0.1:1/?key=" + canary, "http://127.0.0.1:1/#" + canary} {
		c := cfgFor(target)
		err := c.Validate()
		if err == nil || strings.Contains(err.Error(), canary) {
			t.Errorf("target %q: want a refusal without the canary, got %v", target, err)
		}
	}
	c := cfgFor("http://127.0.0.1:1")
	c.Issuer = cm.IssuerConfig{Type: "http", MintURL: "http://127.0.0.1:1/m?token=" + canary, RevokeURL: "http://127.0.0.1:1/r", CredentialFile: "/x"}
	if err := c.Validate(); err == nil || strings.Contains(err.Error(), canary) {
		t.Errorf("issuer URL with a query: %v", err)
	}
	// A Runner built without Validate must not leak either: dead host, userinfo,
	// query, and a URL that does not parse.
	for name, target := range map[string]string{
		"dead host with userinfo and query": "http://user:" + canary + "@127.0.0.1:1/?key=" + canary,
		"unparseable":                       "http://exa mple.invalid/?key=" + canary,
		"control character":                 "http://127.0.0.1:1/\x7f?key=" + canary,
	} {
		r := &cm.Runner{Issuer: newStubIssuer(), Cfg: cm.Config{RevokeBoundSeconds: 1, PollIntervalMillis: 10, Pairs: []cm.Pair{{
			Name: "p", Target: target, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Method: "GET", Path: "/x", OKStatus: 200}}}}}
		rs := r.Run(context.Background(), nil)
		var table, js bytes.Buffer
		cm.WriteTable(&table, rs)
		if err := cm.WriteJSON(&js, rs); err != nil {
			t.Fatal(err)
		}
		for _, out := range []string{table.String(), js.String()} {
			if strings.Contains(out, canary) {
				t.Errorf("%s: report leaks the canary:\n%s", name, out)
			}
		}
	}
	for in, want := range map[string]string{
		"http://user:" + canary + "@host:8080/p?q=" + canary: "http://host:8080",
		"https://example.test/a/b":                           "https://example.test",
		"not a url":                                          "<invalid target>",
	} {
		if got := cm.SafeTarget(in); got != want {
			t.Errorf("SafeTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRevokeFailuresAreReportedOnEveryExit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	notEnforcing := false
	// Non-enforcing: the pair would otherwise pass while a long-lived token stays live.
	is := newStubIssuer()
	is.failRevoke = true
	r := stubRunner(t, is, cm.Pair{Name: "p", Target: srv.URL, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}, Enforcing: &notEnforcing}, 1)
	rs := r.Run(context.Background(), nil)
	if cm.AllPassed(rs) || result(rs[0], "cleanup").Result != cm.Fail {
		t.Fatalf("a failed cleanup revoke on the non-enforcing path went unreported: %+v", rs[0].Checks)
	}
	// Accepted check fails (target answers 500): the token is still cleaned up and the failure shows.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	is2 := newStubIssuer()
	is2.failRevoke = true
	r = stubRunner(t, is2, cm.Pair{Name: "p", Target: bad.URL, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}}, 1)
	rs = r.Run(context.Background(), nil)
	if result(rs[0], "accepted").Result != cm.Fail || result(rs[0], "cleanup").Result != cm.Fail {
		t.Fatalf("early exit must still report the revoke result: %+v", rs[0].Checks)
	}
	// Enforcing path: a failed revoke of the wrong-audience token is its own failing check.
	enforcing := true
	is3 := newStubIssuer()
	is3.failRevoke = true
	r = stubRunner(t, is3, cm.Pair{Name: "p", Target: srv.URL, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}, Enforcing: &enforcing}, 1)
	rs = r.Run(context.Background(), nil)
	if result(rs[0], "cleanup_other").Result != cm.Fail {
		t.Fatalf("other-audience cleanup failure not reported: %+v", rs[0].Checks)
	}
	// Healthy issuer: every token is revoked, nothing reports a cleanup failure.
	is4 := newStubIssuer()
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if strings.Contains(tok, "-other-") || is4.isRevoked(tok) {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer rejecting.Close()
	r = stubRunner(t, is4, cm.Pair{Name: "p", Target: rejecting.URL, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}, Enforcing: &enforcing}, 5)
	rs = r.Run(context.Background(), nil)
	if !cm.AllPassed(rs) || result(rs[0], "cleanup").Result == cm.Fail || len(is4.revoked) != 2 {
		t.Fatalf("healthy run: %+v revoked=%v", rs[0].Checks, is4.revoked)
	}
}

func TestOnlyAnAdmissionStatusProvesConnectivity(t *testing.T) {
	for _, bad := range []int{401, 403, 199, 300, 500} {
		c := cm.Config{Issuer: cm.IssuerConfig{Type: "mock", URL: "http://127.0.0.1:1"}, Pairs: []cm.Pair{{
			Name: "p", Target: "http://127.0.0.1:1", Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x", OKStatus: bad}}}}
		if err := c.Validate(); err == nil {
			t.Errorf("ok_status %d accepted", bad)
		}
	}
	// A target that refuses everything fails "accepted" under the default status.
	always401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer always401.Close()
	enforcing := true
	r := stubRunner(t, newStubIssuer(), cm.Pair{Name: "p", Target: always401.URL, Audience: "a", Scopes: []string{"a.read"}, Probe: cm.Probe{Path: "/x"}, Enforcing: &enforcing}, 1)
	rs := r.Run(context.Background(), nil)
	if cm.AllPassed(rs) || result(rs[0], "accepted").Result != cm.Fail {
		t.Fatalf("an always-401 target proved connectivity: %+v", rs[0].Checks)
	}
}

func TestIssuerRefusesRedirectsOffItsOrigin(t *testing.T) {
	var contacted atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { contacted.Add(1) }))
	defer other.Close() // same host, different port
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	cred := filepath.Join(t.TempDir(), "cred")
	if err := os.WriteFile(cred, []byte("op-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	is, err := cm.NewIssuer(cm.IssuerConfig{Type: "http", MintURL: redirector.URL + "/mint", RevokeURL: redirector.URL + "/revoke", CredentialFile: cred}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = is.Mint(context.Background(), cm.MintRequest{Name: "x", Audience: "a", Scopes: []string{"a.read"}})
	if err == nil || strings.Contains(err.Error(), "op-secret") {
		t.Fatalf("redirected mint: %v", err)
	}
	if err := is.Revoke(context.Background(), cm.Minted{ID: "i"}); err == nil {
		t.Fatal("redirected revoke accepted")
	}
	if contacted.Load() != 0 {
		t.Fatalf("the redirect target was contacted %d times", contacted.Load())
	}
}
