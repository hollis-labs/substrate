package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// stdioAppMain is a scratch app with identity, cli, database, auth (enforce) and
// mcp. `stdio` serves MCP on stdin and stdout exactly as an adopter's agent-facing
// command does: the token file comes from STDIO_TOKEN_FILE and local trust is the
// app's own Go-level choice, switched here by STDIO_LOCAL for the test only.
const stdioAppMain = `package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/mcp"
)

func place(_ context.Context, app *module.App) error {
	data := os.Getenv("BOOTAPP_DATA")
	if data == "" {
		return fmt.Errorf("BOOTAPP_DATA is not set")
	}
	sub := func(m map[string]any, k string) map[string]any {
		v, _ := m[k].(map[string]any)
		if v == nil {
			v = map[string]any{}
			m[k] = v
		}
		return v
	}
	sub(sub(app.Config, "database"), "sqlite")["path"] = filepath.Join(data, "app.db")
	sub(app.Config, "identity")["key_file"] = filepath.Join(data, "signing.key")
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, boot.Options{AfterConfig: place})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = run.Stop(context.Background()) }()

	auth.ScopeTable(run.App).Tool("secret", "bootapp.admin")
	srv := mcp.Server(run.App)
	srv.RegisterChecked(gmcp.Tool{
		Name: "ping", Description: "returns pong", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(context.Context, map[string]any) (any, error) { return map[string]any{"msg": "pong"}, nil },
	}, gmcp.Reads("constant"))
	srv.RegisterChecked(gmcp.Tool{
		Name: "whoami", Description: "the caller's principal id", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			p, _ := libauth.FromContext(ctx)
			return map[string]any{"id": p.ID, "kind": string(p.Kind)}, nil
		},
	}, gmcp.Reads("caller"))
	srv.RegisterChecked(gmcp.Tool{
		Name: "secret", Description: "needs bootapp.admin", InputSchema: gmcp.EmptyObjectSchema(),
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			if err := mcp.Authorize(ctx, run.App, "secret"); err != nil {
				return nil, err
			}
			return map[string]any{"msg": "classified"}, nil
		},
	}, gmcp.Reads("gated"))

	root := cli.Root(run.App)
	root.AddCommand(&cobra.Command{Use: "stdio", Short: "Serve MCP on stdio", RunE: func(c *cobra.Command, _ []string) error {
		return mcp.ServeStdio(ctx, run.App, mcp.StdioOptions{
			TokenFile:  os.Getenv("STDIO_TOKEN_FILE"),
			LocalTrust: os.Getenv("STDIO_LOCAL") == "1",
		})
	}})
	if err := root.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
`

const stdioAppManifest = `app: bootapp
variant: app-host
profiles: [minimal]
modules:
  add: [cli, database, auth, httphost, identity, mcp]
config:
  logging: { level: info, format: text }
  auth: { mode: enforce }
  identity: { bcrypt_cost: 4 }
`

// syncBuffer is a stderr sink the child's copier and the test can both touch.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// stdioCmd builds the child's environment.
func (a *bootApp) stdioCmd(env ...string) *exec.Cmd {
	c := exec.Command(a.bin, "stdio")
	c.Env = append(os.Environ(), "BOOTAPP_DATA="+a.data)
	c.Env = append(c.Env, env...)
	return c
}

