package httphost_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/modules/httphost"
)

func unixClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
}

func TestServeUnixListener(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := httphost.ListenUnix(sock, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", sse(true, ended))
	mux.HandleFunc("/who", func(w http.ResponseWriter, r *http.Request) {
		uid, _, pid, ok := httphost.PeerCredentials(r)
		_, _ = fmt.Fprintf(w, "%v %d %d %v", ok, uid, pid, r.Context().Value(ctxKey{}))
	})
	app, stopApp := serveApp(t, `{"auth":{"mode":"off"}}`)
	defer stopApp()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan net.Addr, 1)
	go func() {
		done <- httphost.Serve(ctx, app, mux, httphost.ServeOptions{
			Listener: ln, Ready: func(a net.Addr) { ready <- a }, GracePeriod: 2 * time.Second,
			ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
				return context.WithValue(ctx, ctxKey{}, "custom")
			},
		})
	}()
	<-ready
	c := unixClient(sock)

	resp, err := c.Get("http://u/who")
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	var uid, pid int
	var custom string
	_, _ = fmt.Fscan(resp.Body, &ok, &uid, &pid, &custom)
	_ = resp.Body.Close()
	if custom != "custom" {
		t.Fatalf("ConnContext value missing: %q", custom)
	}
	if runtime.GOOS == "linux" && (!ok || uid != os.Getuid() || pid != os.Getpid()) {
		t.Fatalf("peer creds = %v uid %d pid %d, want true %d %d", ok, uid, pid, os.Getuid(), os.Getpid())
	}

	sresp, err := c.Get("http://u/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sresp.Body.Close() }()
	stop()
	select {
	case why := <-ended:
		if why != "stopping" {
			t.Fatalf("SSE ended by %q, want stopping", why)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SSE handler did not see Stopping on the unix listener")
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Fatal("socket file left behind after clean stop")
	}
}

type ctxKey struct{}

// A unix: address without a Listener is refused with a message naming Listener, from
// ServeOptions.Addr or config httphost.addr, instead of a TCP lookup error.
func TestServeUnixAddrPointsAtListener(t *testing.T) {
	for _, tc := range []struct{ name, cfg, addr string }{
		{"option", `{"auth":{"mode":"permissive"}}`, "unix:/tmp/x.sock"},
		{"config", `{"auth":{"mode":"permissive"},"httphost":{"addr":"unix:/tmp/x.sock"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, stopApp := serveApp(t, tc.cfg)
			defer stopApp()
			err := httphost.Serve(context.Background(), app, http.NotFoundHandler(), httphost.ServeOptions{Addr: tc.addr})
			if err == nil || !strings.Contains(err.Error(), "ServeOptions.Listener") {
				t.Fatalf("Serve = %v, want an error naming ServeOptions.Listener", err)
			}
		})
	}
}
