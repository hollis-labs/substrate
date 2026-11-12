package httphost_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// nonLoopbackIP returns an address of this machine that is not loopback, or "" when none.
func nonLoopbackIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return ""
}

// serveRealOn starts a real listener bound to addr and returns the bound address.
func serveRealOn(t *testing.T, cfg, addr string) string {
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
	h, err := httphost.Handler(run.App, httphost.Options{Routes: api})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	bound := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- httphost.Serve(ctx, run.App, h, httphost.ServeOptions{Addr: addr, Ready: func(a net.Addr) { bound <- a }})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return (<-bound).String()
}

// A listed direct host is served on a non-loopback bind to a non-loopback peer: the client
// dials this machine's own interface address, so the peer the server sees is not loopback.
// Nothing a client sends is believed for it.
func TestRealServerServesADirectHostToANonLoopbackPeer(t *testing.T) {
	ip := nonLoopbackIP()
	if ip == "" {
		t.Skip("no non-loopback interface address on this machine")
	}
	bound := serveRealOn(t, `{"httphost":{"direct_hosts":["tail.example.test"],"allowed_hosts":["app.example.test"]}}`, ip+":0")
	_, port, _ := net.SplitHostPort(bound)
	addr := net.JoinHostPort(ip, port)
	spoof := map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.5"}
	for _, path := range []string{"/api/hello", "/healthz"} {
		if code, _, _ := get(t, addr, "tail.example.test", path, nil); code != 200 {
			t.Errorf("%s direct host, non-loopback peer: %d", path, code)
		}
		if code, body, _ := get(t, addr, "evil.example", path, nil); code != 403 || !strings.Contains(body, `"invalid_host"`) {
			t.Errorf("%s foreign host: %d %s", path, code, body)
		}
		// The proxied name is still refused from this non-loopback peer.
		if code, body, _ := get(t, addr, "app.example.test", path, spoof); code != 403 || !strings.Contains(body, `"untrusted_proxy"`) {
			t.Errorf("%s allowed host from a non-loopback peer: %d %s", path, code, body)
		}
	}
	if _, body, _ := get(t, addr, "tail.example.test", "/api/hello", spoof); body != "hello " {
		t.Errorf("direct host: handler saw %q, want every forwarding header gone", body)
	}
}

func TestRealServerDirectHostOnALoopbackBind(t *testing.T) {
	addr := serveReal(t, `{"httphost":{"direct_hosts":["tail.example.test"]}}`)
	if code, _, _ := get(t, addr, "tail.example.test", "/healthz", nil); code != 200 {
		t.Errorf("direct host: %d", code)
	}
	if code, body, _ := get(t, addr, "tail.example.test:1", "/healthz", nil); code != 403 || !strings.Contains(body, "invalid_host") {
		t.Errorf("direct host with a port: %d %s", code, body)
	}
}

func TestABadDirectHostFailsStart(t *testing.T) {
	for _, bad := range []string{`["*"]`, `[""]`, `["https://a.example"]`, `"a.example"`, `["a.example/x"]`} {
		_, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{"httphost":{"direct_hosts":`+bad+`}}`)
		if err == nil || !strings.Contains(err.Error(), "httphost.direct_hosts") {
			t.Errorf("%s: start error = %v, want one naming httphost.direct_hosts", bad, err)
		}
	}
	_, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{"httphost":{"allowed_hosts":["a.example"],"direct_hosts":["A.example"]}}`)
	if err == nil || !strings.Contains(err.Error(), "direct_hosts") {
		t.Errorf("overlap: start error = %v", err)
	}
}

func TestServeDoesNotWarnOnANonLoopbackBindWithDirectHosts(t *testing.T) {
	logs := captureLogs(t)
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{"httphost":{"direct_hosts":["tail.example.test"]}}`)
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
	if strings.Contains(logs.String(), "allowed_hosts") {
		t.Fatalf("warned although direct_hosts is set: %s", logs)
	}
}
