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
//
// With auth started, /mcp is mapped to the configured scope in enforce mode and
// the guard's principal reaches tool handlers; call Authorize(ctx, app, tool)
// for per-tool scopes.
package mcp

import (
	"context"
	"net/http"

	libauth "github.com/hollis-labs/libs/security/auth"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	mcphttp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/transport/http"

	"github.com/hollis-labs/substrate/module"
)

// Keys under which the server and mount are published. httphost reads MountKey
// by name, so it does not import this package.
const (
	ServerKey = "mcp.server"
	MountKey  = "mcp.mount"
)

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
	srv := gmcp.NewServer(app.Name, str("version", "0.0.0-dev"))
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
