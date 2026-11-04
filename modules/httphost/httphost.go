// Package httphost wraps libs app-host: SPA assets, /api/ routes, /plugins/, and
// the health registry served on /healthz. The app owns listeners and policy.
package httphost

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	apphost "github.com/hollis-labs/libs/app/app-host/host"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/apierr"
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
	// When the auth module is started, its guard wraps /api/ (never /healthz or
	// the SPA). Read by key so httphost does not import auth.
	guard := func(h http.Handler) http.Handler { return h }
	if v, ok := app.Value("auth.guard"); ok {
		if g, ok := v.(interface {
			Middleware(http.Handler) http.Handler
		}); ok {
			guard = g.Middleware
		}
	}
	// Routes modules declared (module.AddRoutes) are mounted here, and mapped to
	// their scope when auth is started, so none can be left unmapped. /api/ routes
	// go under the /api/ guard; other declared paths (such as /admin/...) are
	// mounted exactly, guarded the same way, so the SPA's own routes still fall through.
	table, _ := app.Values["auth.scopes"].(*libauth.ScopeTable)
	outerRoutes := http.NewServeMux()
	for _, r := range module.Routes(app) {
		if table != nil {
			table.Route(r.Method, r.ScopePattern(), r.Scope)
		}
		if strings.HasPrefix(r.Path, "/api/") {
			api.Handle(r.Pattern(), r.Handler)
		} else {
			outerRoutes.Handle(r.Pattern(), apierr.Normalize(guard(r.Handler)))
		}
	}
	apiHandler := apierr.Normalize(guard(api))
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
	if len(module.Routes(app)) > 0 {
		for _, r := range module.Routes(app) {
			if !strings.HasPrefix(r.Path, "/api/") {
				outer.Handle(r.Pattern(), outerRoutes)
			}
		}
	}
	outer.Handle("/", h.Handler())
	return outer, nil
}
