package httphost_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hollis-labs/substrate/boot"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// serveReal starts a real listener with the host check on and returns its address.
func serveReal(t *testing.T, cfg string) string {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	api := http.NewServeMux()
	api.HandleFunc("/api/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello " + r.Header.Get("X-Forwarded-For") + r.Header.Get("X-Forwarded-Proto")))
	})
	h, err := httphost.Handler(run.App, httphost.Options{
		Assets: fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}},
		Routes: api,
		Mounts: []httphost.Mount{{Pattern: "/hook/", Handler: ok("hook"), Open: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	bound := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- httphost.Serve(ctx, run.App, h, httphost.ServeOptions{Addr: "127.0.0.1:0", Ready: func(a net.Addr) { bound <- a }})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return (<-bound).String()
}

func get(t *testing.T, addr, host, path string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestRealServerChecksTheHostOnEverySurface(t *testing.T) {
	addr := serveReal(t, `{"httphost":{"allowed_hosts":["app.example.test"]}}`)
	https := map[string]string{"X-Forwarded-Proto": "https"}
	// Every surface the host serves: API route, health, a mount, the SPA, and an unknown
	// path that falls through to the SPA handler.
	for _, path := range []string{"/api/hello", "/healthz", "/hook/x", "/", "/some/route"} {
		if code, body, hdr := get(t, addr, "evil.example", path, https); code != 403 || !strings.Contains(body, `"invalid_host"`) {
			t.Errorf("%s foreign host: %d %s", path, code, body)
		} else if hdr.Get("X-Frame-Options") != "DENY" || hdr.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: refusal lacks the hardening headers", path)
		}
		if code, body, _ := get(t, addr, "app.example.test", path, nil); code != 403 || !strings.Contains(body, `"untrusted_proxy"`) {
			t.Errorf("%s listed host without the proxy proto: %d %s", path, code, body)
		}
		code, _, hdr := get(t, addr, "app.example.test", path, https)
		if code != 200 {
			t.Errorf("%s listed host through the proxy: %d", path, code)
		}
		if hdr.Get("X-Frame-Options") != "DENY" || hdr.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: served response lacks the hardening headers", path)
		}
		if code, _, _ := get(t, addr, addr, path, nil); code != 200 { // Host is 127.0.0.1:port
			t.Errorf("%s loopback host: %d", path, code)
		}
	}
	// What the handler sees: forwarding headers gone, the validated proto kept.
	_, body, _ := get(t, addr, "app.example.test", "/api/hello", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.5"})
	if body != "hello https" {
		t.Errorf("handler saw %q, want the proto kept and X-Forwarded-For gone", body)
	}
	_, body, _ = get(t, addr, addr, "/api/hello", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.5"})
	if body != "hello " {
		t.Errorf("loopback request: handler saw %q, want both headers gone", body)
	}
}

func TestEmptyListServesLoopbackOnly(t *testing.T) {
	addr := serveReal(t, `{}`)
	if code, _, _ := get(t, addr, "localhost", "/healthz", nil); code != 200 {
		t.Errorf("loopback: %d", code)
	}
	if code, body, _ := get(t, addr, "app.example.test", "/healthz", map[string]string{"X-Forwarded-Proto": "https"}); code != 403 || !strings.Contains(body, "invalid_host") {
		t.Errorf("unlisted name: %d %s", code, body)
	}
}

func TestABadAllowedHostFailsStart(t *testing.T) {
	for _, bad := range []string{`["*"]`, `[""]`, `["https://a.example"]`, `"a.example"`} {
		_, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{"httphost":{"allowed_hosts":`+bad+`}}`)
		if err == nil || !strings.Contains(err.Error(), "httphost.allowed_hosts") {
			t.Errorf("%s: start error = %v, want one naming httphost.allowed_hosts", bad, err)
		}
	}
}

func TestServeWarnsOnANonLoopbackBindWithNoAllowedHosts(t *testing.T) {
	logs := captureLogs(t)
	serveBriefly(t, `{"auth":{"mode":"off"}}`) // 127.0.0.1: no warning
	if strings.Contains(logs.String(), "allowed_hosts") {
		t.Fatalf("loopback bind warned: %s", logs)
	}
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	bound := make(chan net.Addr, 1)
	go func() {
		done <- httphost.Serve(ctx, run.App, http.NotFoundHandler(), httphost.ServeOptions{Addr: "0.0.0.0:0", Ready: func(a net.Addr) { bound <- a }})
	}()
	<-bound
	cancel()
	<-done
	if !strings.Contains(logs.String(), "httphost.allowed_hosts") {
		t.Fatalf("no warning for a wildcard bind with no list: %s", logs)
	}
}
