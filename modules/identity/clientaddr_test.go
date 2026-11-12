package identity_test

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
)

// startBehindProxy is an app whose httphost lists demo.example, so a local TLS proxy can
// forward that name.
func startBehindProxy(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
		"database": {"sqlite": {"path": %q}},
		"auth": {"mode": "enforce"},
		"httphost": {"allowed_hosts": ["demo.example"]},
		"identity": {"key_file": %q, "bcrypt_cost": 4, "trusted_proxy": true}
	}`, filepath.Join(dir, "t.db"), filepath.Join(dir, "id.key"))
	run, err := boot.Start(context.Background(), "demo", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	inner, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	svc := identity.From(run.App)
	return &app{t: t, h: svc.Middleware(inner), dir: dir, svc: svc, running: run}
}

// viaProxy makes the request look like the local TLS proxy forwarded it: the listed host,
// a loopback peer, one https proto, and the X-Forwarded-For values given (one header
// line each).
func viaProxy(xff ...string) opt {
	return func(r *http.Request) {
		r.Host = "demo.example"
		r.RemoteAddr = "127.0.0.1:5555"
		r.Header.Set("X-Forwarded-Proto", "https")
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
	}
}

func (a *app) loginWith(pw string, opts ...opt) int {
	return a.do("POST", "/api/login", map[string]string{"username": "owner", "password": pw}, opts...).Code
}

func TestLoginLimiterKeysOnTheClientBehindALocalProxy(t *testing.T) {
	a := startBehindProxy(t)
	a.owner()
	// Five failures from client A (the proxy appends the peer it saw) lock A out ...
	for i := 0; i < 5; i++ {
		if got := a.loginWith("wrong password "+fmt.Sprint(i), viaProxy("203.0.113.5")); got != 401 {
			t.Fatalf("failure %d: %d", i, got)
		}
	}
	if got := a.loginWith(password, viaProxy("203.0.113.5")); got != 429 {
		t.Fatalf("client A after five failures: %d, want 429", got)
	}
	// ... and no one else: client B behind the same proxy still signs in.
	if got := a.loginWith(password, viaProxy("203.0.113.6")); got != 200 {
		t.Fatalf("client B shares client A's bucket: %d, want 200", got)
	}
}

func TestLoginLimiterTakesTheLastHopSoTheFirstCannotBeChosen(t *testing.T) {
	a := startBehindProxy(t)
	a.owner()
	// The client prefixes a different made-up first hop each time; the proxy's own
	// entry is last. Rotating the first hop must not reset the count.
	for i := 0; i < 5; i++ {
		a.loginWith("wrong password "+fmt.Sprint(i), viaProxy(fmt.Sprintf("198.51.100.%d, 203.0.113.5", i)))
	}
	if got := a.loginWith(password, viaProxy("198.51.100.99, 203.0.113.5")); got != 429 {
		t.Fatalf("rotating the first hop escaped the limiter: %d, want 429", got)
	}
	// Two header lines: the last line's last hop counts.
	if got := a.loginWith(password, viaProxy("203.0.113.5", "203.0.113.7")); got != 200 {
		t.Fatalf("last line's last hop should be a fresh client: %d, want 200", got)
	}
}

func TestLoginLimiterFallsBackToThePeerForAMalformedOrUntrustedHeader(t *testing.T) {
	a := startBehindProxy(t)
	a.owner()
	// Malformed last hops (a name, a host:port, empty, a bracketed or zoned address) fall
	// back to the peer, so they all share the one peer bucket and cannot dodge the limit.
	bad := []string{"not-an-ip", "203.0.113.5:80", "", "[::1]", "fe80::1%eth0", "203.0.113.5,"}
	for i, v := range bad {
		a.loginWith("wrong password "+fmt.Sprint(i), viaProxy(v))
	}
	a.loginWith("wrong password x", viaProxy("also-bad"))
	if got := a.loginWith(password, viaProxy("zzz")); got != 429 {
		t.Fatalf("malformed headers did not share the peer bucket: %d, want 429", got)
	}

	// A loopback Host is not a validated proxy request: its X-Forwarded-For is ignored,
	// so a header cannot move a request into a fresh bucket.
	b := startBehindProxy(t)
	b.owner()
	for i := 0; i < 5; i++ {
		b.loginWith("wrong password "+fmt.Sprint(i), func(r *http.Request) {
			r.RemoteAddr = "127.0.0.1:1"
			r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		})
	}
	got := b.loginWith(password, func(r *http.Request) {
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
	})
	if got != 429 {
		t.Fatalf("a header on a loopback request chose the bucket: %d, want 429", got)
	}
}

// trusted_proxy believes X-Forwarded-Proto; the host check leaves it only on a request it
// validated, so only that request gets a Secure cookie.
func TestSecureCookieOnlyForAValidatedProxyRequest(t *testing.T) {
	a := startBehindProxy(t)
	a.owner()
	secure := func(opts ...opt) bool {
		w := a.do("POST", "/api/login", map[string]string{"username": "owner", "password": password}, opts...)
		if w.Code != 200 {
			t.Fatalf("login: %d %s", w.Code, w.Body)
		}
		c := w.cookie("demo_session")
		if c == nil {
			t.Fatal("no session cookie")
		}
		return c.Secure
	}
	if !secure(viaProxy("203.0.113.5")) {
		t.Error("a validated proxy request should get a Secure cookie")
	}
	if secure(func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }) {
		t.Error("a loopback request claiming https must not get a Secure cookie")
	}
}
