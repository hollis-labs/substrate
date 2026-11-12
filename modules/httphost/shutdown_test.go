package httphost_test

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/modules/httphost"
)

// sse streams an event a few times a second until the client leaves or, when watch is
// set, the server begins to stop. It reports the reason it ended.
func sse(watch bool, ended chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte("data: hello\n\n"))
		fl.Flush()
		var stop <-chan struct{}
		if watch {
			stop = httphost.Stopping(r)
		}
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				ended <- "stopping"
				return
			case <-r.Context().Done():
				ended <- "context"
				return
			case <-tick.C:
				_, _ = w.Write([]byte("data: tick\n\n"))
				fl.Flush()
			}
		}
	}
}

// echo hijacks the connection like a WebSocket upgrade and echoes bytes back.
func echo(watch bool, ended chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n\r\n")
		_ = rw.Flush()
		go func() {
			buf := make([]byte, 256)
			for {
				n, err := rw.Read(buf)
				if err != nil {
					return
				}
				_, _ = conn.Write(buf[:n])
			}
		}()
		var stop <-chan struct{}
		if watch {
			stop = httphost.Stopping(r)
		}
		select {
		case <-stop:
			ended <- "stopping"
		case <-r.Context().Done():
			ended <- "context"
		}
	}
}

type server struct {
	addr   string
	cancel context.CancelFunc
	done   chan error
}

func serveHandler(t *testing.T, h http.Handler, grace time.Duration) *server {
	t.Helper()
	app, stop := serveApp(t, `{"auth":{"mode":"off"}}`)
	t.Cleanup(stop)
	ctx, cancel := context.WithCancel(context.Background())
	bound := make(chan net.Addr, 1)
	s := &server{cancel: cancel, done: make(chan error, 1)}
	go func() {
		s.done <- httphost.Serve(ctx, app, h, httphost.ServeOptions{Addr: "127.0.0.1:0", GracePeriod: grace, Ready: func(a net.Addr) { bound <- a }})
	}()
	s.addr = (<-bound).String()
	t.Cleanup(cancel)
	return s
}

func muxOf(paths map[string]http.HandlerFunc) *http.ServeMux {
	m := http.NewServeMux()
	for p, h := range paths {
		m.HandleFunc(p, h)
	}
	return m
}

// openSSE connects and waits for the first event.
func openSSE(t *testing.T, addr string) (*http.Response, *bufio.Reader) {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(resp.Body)
	if line, err := br.ReadString('\n'); err != nil || !strings.HasPrefix(line, "data:") {
		t.Fatalf("first event: %q %v", line, err)
	}
	return resp, br
}

// openEcho upgrades and checks one echo round trip.
func openEcho(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("GET /ws HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	br := bufio.NewReader(c)
	if line, err := br.ReadString('\n'); err != nil || !strings.Contains(line, "101") {
		t.Fatalf("upgrade: %q %v", line, err)
	}
	_, _ = br.ReadString('\n') // blank line
	_, _ = c.Write([]byte("ping\n"))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if line, err := br.ReadString('\n'); err != nil || line != "ping\n" {
		t.Fatalf("echo: %q %v", line, err)
	}
	return c, br
}

func waitServe(t *testing.T, s *server, within time.Duration) (time.Duration, error) {
	t.Helper()
	start := time.Now()
	select {
	case err := <-s.done:
		return time.Since(start), err
	case <-time.After(within):
		t.Fatalf("Serve did not return within %v", within)
		return 0, nil
	}
}

func TestServeEndsCleanlyWhenStreamingAndHijackedHandlersWatchStopping(t *testing.T) {
	ended := make(chan string, 2)
	s := serveHandler(t, muxOf(map[string]http.HandlerFunc{"/sse": sse(true, ended), "/ws": echo(true, ended)}), 5*time.Second)
	resp, br := openSSE(t, s.addr)
	defer func() { _ = resp.Body.Close() }()
	conn, cbr := openEcho(t, s.addr)
	defer func() { _ = conn.Close() }()

	s.cancel()
	took, err := waitServe(t, s, 3*time.Second)
	if err != nil {
		t.Fatalf("Serve = %v, want nil", err)
	}
	if took > time.Second {
		t.Errorf("Serve took %v, want it prompt", took)
	}
	for i := 0; i < 2; i++ {
		if why := <-ended; why != "stopping" {
			t.Errorf("a handler ended by %q, want stopping", why)
		}
	}
	// The client sees both connections end.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := cbr.ReadString('\n'); err == nil {
		t.Error("the echo connection stayed open")
	}
	for {
		if _, err := br.ReadString('\n'); err != nil {
			break
		}
	}
}

