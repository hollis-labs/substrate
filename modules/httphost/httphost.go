// Package httphost wraps libs app-host: SPA assets, /api/ routes, declared module routes, app mounts, and
// the health registry served on /healthz. The app owns listeners and policy.
//
// # Shutdown and long-lived handlers
//
// Serve ends on its context. http.Server.Shutdown waits for ordinary requests but cannot end an
// SSE stream and does not track a hijacked connection (a WebSocket), so a handler that keeps a
// connection open must watch Stopping(r), which closes when shutdown begins. Serve waits for
// every handler to return and then returns nil; a handler that ignores Stopping holds Serve for
// GracePeriod (default 10s), after which r.Context() is cancelled, connections are closed and
// Serve returns an error. r.Context() is not cancelled by a graceful stop, so a request doing
// real work is allowed to finish.
//
// # Host check
//
// Handler wraps everything it serves (/api/, /mcp, /healthz, declared routes, mounts and
// the SPA) in a host check, on for every app with no off switch. It answers a request only
// when its Host is a loopback name (localhost, 127.0.0.0/8, ::1, any port) or a name listed in
// config httphost.allowed_hosts (exact, case-insensitive; a port only if the client sends one,
// so "app.example.test" does not admit "app.example.test:8443"). A listed name is served only
// when the peer is a loopback address and the request carries exactly one X-Forwarded-Proto:
// https, that is a local TLS proxy; otherwise 403 untrusted_proxy. Any other Host is
// 403 invalid_host. Bodies are the flat apierr shape and never echo the Host. Before routing
// the check removes Forwarded, X-Forwarded-Host, X-Forwarded-For and X-Real-IP, and removes
// X-Forwarded-Proto unless it was just validated (so identity's trusted_proxy can believe
// it); it sets X-Frame-Options: DENY and Referrer-Policy: no-referrer on every response.
// For a validated proxied request it records the client address (the last hop of
// X-Forwarded-For, when a bare IP) for clientaddr.From. Loopback is always served and
// cannot be removed. Entries that are empty, wildcards, or carry a scheme, path or user
// info fail Start. Serve warns when it binds a non-loopback address with an empty list,
// since every request then needs a loopback Host.
//
// A proxy on another host is not supported: the peer rule is loopback only.
package httphost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
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
	cfg, _ := app.Config["httphost"].(map[string]any)
	_, err := parseAllowedHosts(cfg)
	return err
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
// that published handlers (health, capabilities) are mounted. The returned handler
// carries the host check (package comment); an app does not add its own.
func Handler(app *module.App, o Options) (http.Handler, error) {
	hcfg, _ := app.Config["httphost"].(map[string]any)
	allowed, err := parseAllowedHosts(hcfg)
	if err != nil {
		return nil, err
	}
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
	return hostGuard(allowed, outer), nil
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
// (SSE) are not cut off; header and idle timeouts are set. In off or permissive
// mode Serve logs the "auth is not enforcing" warning (the auth module itself
// stays quiet at Start, so short-lived commands print nothing). Serve returns nil
// after a clean shutdown: the listener is closed, in-flight requests have finished and
// every handler, hijacked ones included, has returned within GracePeriod. Long-lived
// handlers learn of the stop through Stopping. Past the grace period Serve cancels the
// request contexts, closes the connections and returns an error naming how many handlers
// were still running.
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
			// The auth module stays quiet at Start so a short-lived command
			// prints nothing; a listener is where the warning belongs.
			if m := g.Mode(); m != libauth.ModeEnforce {
				slog.Warn("auth is not enforcing", "auth", string(m), "addr", addr)
			}
		}
	}
	if hcfg, _ := app.Config["httphost"].(map[string]any); !loopbackAddr(addr) {
		if allowed, _ := parseAllowedHosts(hcfg); len(allowed) == 0 {
			slog.Warn("httphost: serving a non-loopback address with no httphost.allowed_hosts; "+
				"every request needs a loopback Host and is refused otherwise", "addr", addr)
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("httphost: %w", err)
	}
	if o.Ready != nil {
		o.Ready(ln.Addr())
	}
	// stopping closes when shutdown begins; every request context carries it (Stopping).
	// base is the parent of every request context. It is cancelled only when the grace
	// period has run out, just before the server closes connections, so an ordinary
	// request that uses r.Context() for its own work is not aborted by a graceful stop.
	stopping := make(chan struct{})
	base, baseCancel := context.WithCancel(context.WithValue(context.WithoutCancel(ctx), stoppingKey{}, (<-chan struct{})(stopping)))
	defer baseCancel()
	// active counts handlers still running. http.Server.Shutdown does not wait for a
	// hijacked connection's handler (a WebSocket) and cannot end a stream; Serve waits
	// for the handlers themselves so a clean stop means they returned.
	var active atomic.Int64
	tracked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		h.ServeHTTP(w, r)
	})
	srv := &http.Server{
		Handler:           tracked,
		BaseContext:       func(net.Listener) context.Context { return base },
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	close(stopping)
	grace := o.GracePeriod
	if grace <= 0 {
		grace = 10 * time.Second
	}
	sctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	err = srv.Shutdown(sctx)
	for err == nil && active.Load() > 0 {
		select {
		case <-sctx.Done():
			err = sctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err != nil {
		n := active.Load()
		baseCancel() // tell the handlers that ignored Stopping, then cut the connections
		_ = srv.Close()
		return fmt.Errorf("httphost: shutdown: %w (%d handler(s) still running)", err, n)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type stoppingKey struct{}

// Stopping returns a channel that is closed when the server begins to shut down (when
// the context given to Serve ends), for a handler that outlives a normal request: an SSE
// stream, a WebSocket, a long poll. http.Server.Shutdown cannot end those, so a handler
// that does not watch this channel holds Serve until the grace period runs out, after
// which Serve cancels r.Context(), closes the connections and returns an error:
//
//	select {
//	case <-httphost.Stopping(r): // send a close frame, flush, return
//	case <-r.Context().Done():   // the client went away
//	case ev := <-events: ...
//	}
//
// It is read from the request context (Serve is the only thing that sets it), so it
// reaches a handler through every wrapper (host check, identity, guard, apierr) without a
// parameter. Outside Serve (a handler called from httptest) it returns nil, a channel that
// never closes, so the same select works in a test. Serve returns nil only once every
// handler has returned; a hijacked handler must return too (close its connection) because
// Serve counts it.
//
// r.Context() itself stays live through a graceful stop, so a request that is still
// working (a database call) finishes; it is cancelled only when the grace period ends.
func Stopping(r *http.Request) <-chan struct{} {
	c, _ := r.Context().Value(stoppingKey{}).(<-chan struct{})
	return c
}
