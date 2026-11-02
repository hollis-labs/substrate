// Package httphost wraps libs app-host: SPA assets, /api/ routes, /plugins/, and
// the health registry served on /healthz. The app owns listeners and policy.
package httphost

import (
	"context"
	"io/fs"
	"net/http"

	apphost "github.com/hollis-labs/libs/app/app-host/host"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/health"
)

func init() { module.Register(mod{}) }

type mod struct{}

func (mod) Name() string { return "httphost" }

func (mod) Start(_ context.Context, app *module.App) error {
	if health.From(app) == nil {
		return http.ErrNotSupported // unreachable via builder: health is required
	}
	return nil
}

func (mod) Stop(context.Context) error { return nil }

// Options are the app-owned inputs to the host.
type Options struct {
	Assets fs.FS          // embedded SPA, may be nil
	Routes *http.ServeMux // app routes mounted under /api/, may be nil
}

// Handler builds the app handler after all modules have started, so modules
// that published handlers (health, capabilities) are mounted.
func Handler(app *module.App, o Options) (http.Handler, error) {
	api := http.NewServeMux()
	if o.Routes != nil {
		api.Handle("/api/", o.Routes)
	}
	// The pluginhost module publishes the plugin forwarding routes; read by
	// key so httphost does not import it.
	if v, ok := app.Value("pluginhost.routes"); ok {
		if h, ok := v.(http.Handler); ok {
			api.Handle("/api/plugins/", h)
		}
	}
	if v, ok := app.Value("capabilities.handler"); ok {
		api.Handle("GET /api/capabilities", v.(http.Handler))
	}
	// When the auth module is started, its guard wraps /api/ (never /healthz or
	// the SPA). Read by key so httphost does not import auth.
	var apiHandler http.Handler = api
	if v, ok := app.Value("auth.guard"); ok {
		if g, ok := v.(interface {
			Middleware(http.Handler) http.Handler
		}); ok {
			apiHandler = g.Middleware(api)
		}
	}
	h, err := apphost.New(apphost.Config{Name: app.Name, Assets: o.Assets, Routes: apiHandler})
	if err != nil {
		return nil, err
	}
	outer := http.NewServeMux()
	// The mcp module publishes a mount; read by key so httphost does not import it.
	if v, ok := app.Value("mcp.mount"); ok {
		if m, ok := v.(interface{ MCPHandler() http.Handler }); ok {
			outer.Handle("/mcp", m.MCPHandler())
		}
	}
	outer.Handle("GET /healthz", health.From(app).Handler())
	outer.Handle("/", h.Handler())
	return outer, nil
}