// A handler that ignores Stopping holds Serve for the grace period, then is released by the
// cancelled request context and Serve returns an error. A hijacked connection is the sharp
// case: http.Server.Shutdown does not wait for it at all, so only Serve's own handler count
// holds Serve here.
func TestHandlersThatIgnoreStoppingHoldServeForTheGracePeriodThenAreClosed(t *testing.T) {
	for _, kind := range []string{"streaming", "hijacked", "both"} {
		t.Run(kind, func(t *testing.T) {
			ended := make(chan string, 2)
			want := 1
			s := serveHandler(t, muxOf(map[string]http.HandlerFunc{"/sse": sse(false, ended), "/ws": echo(false, ended)}), 400*time.Millisecond)
			if kind != "hijacked" {
				resp, _ := openSSE(t, s.addr)
				defer func() { _ = resp.Body.Close() }()
			}
			if kind != "streaming" {
				conn, _ := openEcho(t, s.addr)
				defer func() { _ = conn.Close() }()
			}
			if kind == "both" {
				want = 2
			}
			ignoringHandlersAreHeld(t, s, ended, want)
		})
	}
}

func ignoringHandlersAreHeld(t *testing.T, s *server, ended chan string, want int) {
	t.Helper()
	s.cancel()
	took, err := waitServe(t, s, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "still running") || !strings.Contains(err.Error(), "shutdown") {
		t.Fatalf("Serve = %v, want a shutdown error naming handlers still running", err)
	}
	if took < 350*time.Millisecond {
		t.Errorf("Serve gave up after %v, before the grace period", took)
	}
	// After the grace period the request contexts are cancelled and the connections closed,
	// so even the handlers that ignored Stopping end.
	for i := 0; i < want; i++ {
		select {
		case why := <-ended:
			if why != "context" {
				t.Errorf("an ignoring handler ended by %q, want context (cancelled after the grace)", why)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a handler that ignored Stopping was never released")
		}
	}
}

func TestAnOrdinaryRequestIsNotCancelledByAGracefulStop(t *testing.T) {
	inFlight, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	s := serveHandler(t, muxOf(map[string]http.HandlerFunc{"/slow": func(w http.ResponseWriter, r *http.Request) {
		close(inFlight)
		<-release
		result <- r.Context().Err() // the request's own work would use this context
		_, _ = w.Write([]byte("done"))
	}}), 5*time.Second)
	go func() { _, _ = http.Get("http://" + s.addr + "/slow") }()
	<-inFlight
	s.cancel()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-s.done:
		t.Fatal("Serve returned with a request in flight")
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Errorf("r.Context() was cancelled by a graceful stop: %v", err)
	}
	if _, err := waitServe(t, s, 3*time.Second); err != nil {
		t.Errorf("Serve = %v, want nil", err)
	}
}

func TestStoppingReachesAHandlerThroughTheHostCheckAndGuards(t *testing.T) {
	app, stop := serveApp(t, `{"auth":{"mode":"off"}}`)
	defer stop()
	ended := make(chan string, 1)
	api := http.NewServeMux()
	api.HandleFunc("/api/stream", sse(true, ended))
	h, err := httphost.Handler(app, httphost.Options{Routes: api})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bound := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- httphost.Serve(ctx, app, h, httphost.ServeOptions{Addr: "127.0.0.1:0", Ready: func(a net.Addr) { bound <- a }})
	}()
	addr := (<-bound).String()
	resp, err := http.Get("http://" + addr + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if line, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || !strings.HasPrefix(line, "data:") {
		t.Fatalf("stream: %q %v", line, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return")
	}
	if why := <-ended; why != "stopping" {
		t.Errorf("handler ended by %q", why)
	}
}

func TestStoppingOutsideServeNeverCloses(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	if httphost.Stopping(r) != nil {
		t.Fatal("outside Serve, Stopping should be a nil channel")
	}
}
