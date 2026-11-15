package httphost_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// The bind is checked against the listener's AuthMode, not the app's.
func TestServeAuthModeChecksTheBind(t *testing.T) {
	key := "am-key"
	keys := `,"api_keys":[{"id":"svc/a","hash":"` + libauth.HashKey(key) + `","scopes":["t.read"]}]`

	// An enforcing app may not open a permissive listener on a wildcard address.
	enf, stopEnf := serveApp(t, `{"auth":{"mode":"enforce"`+keys+`}}`)
	defer stopEnf()
	err := httphost.Serve(context.Background(), enf, http.NotFoundHandler(), httphost.ServeOptions{Addr: "0.0.0.0:0", AuthMode: libauth.ModePermissive})
	if err == nil || !strings.Contains(err.Error(), "use enforce") {
		t.Fatalf("permissive listener on 0.0.0.0 in an enforcing app: %v", err)
	}

	// A permissive app may open an enforcing listener on a wildcard address.
	perm, stopPerm := serveApp(t, `{"auth":{"mode":"permissive"`+keys+`}}`)
	defer stopPerm()
	if err := httphost.Serve(context.Background(), perm, http.NotFoundHandler(), httphost.ServeOptions{Addr: "0.0.0.0:0"}); err == nil {
		t.Fatal("app-mode (permissive) listener on 0.0.0.0 accepted")
	}
	ctx, stop := context.WithCancel(context.Background())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- httphost.Serve(ctx, perm, http.NotFoundHandler(), httphost.ServeOptions{
			Addr: "0.0.0.0:0", AuthMode: libauth.ModeEnforce, Ready: func(a net.Addr) { ready <- a }, GracePeriod: time.Second,
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("enforcing listener on 0.0.0.0 in a permissive app: %v", err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeAuthModeRefusals(t *testing.T) {
	// Enforce with no verifier configured.
	t.Setenv("HOLLIS_AUTH", "")
	app, stopApp := serveApp(t, `{"auth":{"mode":"permissive"}}`)
	defer stopApp()
	err := httphost.Serve(context.Background(), app, http.NotFoundHandler(), httphost.ServeOptions{Addr: "127.0.0.1:0", AuthMode: libauth.ModeEnforce})
	if err == nil || !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("enforce listener without a verifier: %v", err)
	}
	// An unknown mode.
	if err := httphost.Serve(context.Background(), app, http.NotFoundHandler(), httphost.ServeOptions{Addr: "127.0.0.1:0", AuthMode: "loose"}); err == nil {
		t.Fatal("unknown AuthMode accepted")
	}
	// No auth module: a listener mode cannot be honoured, so Serve refuses rather than
	// serving unguarded. The listener it was given is closed.
	run, err := boot.Start(context.Background(), "t", []string{"health", "httphost"}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	err = httphost.Serve(context.Background(), run.App, http.NotFoundHandler(), httphost.ServeOptions{Listener: ln, AuthMode: libauth.ModeEnforce})
	if err == nil || !strings.Contains(err.Error(), "auth module") {
		t.Fatalf("AuthMode without the auth module: %v", err)
	}
	if _, err := ln.Accept(); err == nil {
		t.Fatal("listener left open after a refusal")
	}
}
