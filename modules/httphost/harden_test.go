package httphost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/httphost"
)

func mustHarden(t *testing.T, h http.Handler, o httphost.HardenOptions) http.Handler {
	t.Helper()
	hh, err := httphost.Harden(nil, h, o)
	if err != nil {
		t.Fatal(err)
	}
	return hh
}

func hreq(method, target, host string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	if host != "" {
		r.Host = host
	}
	return r
}

func serveH(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func flat(t *testing.T, w *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var b struct{ Code, Message string }
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.Code == "" {
		t.Fatalf("not the flat error body: %q", w.Body.String())
	}
	return b.Code, b.Message
}

func TestHardenHostCheck(t *testing.T) {
	h := mustHarden(t, ok("ok"), httphost.HardenOptions{AllowedHosts: []string{"svc.example", "other.example:8443"}})
	for host, want := range map[string]int{
		"localhost:8080":        200,
		"app.localhost":         200,
		"127.0.0.1:9":           200,
		"[::1]:8080":            200,
		"svc.example":           200,
		"svc.example:1234":      200, // a port-less entry matches any port
		"other.example:8443":    200,
		"other.example:80":      403,
		"attacker.invalid":      403,
		"attacker.invalid:8080": 403,
		"localhost.attacker":    403,
		"":                      403,
	} {
		w := serveH(h, hreq("GET", "/x", host, nil))
		if w.Code != want {
			t.Errorf("Host %q: %d, want %d", host, w.Code, want)
		}
		if want == 403 {
			if code, _ := flat(t, w); code != "host_not_allowed" {
				t.Errorf("Host %q: code %q", host, code)
			}
		}
	}
	// Off by default: any Host passes.
	if w := serveH(mustHarden(t, ok("ok"), httphost.HardenOptions{}), hreq("GET", "/x", "attacker.invalid", nil)); w.Code != 200 {
		t.Errorf("no host check configured: %d", w.Code)
	}
}

func TestHardenOriginCheck(t *testing.T) {
	h := mustHarden(t, ok("ok"), httphost.HardenOptions{AllowedOrigins: []string{"https://ui.example/"}})
	for _, c := range []struct {
		method, origin string
		want           int
	}{
		{"POST", "", 200},
		{"POST", "http://localhost:8080", 200}, // own host
		{"POST", "https://ui.example", 200},
		{"POST", "https://evil.example", 403},
		{"DELETE", "null", 403},
		{"GET", "https://evil.example", 200}, // a safe method is not a cross-site write
	} {
		r := hreq(c.method, "/x", "localhost:8080", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if w := serveH(h, r); w.Code != c.want {
			t.Errorf("%s Origin %q: %d, want %d", c.method, c.origin, w.Code, c.want)
		}
	}
}

func TestHardenBodyLimit(t *testing.T) {
	var readErr error
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		_, _ = w.Write([]byte("read"))
	})
	h := mustHarden(t, echo, httphost.HardenOptions{MaxBodyBytes: 1024})
	// A declared length over the limit is refused before the handler runs.
	w := serveH(h, hreq("POST", "/x", "", bytes.NewReader(make([]byte, 2048))))
	if code, _ := flat(t, w); w.Code != 413 || code != "body_too_large" {
		t.Errorf("declared oversize: %d %q", w.Code, code)
	}
	// An undeclared length (chunked) fails when the handler reads past the limit.
	r := hreq("POST", "/x", "", io.NopCloser(bytes.NewReader(make([]byte, 2048))))
	r.ContentLength = -1
	if w := serveH(h, r); w.Code != 413 || readErr == nil {
		t.Errorf("undeclared oversize: %d, read error %v", w.Code, readErr)
	}
	if w := serveH(h, hreq("POST", "/x", "", bytes.NewReader(make([]byte, 512)))); w.Code != 200 {
		t.Errorf("within the limit: %d", w.Code)
	}
	// The default limit is DefaultMaxBodyBytes (8 MiB): a 9 MiB declared body is refused.
	d := mustHarden(t, echo, httphost.HardenOptions{})
	if w := serveH(d, hreq("POST", "/x", "", bytes.NewReader(make([]byte, 9<<20)))); w.Code != 413 {
		t.Errorf("default limit, 9 MiB: %d", w.Code)
	}
	// Negative is no limit.
	n := mustHarden(t, echo, httphost.HardenOptions{MaxBodyBytes: -1})
	if w := serveH(n, hreq("POST", "/x", "", bytes.NewReader(make([]byte, 9<<20)))); w.Code != 200 {
		t.Errorf("no limit, 9 MiB: %d", w.Code)
	}
}

