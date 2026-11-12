// Package mcp is the MCP server plumbing module: libs go-mcp's server and
// Streamable HTTP transport, created from config and mounted by httphost at
// /mcp. Apps only define tools; Substrate does not own what the tools do.
//
// After boot.Start the app registers tools on Server(app):
//
//	mcp.Server(app).RegisterChecked(server.Tool{...}, server.Reads("why"))
//
// Config under "mcp":
//
//	version:         server version string (default "0.0.0-dev").
//	scope:           scope required on /mcp when the auth module is started
//	                 (default "<app>.mcp").
//	allowed_origins: Origin allowlist for browser clients (default: all).
//	stdio_token_file: file holding the token ServeStdio presents (default none).
//
// Over stdio (an agent launching the app as a subprocess) call
// ServeStdio from the app's own command. The caller is identified once at start
// from a token file (config stdio_token_file or StdioOptions.TokenFile) and
// verified by the auth module, then stamped on every tool call, so Authorize
// and per-tool scopes behave exactly as over HTTP.
//
// With auth started, /mcp is mapped to the configured scope in enforce mode and
// the guard's principal reaches tool handlers; call Authorize(ctx, app, tool)
// for per-tool scopes.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	mcphttp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/transport/http"
	libauth "github.com/hollis-labs/libs/security/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/substrate/module"
)

// Keys under which the server and mount are published. httphost reads MountKey
// by name, so it does not import this package.
const (
	ServerKey = "mcp.server"
	MountKey  = "mcp.mount"
)

var (
	filterMu   sync.Mutex
	toolFilter func(ctx context.Context, names []string) []string
)

// UseToolFilter registers a per-caller visibility filter for tools/list. f gets
// the names of every registered tool and returns the ones to list to this
// caller, in the order to list them; one call per request, so it can do a
// lookup per caller rather than per tool. ctx carries the caller's principal
// (HTTP and stdio alike). It is visibility, not authorization: a hidden tool can
// still be called by name, so the tool's handler must authorize (see
// Authorize). Call it before boot.Start; f may consult state that changes while
// serving.
func UseToolFilter(f func(ctx context.Context, names []string) []string) {
	filterMu.Lock()
	toolFilter = f
	filterMu.Unlock()
}

// filterTools is receiving middleware that trims the tools/list result.
func filterTools(f func(context.Context, []string) []string) mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return res, err
			}
			if lr, ok := res.(*mcpsdk.ListToolsResult); ok {
				names := make([]string, len(lr.Tools))
				byName := make(map[string]*mcpsdk.Tool, len(lr.Tools))
				for i, t := range lr.Tools {
					names[i], byName[t.Name] = t.Name, t
				}
				kept := make([]*mcpsdk.Tool, 0, len(lr.Tools))
				for _, n := range f(ctx, names) {
					if t, ok := byName[n]; ok {
						kept = append(kept, t)
					}
				}
				lr.Tools = kept
			}
			return res, nil
		}
	}
}

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "mcp" }

func (mod) Start(_ context.Context, app *module.App) error {
	cfg, _ := app.Config["mcp"].(map[string]any)
	str := func(k, def string) string {
		if s, ok := cfg[k].(string); ok && s != "" {
			return s
		}
		return def
	}
	var origins []string
	if l, ok := cfg["allowed_origins"].([]any); ok {
		for _, o := range l {
			if s, ok := o.(string); ok {
				origins = append(origins, s)
			}
		}
	}
	filterMu.Lock()
	f := toolFilter
	filterMu.Unlock()
	var opts []gmcp.Option
	if f != nil {
		opts = append(opts, gmcp.WithReceivingMiddleware(filterTools(f)))
	}
	srv := gmcp.NewServer(app.Name, str("version", "0.0.0-dev"), opts...)
	app.Set(ServerKey, srv)
	app.Set(MountKey, &mount{app: app, srv: srv, scope: str("scope", app.Name+".mcp"), origins: origins})
	return nil
}

func (mod) Stop(context.Context) error { return nil }

// mount builds the /mcp handler after every module has started, so auth (if
// selected) is wired regardless of module order.
type mount struct {
	app     *module.App
	srv     *gmcp.Server
	scope   string
	origins []string
}

