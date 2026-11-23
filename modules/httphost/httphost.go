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
// real work is allowed to finish. An app's own background goroutines started from a handler
// should watch the app context, not Stopping(r): Stopping is for the request's own connection.
//
// # Error bodies
//
// Every route and mount is wrapped in apierr.Normalize, so a 4xx or 5xx JSON body of the form
// {"error":{"code","message"}} reaches the client as the flat {code, message}. That is right
// for an app's own API and wrong for a handler that relays a third-party protocol whose error
// shape clients depend on (an OpenAI-dialect gateway: {"error":{"message","type","param","code"}}).
// For those, declare the path once: Options.RawPrefixes for routes under /api/, Mount.Raw for
// a mount. The handler's response (status, headers, body, stream) is then served untouched.
// The guard, the scope table and every other refusal by the framework for that path still
// answer in the flat shape, so a client sees the upstream dialect for upstream errors and the
// framework shape for framework refusals. Do not use it for an app's own API.
//
// # Hardening
//
// Harden adds a Host check, an Origin check on writes, a body limit, panic recovery and a
// credential-free access log, to Handler's result or to an app's own handler. It is opt-in:
// Handler applies it when Options.Harden is set or a hardening key is configured under httphost:.
// # Unknown paths and wrong methods
//
// A request that no route or mount matches falls to the SPA at "/" (the placeholder page when
// Assets is nil or not built), so a client-side route works on reload. A request whose path a
// method-specific mount, module route or /healthz serves but whose method none of them accepts
// does not fall to the SPA: it is answered 405 method_not_allowed in the flat body with an Allow
// header. GET and HEAD to such a path still reach the SPA, so a browser can load a client route
// that shares its path with, say, a POST endpoint.
//
// A service (variant: service; the builder writes config httphost.fallback: not_found for it)
// has no SPA: with Assets nil, an unknown path answers 404 not_found and a wrong method 405
// method_not_allowed, flat (NotFoundFlat). fallback: spa keeps the placeholder; an app-host
// defaults to spa. An app with no UI whose clients expect its own error envelope sets
// Options.NotFound, which wins over the config. The SPA is then not installed: every request outside /api/ that no
// route or mount matches reaches NotFound, raw, and so does a wrong method on a method-specific
// path, GET included, with the Allow header already set so NotFound can answer 405 in its own
// shape. Unknown paths under /api/ stay with Options.Routes.
package httphost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	apphost "github.com/hollis-labs/libs/app/app-host/host"
	"github.com/hollis-labs/libs/core/panics"
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
	// RawPrefixes are path prefixes under /api/ whose handlers' responses skip
	// apierr.Normalize, for relaying a third-party protocol whose error shape clients
	// depend on (see the package comment). Each must start with /api/, end with "/", be more
	// than "/api/" itself, appear once, and not overlap a path a module declared; a prefix
	// equal to a mount's path makes that mount raw. Refused otherwise, naming the prefix.
	// The guard and the framework's own refusals stay flat.
	RawPrefixes []string
	// Harden, when set, wraps the handler in Harden with these options. Handler also applies
	// Harden (with zero options) when a hardening key is configured under httphost:. Nil and no
	// key (the default) leaves the handler as before.
	Harden *HardenOptions
	// NotFound, when set, replaces the SPA (and config httphost.fallback): it answers, raw and unguarded, every request outside
	// /api/ that no route or mount matches, and a wrong method on a method-specific path (with
	// the Allow header already set; answer 405 when it is present). For an app with no UI whose
	// clients expect its own error envelope; NotFoundFlat is the flat 404. Refused with Assets.
	// It runs unguarded, so it must answer refusals only and never serve data. Nil follows
	// config httphost.fallback (see the package comment).
	NotFound http.Handler
}