func TestHardenRecoversPanicsWithoutLeakingThem(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("db failed: password=fixture-pw-0123456789abcdef")
	})
	h := mustHarden(t, boom, httphost.HardenOptions{Logger: logger})
	w := serveH(h, hreq("GET", "/x", "", nil))
	code, msg := flat(t, w)
	if w.Code != 500 || code != "internal" || !strings.Contains(msg, "ref") {
		t.Fatalf("panic: %d %q %q", w.Code, code, msg)
	}
	if strings.Contains(w.Body.String(), "fixture-pw") {
		t.Errorf("the response leaks the panic value: %s", w.Body.String())
	}
	if !strings.Contains(logs.String(), "panic recovered") || strings.Contains(logs.String(), "fixture-pw-0123456789abcdef") {
		t.Errorf("panic log missing or not redacted: %s", logs.String())
	}
	// A custom envelope for a raw handler.
	h = mustHarden(t, boom, httphost.HardenOptions{Logger: logger, ErrorBody: func(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"` + code + `"}}`))
	}})
	if w := serveH(h, hreq("GET", "/x", "", nil)); w.Code != 500 || w.Body.String() != `{"error":{"code":"internal"}}` {
		t.Errorf("custom ErrorBody: %d %q", w.Code, w.Body.String())
	}
}

func TestHardenAccessLogIsCredentialFree(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := mustHarden(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("made"))
	}), httphost.HardenOptions{AccessLog: true, Logger: logger})
	// key and code are credentials redact.SensitiveName does not know; page is not. No value
	// is logged at all, so none of them can leak.
	r := hreq("POST", "/api/things?access_token=qv-1&key=qv-2&code=qv-3&page=qv-4", "", nil)
	r.Header.Set("Authorization", "Bearer hdr-secret-bearer")
	r.Header.Set("Cookie", "session=cookie-secret-value")
	serveH(h, r)
	out := logs.String()
	for _, secret := range []string{"qv-1", "qv-2", "qv-3", "qv-4", "hdr-secret-bearer", "cookie-secret-value"} {
		if strings.Contains(out, secret) {
			t.Errorf("access log carries %q: %s", secret, out)
		}
	}
	for _, want := range []string{"method=POST", "path=/api/things", "params=access_token,code,key,page", "status=201", "bytes=4"} {
		if !strings.Contains(out, want) {
			t.Errorf("access log lacks %q: %s", want, out)
		}
	}
	// Off by default.
	logs.Reset()
	serveH(mustHarden(t, ok("ok"), httphost.HardenOptions{Logger: logger}), hreq("GET", "/x", "", nil))
	if strings.Contains(logs.String(), "http request") {
		t.Errorf("access log written without AccessLog: %s", logs.String())
	}
}

func TestHardenForwardingHeaders(t *testing.T) {
	var got string
	var seen http.Header
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got, seen = httphost.Scheme(r), r.Header.Clone() })
	for _, c := range []struct {
		name    string
		trusted bool
		peer    string
		xfp     []string
		want    string
	}{
		{"untrusted", false, "127.0.0.1:5000", []string{"https"}, "http"},
		{"trusted loopback", true, "127.0.0.1:5000", []string{"https"}, "https"},
		{"trusted ipv6 loopback", true, "[::1]:5000", []string{"https"}, "https"},
		{"trusted but not a loopback peer", true, "192.0.2.10:5555", []string{"https"}, "http"},
		{"two values", true, "127.0.0.1:5000", []string{"https", "http"}, "http"},
		{"none", true, "127.0.0.1:5000", nil, "http"},
	} {
		r := hreq("GET", "/x", "", nil)
		r.RemoteAddr = c.peer
		for _, v := range c.xfp {
			r.Header.Add("X-Forwarded-Proto", v)
		}
		for _, n := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP"} {
			r.Header.Set(n, "1.2.3.4")
		}
		serveH(mustHarden(t, h, httphost.HardenOptions{TrustedProxy: c.trusted}), r)
		if got != c.want {
			t.Errorf("%s: Scheme %q, want %q", c.name, got, c.want)
		}
		// The handler (identity included) sees X-Forwarded-Proto only when it was believed,
		// and never the other forwarding headers.
		if (seen.Get("X-Forwarded-Proto") != "") != (c.want == "https") {
			t.Errorf("%s: handler saw X-Forwarded-Proto %q", c.name, seen.Values("X-Forwarded-Proto"))
		}
		for _, n := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP"} {
			if seen.Get(n) != "" {
				t.Errorf("%s: handler saw %s", c.name, n)
			}
		}
	}
}

func TestHardenTrustedProxyIsIdentitysSwitch(t *testing.T) {
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{"identity":{"trusted_proxy":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	var got string
	h, err := httphost.Harden(run.App, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = httphost.Scheme(r) }), httphost.HardenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := hreq("GET", "/x", "", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Forwarded-Proto", "https")
	serveH(h, r)
	if got != "https" {
		t.Errorf("identity.trusted_proxy: true: Scheme %q", got)
	}
}

func TestHardenAllowedHostsAreValidated(t *testing.T) {
	for _, bad := range []string{"", "*", "*.example", "https://app.example", "app.example/x", "user@app.example", "app example"} {
		if _, err := httphost.Harden(nil, ok("ok"), httphost.HardenOptions{AllowedHosts: []string{bad}}); err == nil || !strings.Contains(err.Error(), "allowed_hosts") {
			t.Errorf("allowed_hosts %q: %v, want an error naming the key", bad, err)
		}
	}
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{"httphost":{"allowed_hosts":["*"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	if _, err := httphost.Handler(run.App, httphost.Options{}); err == nil || !strings.Contains(err.Error(), "allowed_hosts") {
		t.Errorf("Handler with allowed_hosts [*]: %v", err)
	}
}

// The Host check never applies on a unix-socket listener, decided from the connection.
func TestHardenHostCheckSkipsUnixSockets(t *testing.T) {
	h := mustHarden(t, ok("ok"), httphost.HardenOptions{AllowedHosts: []string{"app.example.test"}})
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets here:", err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	for _, host := range []string{"unix", "attacker.invalid"} {
		resp, err := c.Get("http://" + host + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("unix socket, Host %q: %d, want 200", host, resp.StatusCode)
		}
	}
	// The same handler on TCP still checks.
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	req, _ := http.NewRequest("GET", ts.URL+"/healthz", nil)
	req.Host = "attacker.invalid"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("TCP, Host attacker.invalid: %d, want 403", resp.StatusCode)
	}
}

func TestHandlerAppliesHardenFromConfigOrOptions(t *testing.T) {
	start := func(cfg string) *boot.Running {
		run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = run.Stop(context.Background()) })
		return run
	}
	// Default: no hardening, as before.
	run := start(`{}`)
	h, err := httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w := serveH(h, hreq("GET", "/healthz", "attacker.invalid", nil)); w.Code != 200 {
		t.Errorf("default: %d", w.Code)
	}
	// Config keys under httphost: turn it on.
	run = start(`{"httphost":{"allowed_hosts":["svc.example"],"max_body_bytes":16}}`)
	h, err = httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w := serveH(h, hreq("GET", "/healthz", "attacker.invalid", nil)); w.Code != 403 {
		t.Errorf("config allowed_hosts: %d", w.Code)
	}
	if w := serveH(h, hreq("GET", "/healthz", "svc.example", nil)); w.Code != 200 {
		t.Errorf("config allowed host: %d", w.Code)
	}
	if w := serveH(h, hreq("POST", "/api/x", "svc.example", strings.NewReader(strings.Repeat("a", 32)))); w.Code != 413 {
		t.Errorf("config max_body_bytes: %d", w.Code)
	}
	// access_log alone changes no request: no implicit body limit.
	run = start(`{"httphost":{"access_log":true}}`)
	h, err = httphost.Handler(run.App, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w := serveH(h, hreq("POST", "/api/x", "", bytes.NewReader(make([]byte, 9<<20)))); w.Code == 413 {
		t.Errorf("access_log alone refused a 9 MiB body")
	}
	// Options.Harden turns it on in code.
	run = start(`{}`)
	h, err = httphost.Handler(run.App, httphost.Options{Harden: &httphost.HardenOptions{CheckHost: true}})
	if err != nil {
		t.Fatal(err)
	}
	if w := serveH(h, hreq("GET", "/healthz", "attacker.invalid", nil)); w.Code != 403 {
		t.Errorf("Options.Harden CheckHost: %d", w.Code)
	}
}

// Turned on by config alone, Harden runs only what is configured: with access_log and nothing
// else, forwarding headers, a panic and a large body behave as without Harden.
func TestAccessLogAloneChangesNoRequest(t *testing.T) {
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{"httphost":{"access_log":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	var seen http.Header
	var read int
	h, err := httphost.Handler(run.App, httphost.Options{Mounts: []httphost.Mount{
		{Pattern: "/echo", Open: true, Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = r.Header.Clone()
			b, _ := io.ReadAll(r.Body)
			read = len(b)
		})},
		{Pattern: "/boom", Open: true, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })},
	}})
	if err != nil {
		t.Fatal(err)
	}
	r := hreq("POST", "/echo", "", bytes.NewReader(make([]byte, 9<<20)))
	r.RemoteAddr = "192.0.2.10:5555"
	for _, n := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Real-IP", "Forwarded"} {
		r.Header.Set(n, "v")
	}
	serveH(h, r)
	for _, n := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Real-IP", "Forwarded"} {
		if seen.Get(n) != "v" {
			t.Errorf("%s did not reach the handler", n)
		}
	}
	if read != 9<<20 {
		t.Errorf("read %d bytes of a 9 MiB body", read)
	}
	// No panic guard: the panic reaches net/http as it would without Harden.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was recovered with only access_log configured")
			}
		}()
		serveH(h, hreq("GET", "/boom", "", nil))
	}()
}