// MCPHandler is read by httphost through an interface.
func (m *mount) MCPHandler() http.Handler {
	h := http.Handler(mcphttp.NewHandler(m.srv, mcphttp.HandlerOptions{AllowedOrigins: m.origins}))
	if g := guard(m.app); g != nil {
		if t := scopes(m.app); t != nil {
			for _, method := range []string{"GET", "POST", "DELETE"} {
				t.Route(method, "/mcp", m.scope)
			}
		}
		h = g.Middleware(h)
	}
	return h
}

// Server returns the MCP server published by the mcp module, or nil.
func Server(app *module.App) *gmcp.Server {
	v, _ := app.Value(ServerKey)
	s, _ := v.(*gmcp.Server)
	return s
}

// Authorize checks the caller's principal against tool's scope when the auth
// module is started (see libs auth.ScopeTable.Tool). Without auth it allows.
func Authorize(ctx context.Context, app *module.App, tool string) error {
	if g := guard(app); g != nil {
		return g.AuthorizeTool(ctx, tool)
	}
	return nil
}

func guard(app *module.App) *libauth.Guard {
	v, _ := app.Value("auth.guard")
	g, _ := v.(*libauth.Guard)
	return g
}

func scopes(app *module.App) *libauth.ScopeTable {
	v, _ := app.Value("auth.scopes")
	t, _ := v.(*libauth.ScopeTable)
	return t
}

// StdioOptions configure ServeStdio.
type StdioOptions struct {
	In        io.ReadCloser  // default os.Stdin
	Out       io.WriteCloser // default os.Stdout; nothing else may write to it
	TokenFile string         // the caller's token; default config mcp.stdio_token_file
}

// ServeStdio serves the app's MCP server over stdio until ctx ends or the
// client disconnects. With auth in off mode no token is needed. In permissive
// and enforce mode a token that is given must verify; in enforce mode one is
// required, and it must hold the same scope the /mcp route needs.
func ServeStdio(ctx context.Context, app *module.App, o StdioOptions) error {
	srv := Server(app)
	if srv == nil {
		return errors.New("mcp: the module is not started")
	}
	ctx, err := identify(ctx, app, o.TokenFile)
	if err != nil {
		return err
	}
	in, out := o.In, o.Out
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	return srv.SDKServer().Run(ctx, &mcpsdk.IOTransport{Reader: in, Writer: out})
}

// identify verifies the stdio caller's token and stamps the principal on ctx.
func identify(ctx context.Context, app *module.App, tokenFile string) (context.Context, error) {
	g := guard(app)
	if g == nil || g.Mode() == libauth.ModeOff {
		return ctx, nil
	}
	cfg, _ := app.Config["mcp"].(map[string]any)
	if tokenFile == "" {
		tokenFile, _ = cfg["stdio_token_file"].(string)
	}
	enforce := g.Mode() == libauth.ModeEnforce
	if tokenFile == "" {
		if enforce {
			return ctx, errors.New("mcp: stdio in enforce mode needs a token (mcp.stdio_token_file or StdioOptions.TokenFile)")
		}
		return ctx, nil
	}
	b, err := os.ReadFile(tokenFile)
	if err != nil {
		return ctx, errors.New("mcp: cannot read the stdio token file")
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return ctx, errors.New("mcp: the stdio token file is empty")
	}
	v, _ := app.Value("auth.verifier") // read by key: mcp does not import auth
	vf, ok := v.(libauth.Verifier)
	if !ok {
		return ctx, errors.New("mcp: stdio has a token but the auth module has no verifier configured")
	}
	p, err := vf.Verify(ctx, tok)
	if err != nil {
		return ctx, errors.New("mcp: the stdio token was not accepted")
	}
	scope, _ := cfg["scope"].(string)
	if scope == "" {
		scope = app.Name + ".mcp"
	}
	if enforce && !p.HasScope(scope) {
		return ctx, fmt.Errorf("mcp: the stdio token lacks the scope %q", scope)
	}
	return libauth.WithPrincipal(ctx, p), nil
}
