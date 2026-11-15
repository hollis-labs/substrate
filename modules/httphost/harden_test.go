package httphost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/httphost"
)

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
	h := httphost.Harden(nil, ok("ok"), httphost.HardenOptions{AllowedHosts: []string{"svc.example", "other.example:8443"}})
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
	if w := serveH(httphost.Harden(nil, ok("ok"), httphost.HardenOptions{}), hreq("GET", "/x", "attacker.invalid", nil)); w.Code != 200 {
		t.Errorf("no host check configured: %d", w.Code)
	}
}

func TestHardenOriginCheck(t *testing.T) {
	h := httphost.Harden(nil, ok("ok"), httphost.HardenOptions{AllowedOrigins: []string{"https://ui.example/"}})
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
	h := httphost.Harden(nil, echo, httphost.HardenOptions{MaxBodyBytes: 1024})
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
	d := httphost.Harden(nil, echo, httphost.HardenOptions{})
	if w := serveH(d, hreq("POST", "/x", "", bytes.NewReader(make([]byte, 9<<20)))); w.Code != 413 {
		t.Errorf("default limit, 9 MiB: %d", w.Code)
	}
	// Negative is no limit.
	n := httphost.Harden(nil, echo, httphost.HardenOptions{MaxBodyBytes: -1})
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
	h := httphost.Harden(nil, boom, httphost.HardenOptions{Logger: logger})
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
	h = httphost.Harden(nil, boom, httphost.HardenOptions{Logger: logger, ErrorBody: func(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
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
	h := httphost.Harden(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("made"))
	}), httphost.HardenOptions{AccessLog: true, Logger: logger})
	r := hreq("POST", "/api/things?access_token=qtok-secret-1&token=qtok-secret-2&api_key=qtok-secret-3&page=2", "", nil)
	r.Header.Set("Authorization", "Bearer hdr-secret-bearer")
	r.Header.Set("Cookie", "session=cookie-secret-value")
	serveH(h, r)
	out := logs.String()
	for _, secret := range []string{"qtok-secret-1", "qtok-secret-2", "qtok-secret-3", "hdr-secret-bearer", "cookie-secret-value"} {
		if strings.Contains(out, secret) {
			t.Errorf("access log carries %q: %s", secret, out)
		}
	}
	for _, want := range []string{"method=POST", "/api/things", "page=2", "status=201", "bytes=4"} {
		if !strings.Contains(out, want) {
			t.Errorf("access log lacks %q: %s", want, out)
		}
	}
	// Off by default.
	logs.Reset()
	serveH(httphost.Harden(nil, ok("ok"), httphost.HardenOptions{Logger: logger}), hreq("GET", "/x", "", nil))
	if strings.Contains(logs.String(), "http request") {
		t.Errorf("access log written without AccessLog: %s", logs.String())
	}
}

func TestHardenScheme(t *testing.T) {
	var got string
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = httphost.Scheme(r) })
	for _, c := range []struct {
		trust bool
		hdr   []string
		want  string
	}{
		{false, []string{"https"}, "http"},
		{true, []string{"https"}, "https"},
		{true, []string{"https", "http"}, "http"}, // more than one: not a single overwriting proxy
		{true, nil, "http"},
	} {
		r := hreq("GET", "/x", "", nil)
		for _, v := range c.hdr {
			r.Header.Add("X-Forwarded-Proto", v)
		}
		serveH(httphost.Harden(nil, h, httphost.HardenOptions{TrustForwardedProto: c.trust}), r)
		if got != c.want {
			t.Errorf("trust=%v %v: %q, want %q", c.trust, c.hdr, got, c.want)
		}
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