// stdioClient connects an MCP client to a real `bootapp stdio` process.
func (a *bootApp) stdioClient(t *testing.T, env ...string) (*mcpsdk.ClientSession, *syncBuffer) {
	t.Helper()
	c := a.stdioCmd(env...)
	stderr := &syncBuffer{}
	c.Stderr = stderr
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e", Version: "1"}, nil).
		Connect(ctx, &mcpsdk.CommandTransport{Command: c, TerminateDuration: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("connect: %v\nstderr: %s", err, stderr.String())
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, stderr
}

func toolText(t *testing.T, cs *mcpsdk.ClientSession, name string) (string, bool) {
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

// mintKey runs the offline command and returns the key file's path.
func (a *bootApp) mintKey(t *testing.T, name string, scopes ...string) string {
	t.Helper()
	path := filepath.Join(a.data, name+".key")
	args := []string{"identity", "key", "create", "--name", name, "--out", path}
	for _, s := range scopes {
		args = append(args, "--scope", s)
	}
	if o := a.cmd("", "", args...); o.err != nil {
		t.Fatalf("key create %s: %v %s", name, o.err, o.stderr)
	}
	return path
}

// TestStdioWithAnOfflineKeyAndWithLocalTrust runs real binaries end to end: a key
// made by `identity key create --out` with no server, used as the token file of a
// real `mcp.ServeStdio` process; and the same process under LocalTrust with no token.
func TestStdioWithAnOfflineKeyAndWithLocalTrust(t *testing.T) {
	a := buildApp(t, stdioAppMain, stdioAppManifest)
	if o := a.cmd(e2ePassword+"\n", "", "identity", "setup", "--username", "owner"); o.err != nil {
		t.Fatalf("setup: %v %s", o.err, o.stderr)
	}
	reader := a.mintKey(t, "reader", "bootapp.mcp")
	admin := a.mintKey(t, "admin", "bootapp.mcp", "bootapp.admin")
	noMCP := a.mintKey(t, "nomcp", "bootapp.admin")

	t.Run("a key without the admin scope: tools work, the gated tool is refused", func(t *testing.T) {
		cs, _ := a.stdioClient(t, "STDIO_TOKEN_FILE="+reader)
		if out, isErr := toolText(t, cs, "ping"); isErr || !strings.Contains(out, "pong") {
			t.Fatalf("ping: %q err=%v", out, isErr)
		}
		if out, isErr := toolText(t, cs, "whoami"); isErr || !strings.Contains(out, "key:") || strings.Contains(out, "local:operator") {
			t.Fatalf("whoami: %q err=%v", out, isErr)
		}
		if out, isErr := toolText(t, cs, "secret"); !isErr || strings.Contains(out, "classified") {
			t.Fatalf("a key without bootapp.admin reached the gated tool: %q", out)
		}
	})

	t.Run("a key with the admin scope reaches the gated tool", func(t *testing.T) {
		cs, _ := a.stdioClient(t, "STDIO_TOKEN_FILE="+admin)
		if out, isErr := toolText(t, cs, "secret"); isErr || !strings.Contains(out, "classified") {
			t.Fatalf("secret: %q err=%v", out, isErr)
		}
	})

	t.Run("LocalTrust with no token runs as local:operator", func(t *testing.T) {
		cs, stderr := a.stdioClient(t, "STDIO_LOCAL=1")
		if out, isErr := toolText(t, cs, "whoami"); isErr || !strings.Contains(out, "local:operator") {
			t.Fatalf("whoami: %q err=%v", out, isErr)
		}
		if out, isErr := toolText(t, cs, "secret"); isErr || !strings.Contains(out, "classified") {
			t.Fatalf("secret as the local operator: %q err=%v", out, isErr)
		}
		if !strings.Contains(stderr.String(), "local filesystem trust") {
			t.Fatalf("no local-trust notice on stderr: %q", stderr.String())
		}
	})

	t.Run("a token that is given is verified even under LocalTrust", func(t *testing.T) {
		cs, _ := a.stdioClient(t, "STDIO_LOCAL=1", "STDIO_TOKEN_FILE="+reader)
		if out, isErr := toolText(t, cs, "whoami"); isErr || strings.Contains(out, "local:operator") {
			t.Fatalf("a given token fell back to local trust: %q", out)
		}
		if out, isErr := toolText(t, cs, "secret"); !isErr {
			t.Fatalf("the reader key gained the gated tool under LocalTrust: %q", out)
		}
	})

	bad := writeToken(t, "key_not-a-real-key")
	refused := map[string]struct {
		env  []string
		said string // what the process must say on stderr
	}{
		"enforce, no token, no local trust": {nil, "needs a token"},
		"a key lacking the mcp scope":       {[]string{"STDIO_TOKEN_FILE=" + noMCP}, `lacks the scope "bootapp.mcp"`},
		"an unknown token":                  {[]string{"STDIO_TOKEN_FILE=" + bad}, "was not accepted"},
		"a bad token under LocalTrust":      {[]string{"STDIO_LOCAL=1", "STDIO_TOKEN_FILE=" + bad}, "was not accepted"},
	}
	for name, tc := range refused {
		t.Run("refused: "+name, func(t *testing.T) {
			c := a.stdioCmd(tc.env...)
			var stderr bytes.Buffer
			c.Stderr = &stderr
			c.Stdin = strings.NewReader("")
			done := make(chan error, 1)
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			go func() { done <- c.Wait() }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("the process served: %s", stderr.String())
				}
				if !strings.Contains(stderr.String(), tc.said) {
					t.Fatalf("stderr %q does not say %q", stderr.String(), tc.said)
				}
			case <-time.After(30 * time.Second):
				_ = c.Process.Kill()
				t.Fatal("the process neither served nor refused")
			}
		})
	}
}

func writeToken(t *testing.T, tok string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
