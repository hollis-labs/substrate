package httphost_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/modules/httphost"
)

// serveUntilReady runs Serve and returns once the listener is up, or Serve's error.
func serveUntilReady(t *testing.T, ctx context.Context, h http.Handler, cfg string, o httphost.ServeOptions) (net.Addr, <-chan error, error) {
	t.Helper()
	app, stopApp := serveApp(t, cfg)
	t.Cleanup(stopApp)
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	o.Ready = func(a net.Addr) { ready <- a }
	if o.GracePeriod == 0 {
		o.GracePeriod = time.Second
	}
	go func() { done <- httphost.Serve(ctx, app, h, o) }()
	select {
	case a := <-ready:
		return a, done, nil
	case err := <-done:
		return nil, nil, err
	}
}

func listenUnix(t *testing.T, perm os.FileMode) (string, net.Listener) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := httphost.ListenUnix(sock, perm)
	if err != nil {
		t.Fatal(err)
	}
	return sock, ln
}

func TestListenUnixSetsPermsAndCleansUp(t *testing.T) {
	for _, perm := range []os.FileMode{0o600, 0o660} {
		sock, ln := listenUnix(t, perm)
		fi, err := os.Lstat(sock)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Type() != os.ModeSocket || fi.Mode().Perm() != perm {
			t.Fatalf("socket mode %v, want socket %v", fi.Mode(), perm)
		}
		if a := ln.Addr().(*net.UnixAddr); a.Name != sock {
			t.Fatalf("Addr %q, want %q", a.Name, sock)
		}
		// Only the socket is left in the directory: the private bind directory is gone.
		if ents, _ := os.ReadDir(filepath.Dir(sock)); len(ents) != 1 {
			t.Fatalf("directory holds %d entries, want only the socket", len(ents))
		}
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_ = c.Close()
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("socket left after Close: %v", err)
		}
	}
	if _, err := httphost.ListenUnix(filepath.Join(t.TempDir(), "s"), os.ModeSetuid|0o600); err == nil {
		t.Fatal("perm beyond 0777 accepted")
	}
}

func TestListenUnixStaleLiveAndNonSocket(t *testing.T) {
	dir := t.TempDir()
	// A stale socket (nobody listening) is replaced.
	stale := filepath.Join(dir, "stale.sock")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	_ = old.Close()
	ln, err := httphost.ListenUnix(stale, 0o600)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	// A live socket is refused and left working.
	if _, err := httphost.ListenUnix(stale, 0o600); err == nil || !strings.Contains(err.Error(), "listening") {
		t.Fatalf("live socket: %v", err)
	}
	if c, err := net.Dial("unix", stale); err != nil {
		t.Fatalf("live socket broken by the refused ListenUnix: %v", err)
	} else {
		_ = c.Close()
	}
	_ = ln.Close()
	// A regular file is refused and not removed.
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := httphost.ListenUnix(file, 0o600); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("regular file: %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("regular file removed: %v", err)
	}
}

// Off and permissive refuse a socket other users can connect to; enforce accepts it.
func TestServeRefusesAnOtherWritableSocketUnlessEnforcing(t *testing.T) {
	for _, mode := range []string{"off", "permissive"} {
		sock, ln := listenUnix(t, 0o666)
		_, _, err := serveUntilReady(t, context.Background(), http.NotFoundHandler(), `{"auth":{"mode":"`+mode+`"}}`, httphost.ServeOptions{Listener: ln})
		if err == nil || !strings.Contains(err.Error(), "writable by other users") {
			t.Fatalf("%s: o+w socket: %v", mode, err)
		}
		if err := ln.Close(); err == nil {
			t.Fatalf("%s: refused listener left open", mode)
		}
		if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: refused socket file left behind", mode)
		}
	}
	// 0660 (group may connect) is accepted in permissive.
	_, ln := listenUnix(t, 0o660)
	ctx, stop := context.WithCancel(context.Background())
	_, done, err := serveUntilReady(t, ctx, http.NotFoundHandler(), `{"auth":{"mode":"permissive"}}`, httphost.ServeOptions{Listener: ln})
	if err != nil {
		t.Fatalf("0660 socket in permissive: %v", err)
	}
	stop()
	<-done
	_, ln = listenUnix(t, 0o666)
	ctx, stop = context.WithCancel(context.Background())
	defer stop()
	_, done, err = serveUntilReady(t, ctx, http.NotFoundHandler(), `{"auth":{"mode":"enforce","api_keys":[{"id":"svc/u","hash":"00","scopes":["t.read"]}]}}`, httphost.ServeOptions{Listener: ln})
	if err != nil {
		t.Fatalf("o+w socket in enforce: %v", err)
	}
	stop()
	<-done
}