// NotFoundFlat answers 404 not_found, or 405 method_not_allowed when the Allow header is set,
// in the flat error body. Use it as Options.NotFound for an app with no UI.
var NotFoundFlat http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if w.Header().Get("Allow") != "" {
		apierr.Write(w, http.StatusMethodNotAllowed, "method_not_allowed", fmt.Sprintf("method %s is not allowed on %s", r.Method, r.URL.Path), nil)
		return
	}
	apierr.Write(w, http.StatusNotFound, "not_found", fmt.Sprintf("no route for %s", r.URL.Path), nil)
})

// Mount is an app handler at a net/http mux pattern ("/a2a/" or "GET /.well-known/agent.json").
// It is guarded by the auth module like /api/ and gets the flat error body, so
// map its scopes on auth.ScopeTable; set Open only for a path that must be
// reachable without credentials (public discovery). Patterns that would shadow
// /api/, /healthz, /mcp or the declared routes are refused.
type Mount struct {
	Pattern string
	Handler http.Handler
	Open    bool
	// Raw serves the handler's responses untouched (no apierr.Normalize), for a protocol whose
	// error shape clients depend on. Under the guard (not Open) the guard's own refusals stay flat.
	Raw bool
}

// Handler builds the app handler after all modules have started, so modules
// that published handlers (health, capabilities) are mounted.
func Handler(app *module.App, o Options) (http.Handler, error) {
	if err := checkRawPrefixes(app, o); err != nil {
		return nil, err
	}
	if o.NotFound != nil && o.Assets != nil {
		return nil, fmt.Errorf("httphost: NotFound replaces the SPA; set it or Assets, not both")
	}
	if o.NotFound == nil && o.Assets == nil {
		cfg, _ := app.Config["httphost"].(map[string]any)
		switch v, _ := cfg["fallback"].(string); v {
		case "", "spa":
		case "not_found":
			o.NotFound = NotFoundFlat
		default:
			return nil, fmt.Errorf("httphost: fallback %q is not spa or not_found", v)
		}
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
	if len(o.RawPrefixes) > 0 {
		apiHandler = rawSplit(apiHandler, guard, api, func(path string) bool {
			for _, p := range o.RawPrefixes {
				if strings.HasPrefix(path, p) {
					return true
				}
			}
			return false
		})
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
	// rest is "/": only a request no other pattern on outer matched reaches it. probe holds the
	// method-specific patterns, to tell a wrong method from an unknown path there.
	rest := &fallback{probe: http.NewServeMux(), notFound: o.NotFound}
	rest.add("GET /healthz")
	if len(module.Routes(app)) > 0 {
		for _, r := range module.Routes(app) {
			if !strings.HasPrefix(r.Path, "/api/") {
				outer.Handle(r.Pattern(), outerRoutes)
				rest.add(r.Pattern())
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
		if m.Raw || rawMount(o.RawPrefixes, m.Pattern) {
			hh = m.Handler
			if !m.Open {
				hh = rawSplit(nil, guard, m.Handler, func(string) bool { return true })
			}
		}
		if err := handle(outer, m.Pattern, hh); err != nil {
			return nil, err
		}
		rest.add(m.Pattern)
	}
	if o.NotFound == nil {
		rest.spa = h.Handler()
	} else {
		outer.Handle("/api/", h.Handler())
	}
	outer.Handle("/", rest)
	if o.Harden != nil {
		return Harden(app, outer, *o.Harden)
	}
	if hardenConfigured(app) {
		// Turned on by config alone: only what is configured runs (no panic guard, no default
		// body limit), so turning on one key (the access log) changes no other request.
		return harden(app, outer, HardenOptions{}, false)
	}
	return outer, nil
}

// fallback serves "/" on the outer mux. A wrong method on a path a method-specific pattern
// serves is answered 405 (or by notFound), never by the SPA; GET and HEAD still reach the SPA.
type fallback struct {
	probe    *http.ServeMux
	methods  []string // the methods the probe's patterns use
	spa      http.Handler
	notFound http.Handler
}

// add records a method-specific pattern already registered on outer (so it cannot conflict here).
func (f *fallback) add(pattern string) {
	i := strings.IndexByte(pattern, ' ')
	if i < 0 {
		return
	}
	f.probe.Handle(pattern, http.NotFoundHandler())
	if m := pattern[:i]; !slices.Contains(f.methods, m) {
		f.methods = append(f.methods, m)
	}
}

// allowed lists the methods the probe accepts for r's path, with HEAD where GET is.
func (f *fallback) allowed(r *http.Request) []string {
	var out []string
	for _, m := range f.methods {
		r2 := *r
		r2.Method = m
		if _, p := f.probe.Handler(&r2); p != "" {
			out = append(out, m)
			if m == http.MethodGet && !slices.Contains(f.methods, http.MethodHead) {
				out = append(out, http.MethodHead)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (f *fallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	browse := r.Method == http.MethodGet || r.Method == http.MethodHead
	if f.spa == nil || !browse {
		if allow := f.allowed(r); len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			if f.notFound != nil {
				f.notFound.ServeHTTP(w, r)
				return
			}
			apierr.Write(w, http.StatusMethodNotAllowed, "method_not_allowed", fmt.Sprintf("method %s is not allowed on %s", r.Method, r.URL.Path), nil)
			return
		}
	}
	if f.spa != nil {
		f.spa.ServeHTTP(w, r)
		return
	}
	f.notFound.ServeHTTP(w, r)
}

// rawSplit serves a request whose path is raw through the guard with the guard's own refusals
// normalised and the handler behind it writing to the real ResponseWriter, so its response is
// untouched. A request that is not raw goes to normal.
//
// The guard runs under apierr.Normalize (its 401, 403 route_not_mapped and the like are flat);
// the handler it admits ignores the writer the guard handed it and writes to the original one,
// which Normalize never sees. The request (and its context: principal, Stopping) is the guard's.
func rawSplit(normal http.Handler, guard func(http.Handler) http.Handler, inner http.Handler, isRaw func(string) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRaw(r.URL.Path) {
			normal.ServeHTTP(w, r)
			return
		}
		admitted := http.HandlerFunc(func(_ http.ResponseWriter, r2 *http.Request) { inner.ServeHTTP(w, r2) })
		apierr.Normalize(guard(admitted)).ServeHTTP(w, r)
	})
}

func mountPath(pattern string) string {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		return strings.TrimSpace(pattern[i+1:])
	}
	return pattern
}

// rawMount reports whether a declared raw prefix names this mount's path.
func rawMount(prefixes []string, pattern string) bool {
	mp := mountPath(pattern)
	for _, p := range prefixes {
		if !strings.HasPrefix(p, "/api/") && strings.HasPrefix(mp, p) {
			return true
		}
	}
	return false
}

// checkRawPrefixes validates Options.RawPrefixes before anything is built.
func checkRawPrefixes(app *module.App, o Options) error {
	seen := map[string]bool{}
	for _, p := range o.RawPrefixes {
		switch {
		case p == "":
			return fmt.Errorf("httphost: raw prefix is empty")
		case seen[p]:
			return fmt.Errorf("httphost: raw prefix %q is listed twice", p)
		case !strings.HasPrefix(p, "/"):
			return fmt.Errorf("httphost: raw prefix %q must start with /api/", p)
		case p == "/api/":
			return fmt.Errorf("httphost: raw prefix %q is all of /api/; name the relay's own subtree", p)
		}
		seen[p] = true
		if !strings.HasSuffix(p, "/") {
			return fmt.Errorf("httphost: raw prefix %q must end with / (it is a path prefix, so /api/x would also match /api/xy)", p)
		}
		if strings.Contains(p, "..") || strings.Contains(p, "//") || strings.ContainsAny(p, " {}") {
			return fmt.Errorf("httphost: raw prefix %q is not a plain path prefix", p)
		}
		if !strings.HasPrefix(p, "/api/") {
			ok := false
			for _, m := range o.Mounts {
				if strings.HasPrefix(mountPath(m.Pattern), p) {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("httphost: raw prefix %q is outside /api/ and names no mount; set Mount.Raw on a mount instead", p)
			}
			continue
		}
		for _, r := range module.Routes(app) {
			rp := r.Path
			if i := strings.IndexByte(rp, '{'); i >= 0 {
				rp = rp[:i]
			}
			if strings.HasPrefix(rp, p) || (strings.HasSuffix(rp, "/") && strings.HasPrefix(p, rp)) {
				return fmt.Errorf("httphost: raw prefix %q overlaps the route %s declared by a module", p, r.Path)
			}
		}
	}
	return nil
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
	defer panics.Recover("httphost mount", &err)
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
	// Listener, if set, is served instead of listening on Addr (a unix socket from ListenUnix,
	// a socket-activated descriptor). Serve keeps its Stopping, handler tracking and drain over
	// it and closes it on stop. The auth bind check runs on Listener.Addr(); a unix address is
	// checked as "unix:<path>", and in off and permissive mode the socket must be a file that
	// other users cannot write to: file permissions are what protect it, so an abstract socket
	// (@name), which has none, is refused. Create it with ListenUnix rather than net.Listen
	// and a chmod, which leaves a window with the umask's permissions. Addr and config
	// httphost.addr are ignored. Serve takes ownership: it closes the listener on every
	// return, a refused bind included.
	Listener net.Listener
	// ConnContext, if set, is called for each new connection after Serve's own and may return
	// a derived context (http.Server.ConnContext). Derive from the ctx it is given: that one
	// carries Stopping and PeerCredentials. c is the connection exactly as the listener
	// accepted it, never wrapped, so a type assertion (*net.UnixConn, *net.TCPConn) holds;
	// apps rely on that to tell a local socket caller from a remote one.
	ConnContext func(ctx context.Context, c net.Conn) context.Context
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
	if o.Listener != nil {
		addr = o.Listener.Addr().String()
		if ua, ok := listenerUnixAddr(o.Listener); ok {
			addr = "unix:" + ua.Name
		}
	} else if addr == "" {
		cfg, _ := app.Config["httphost"].(map[string]any)
		addr, _ = cfg["addr"].(string)
	}
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if o.Listener == nil && strings.HasPrefix(addr, "unix:") {
		return fmt.Errorf("httphost: addr %q: Serve listens on TCP only; create the unix socket and pass it as ServeOptions.Listener", addr)
	}
	if v, ok := app.Value("auth.guard"); ok {
		if g, ok := v.(interface{ Mode() libauth.Mode }); ok {
			err := libauth.ValidateBind(addr, g.Mode())
			if ua, ok := listenerUnixAddr(o.Listener); ok && err == nil {
				err = checkUnixListener(ua, g.Mode())
			}
			if err != nil {
				if o.Listener != nil {
					_ = o.Listener.Close()
				}
				return fmt.Errorf("httphost: %w", err)
			}
			// The auth module stays quiet at Start so a short-lived command
			// prints nothing; a listener is where the warning belongs.
			if m := g.Mode(); m != libauth.ModeEnforce {
				slog.Warn("auth is not enforcing", "auth", string(m), "addr", addr)
			}
		}
	}
	ln := o.Listener
	if ln == nil {
		var err error
		if ln, err = net.Listen("tcp", addr); err != nil {
			return fmt.Errorf("httphost: %w", err)
		}
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
		Handler:     tracked,
		BaseContext: func(net.Listener) context.Context { return base },
		// c is what ln.Accept returned, unwrapped: readPeerCreds and the app's ConnContext
		// type-assert it (*net.UnixConn). Do not wrap the listener or the conn here.
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if pc, ok := readPeerCreds(c); ok {
				ctx = context.WithValue(ctx, peerKey{}, pc)
			}
			if o.ConnContext != nil {
				ctx = o.ConnContext(ctx, c)
			}
			return ctx
		},
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
	err := srv.Shutdown(sctx)
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

func listenerUnixAddr(ln net.Listener) (*net.UnixAddr, bool) {
	if ln == nil {
		return nil, false
	}
	ua, ok := ln.Addr().(*net.UnixAddr)
	return ua, ok
}
