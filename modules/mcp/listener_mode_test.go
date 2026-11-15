package mcp_test

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/modules/httphost"
)

func postTool(t *testing.T, c *http.Client, url, tool, bearer string) (int, string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
	r, _ := http.NewRequest("POST", url+"/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

// A permissive app serves one handler on a unix socket in its own mode and on TCP with
// AuthMode enforce. On TCP the /mcp guard and the tool's scope check (mcp.Authorize)
// both enforce; on the socket they only log.
func TestServeAuthModePerListenerReachesToolChecks(t *testing.T) {
	app := stdioApp(t, authCfg("permissive"))
	h, err := httphost.Handler(app, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan net.Addr, 2)
	done := make(chan error, 2)
	go func() {
		done <- httphost.Serve(ctx, app, h, httphost.ServeOptions{Listener: ln, Ready: func(a net.Addr) { ready <- a }, GracePeriod: time.Second})
	}()
	go func() {
		done <- httphost.Serve(ctx, app, h, httphost.ServeOptions{Addr: "127.0.0.1:0", AuthMode: libauth.ModeEnforce, Ready: func(a net.Addr) { ready <- a }, GracePeriod: time.Second})
	}()
	var tcp string
	for range 2 {
		if a := <-ready; a.Network() == "tcp" {
			tcp = "http://" + a.String()
		}
	}
	sc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}

	// Socket: permissive. No token, and a token without t.admin, both reach the tool.
	if code, out := postTool(t, sc, "http://u", "secret", ""); code != 200 || !strings.Contains(out, "classified") {
		t.Fatalf("socket, no token: %d %s", code, out)
	}
	// TCP: enforce.
	if code, _ := postTool(t, http.DefaultClient, tcp, "ping", ""); code != 401 {
		t.Fatalf("tcp, no token: %d, want 401", code)
	}
	if code, out := postTool(t, http.DefaultClient, tcp, "ping", readKey); code != 200 || !strings.Contains(out, "pong") {
		t.Fatalf("tcp, read key, ping: %d %s", code, out)
	}
	if code, out := postTool(t, http.DefaultClient, tcp, "secret", readKey); strings.Contains(out, "classified") {
		t.Fatalf("tcp, read key, secret: tool ran without t.admin (%d %s)", code, out)
	}
	if code, out := postTool(t, http.DefaultClient, tcp, "secret", adminKey); code != 200 || !strings.Contains(out, "classified") {
		t.Fatalf("tcp, admin key, secret: %d %s", code, out)
	}
	stop()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("Serve: %v", err)
		}
	}
}
