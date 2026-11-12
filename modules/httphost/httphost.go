// Package httphost wraps libs app-host: SPA assets, /api/ routes, declared module routes, app mounts, and
// the health registry served on /healthz. The app owns listeners and policy.
package httphost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

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
	// Mounts are app handlers served outside /api/, for paths a protocol fixes
	// (an A2A endpoint, /.well-known/...). Everything else belongs under /api/.
	Mounts []Mount
}

// Mount is an app handler at a net/http mux pattern ("/a2a/" or "GET /.well-known/agent.json").
// It is guarded by the auth module like /api/ and gets the flat error body, so
// map its scopes on auth.ScopeTable; set Open only for a path that must be
// reachable without credentials (public discovery). Patterns that would shadow
// /api/, /healthz, /mcp or the declared routes are refused.
type Mount struct {
	Pattern string
	Handler http.Handler
	Open    bool
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
	for _, m := range o.Mounts {
		if err := checkMount(m); err != nil {
			return nil, err
		}
		hh := apierr.Normalize(m.Handler)
		if !m.Open {
			hh = apierr.Normalize(guard(m.Handler))
		}
		if err := handle(outer, m.Pattern, hh); err != nil {
			return nil, err
		}
	}
	outer.Handle("/", h.Handler())
	return outer, nil
}

// checkMount refuses a mount that is malformed or aims at a path the host owns.
func checkMount(m Mount) error {
	if m.Handler == nil || m.Pattern == "" {
		return fmt.Errorf("httphost: a mount needs a pattern and a handler")
	}
	path := m.Pattern
	if i := strings.IndexByte(path, ' '); i >= 0 {
		path = strings.TrimSpace(path[i+1:])
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("httphost: mount %q: the path must start with /", m.Pattern)
	}
	for _, reserved := range []string{"/api", "/healthz", "/mcp"} {
		if path == reserved || strings.HasPrefix(path, reserved+"/") {
			return fmt.Errorf("httphost: mount %q is under %s, which the host owns", m.Pattern, reserved)
		}
	}
	if path == "/" {
		return fmt.Errorf("httphost: mount %q would shadow the SPA", m.Pattern)
	}
	return nil
}

// handle registers on mux and reports a conflicting pattern as an error rather than a panic.
func handle(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("httphost: mount %q: %v", pattern, r)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// ServeOptions configure Serve.
type ServeOptions struct {
	// Addr overrides config httphost.addr (default "127.0.0.1:8080").
	Addr string
	// Ready, if set, is called with the bound address once the listener is up
	// (useful with port 0).
	Ready func(net.Addr)
	// GracePeriod bounds graceful shutdown (default 10s); in-flight requests get
	// that long to finish.
	GracePeriod time.Duration
}

// Serve listens, serves h and shuts down gracefully when ctx ends, so every app
// serves the same way. The address comes from config httphost.addr; when the auth
// module is started the bind is validated against its mode: off and permissive
// accept loopback, a tailnet address (the Tailscale ranges or *.ts.net)
// or a unix socket and refuse a wildcard or any other address, so "off refuses a
// public address" does not mean loopback only (docs/identity.md). There is no write timeout, so streams
// (SSE) are not cut off; header and idle timeouts are set. Serve returns nil
// after a clean shutdown.
func Serve(ctx context.Context, app *module.App, h http.Handler, o ServeOptions) error {
	addr := o.Addr
	if addr == "" {
		cfg, _ := app.Config["httphost"].(map[string]any)
		addr, _ = cfg["addr"].(string)
	}
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if v, ok := app.Value("auth.guard"); ok {
		if g, ok := v.(interface{ Mode() libauth.Mode }); ok {
			if err := libauth.ValidateBind(addr, g.Mode()); err != nil {
				return fmt.Errorf("httphost: %w", err)
			}
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("httphost: %w", err)
	}
	if o.Ready != nil {
		o.Ready(ln.Addr())
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	grace := o.GracePeriod
	if grace <= 0 {
		grace = 10 * time.Second
	}
	sctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("httphost: shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