func TestServeRefusesAnAbstractSocketUnlessEnforcing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("abstract unix sockets are Linux only")
	}
	ln, err := net.Listen("unix", fmt.Sprintf("@substrate-test-%d", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = serveUntilReady(t, context.Background(), http.NotFoundHandler(), `{"auth":{"mode":"permissive"}}`, httphost.ServeOptions{Listener: ln})
	if err == nil || !strings.Contains(err.Error(), "abstract") {
		t.Fatalf("abstract socket in permissive: %v", err)
	}
	if err := ln.Close(); err == nil {
		t.Fatal("refused listener left open")
	}
}

// The security path of Listener: a wildcard TCP listener is refused in permissive, and
// Serve closes it.
func TestServeClosesARefusedWildcardListener(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = serveUntilReady(t, context.Background(), http.NotFoundHandler(), `{"auth":{"mode":"permissive"}}`, httphost.ServeOptions{Listener: ln})
	if err == nil || !strings.Contains(err.Error(), "use enforce") {
		t.Fatalf("wildcard listener in permissive: %v", err)
	}
	// A second Close fails with net.ErrClosed only if Serve closed it (Accept would block).
	if err := ln.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Close after refusal = %v, want net.ErrClosed", err)
	}
}

// recordingListener remembers every conn its Accept returned.
type recordingListener struct {
	net.Listener
	mu       sync.Mutex
	accepted []net.Conn
}

func (l *recordingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.accepted = append(l.accepted, c)
		l.mu.Unlock()
	}
	return c, err
}

func (l *recordingListener) was(c net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, a := range l.accepted {
		if a == c {
			return true
		}
	}
	return false
}

// ConnContext receives exactly the conn Accept returned: the same value (identity), and
// so the concrete type (*net.UnixConn on a socket, *net.TCPConn on TCP). PeerCredentials
// is ok only on the socket.
func TestServeConnContextGetsTheAcceptedConn(t *testing.T) {
	_, uln := listenUnix(t, 0o600)
	tln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, typ string
		ln        net.Listener
		peerOK    bool
	}{
		{"unix", "*net.UnixConn", uln, runtime.GOOS == "linux" || runtime.GOOS == "darwin"},
		{"tcp", "*net.TCPConn", tln, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rl := &recordingListener{Listener: tc.ln}
			type seen struct {
				typ  string
				same bool
			}
			got := make(chan seen, 4)
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _, _, ok := httphost.PeerCredentials(r)
				_, _ = fmt.Fprint(w, ok)
			})
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			addr, done, err := serveUntilReady(t, ctx, h, `{"auth":{"mode":"off"}}`, httphost.ServeOptions{
				Listener: rl,
				ConnContext: func(ctx context.Context, c net.Conn) context.Context {
					got <- seen{fmt.Sprintf("%T", c), rl.was(c)}
					return ctx
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			c := http.DefaultClient
			url := "http://" + addr.String()
			if tc.name == "unix" {
				c, url = unixClient(addr.String()), "http://u"
			}
			resp, err := c.Get(url + "/")
			if err != nil {
				t.Fatal(err)
			}
			var ok bool
			_, _ = fmt.Fscan(resp.Body, &ok)
			_ = resp.Body.Close()
			if s := <-got; s.typ != tc.typ || !s.same {
				t.Fatalf("ConnContext got %s (same as accepted: %v), want %s, same", s.typ, s.same, tc.typ)
			}
			if ok != tc.peerOK {
				t.Fatalf("PeerCredentials ok = %v, want %v", ok, tc.peerOK)
			}
			stop()
			if err := <-done; err != nil {
				t.Fatalf("Serve: %v", err)
			}
		})
	}
}
