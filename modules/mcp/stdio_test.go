package mcp_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	libauth "github.com/hollis-labs/libs/security/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/mcp"
)

const (
	readKey  = "stdio-read-key"
	adminKey = "stdio-admin-key"
)

func authCfg(mode string) string {
	return `{"auth":{"mode":"` + mode + `","api_keys":[` +
		`{"id":"svc/r","hash":"` + libauth.HashKey(readKey) + `","scopes":["t.mcp"]},` +
		`{"id":"svc/a","hash":"` + libauth.HashKey(adminKey) + `","scopes":["t.mcp","t.admin"]}]}}`
}

func stdioApp(t *testing.T, cfg string) *module.App {
	t.Helper()
	run, err := boot.Start(context.Background(), "t", []string{"health", "auth", "httphost", "mcp"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	auth.ScopeTable(run.App).Tool("secret", "t.admin")
	srv := mcp.Server(run.App)
	srv.RegisterChecked(gmcp.Tool{
		Name: "ping", Description: "returns pong", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(context.Context, map[string]any) (any, error) { return map[string]any{"msg": "pong"}, nil },
	}, gmcp.Reads("constant"))
	srv.RegisterChecked(gmcp.Tool{
		Name: "secret", Description: "needs t.admin", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			if err := mcp.Authorize(ctx, run.App, "secret"); err != nil {
				return nil, err
			}
			return map[string]any{"msg": "classified"}, nil
		},
	}, gmcp.Reads("gated"))
	return run.App
}

func tokenFile(t *testing.T, tok string) string {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// connect runs ServeStdio on one end of a pipe pair and an MCP client on the
// other, the way an agent launches the app as a subprocess.
func connect(t *testing.T, app *module.App, tokenPath string) (*mcpsdk.ClientSession, <-chan error) {
	t.Helper()
	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		err := mcp.ServeStdio(ctx, app, mcp.StdioOptions{In: srvIn, Out: srvOut, TokenFile: tokenPath})
		_ = srvOut.Close()
		done <- err
	}()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).
		Connect(ctx, &mcpsdk.IOTransport{Reader: cliIn, Writer: cliOut}, nil)
	if err != nil {
		select {
		case serr := <-done:
			t.Fatalf("connect: %v (server: %v)", err, serr)
		default:
			t.Fatalf("connect: %v", err)
		}
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, done
}

func callTool(t *testing.T, cs *mcpsdk.ClientSession, name string) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name})
	if err != nil {
		return err.Error(), true
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func TestStdioServesToolsInOffModeWithNoToken(t *testing.T) {
	t.Setenv("HOLLIS_AUTH", "")
	cs, _ := connect(t, stdioApp(t, `{}`), "")
	if out, isErr := callTool(t, cs, "ping"); isErr || !strings.Contains(out, "pong") {
		t.Fatalf("ping: %q err=%v", out, isErr)
	}
}

func TestStdioEnforceIdentifiesTheCallerAndScopesTools(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))

	cs, _ := connect(t, app, tokenFile(t, readKey))
	if out, isErr := callTool(t, cs, "ping"); isErr || !strings.Contains(out, "pong") {
		t.Fatalf("ping with the read key: %q err=%v", out, isErr)
	}
	// The per-tool scope is judged against the stdio caller exactly as over HTTP.
	if out, isErr := callTool(t, cs, "secret"); !isErr {
		t.Fatalf("a caller without t.admin reached the tool: %q", out)
	}

	cs2, _ := connect(t, app, tokenFile(t, adminKey))
	if out, isErr := callTool(t, cs2, "secret"); isErr || !strings.Contains(out, "classified") {
		t.Fatalf("secret with the admin key: %q err=%v", out, isErr)
	}
}

func TestStdioRefusesWithoutAnAcceptableToken(t *testing.T) {
	app := stdioApp(t, authCfg("enforce"))
	for name, path := range map[string]string{
		"no token":      "",
		"unknown token": tokenFile(t, "not-a-key"),
		"empty file":    tokenFile(t, ""),
		"missing file":  filepath.Join(t.TempDir(), "secret-dir", "absent"),
	} {
		err := mcp.ServeStdio(context.Background(), app, mcp.StdioOptions{In: io.NopCloser(strings.NewReader("")), Out: nopWriteCloser{io.Discard}, TokenFile: path})
		if err == nil {
			t.Errorf("%s: ServeStdio started", name)
			continue
		}
		if strings.Contains(err.Error(), "secret-dir") || strings.Contains(err.Error(), "not-a-key") {
			t.Errorf("%s: the error leaks: %v", name, err)
		}
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
