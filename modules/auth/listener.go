package auth

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/module"
)

// ListenersKey holds the app's per-listener guards. httphost and mcp read it by name
// through an interface, so they do not import this package.
const ListenersKey = "auth.listeners"

// listeners holds, for one app, the guard of every mode a listener asked for. Each
// shares the app guard's verifiers, scope table and open paths and differs only in
// mode, which is fixed: a reload of the app guard's mode does not move it.
type listeners struct {
	app  *module.App
	app0 *libauth.Guard
	base libauth.Config // the app guard's config; Mode and Verifier are set per guard

	mu     sync.Mutex // guards byMode and orders verifier changes with guard creation
	byMode map[libauth.Mode]*libauth.Guard
}

// listenerKey carries, in a request context, the guard of the listener the request
// arrived on (WithMode, MiddlewareMode, httphost.ServeOptions.AuthMode).
type listenerKey struct{}

func (l *listeners) guard(m libauth.Mode) (*libauth.Guard, error) {
	mode, err := libauth.ParseMode(string(m))
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if g := l.byMode[mode]; g != nil {
		return g, nil
	}
	cfg := l.base
	cfg.Mode = mode
	cur, _ := l.app.Value(VerifierKey)
	cfg.Verifier, _ = cur.(libauth.Verifier)
	g, err := libauth.New(cfg)
	if err != nil {
		if mode == libauth.ModeEnforce && cfg.Verifier == nil {
			return nil, fmt.Errorf("auth: a listener in mode enforce needs a verifier: set jwks_url, bootstrap_key_file or api_keys (or select the identity module)")
		}
		return nil, fmt.Errorf("auth: listener guard: %w", err)
	}
	l.byMode[mode] = g
	return g, nil
}

// setVerifier installs v on the app guard and every listener guard, each in its own
// mode, and publishes it, as one step with respect to guard creation.
func (l *listeners) setVerifier(v libauth.Verifier) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.app0.Apply(l.app0.Mode(), v); err != nil {
		return err
	}
	for _, g := range l.byMode {
		if err := g.Apply(g.Mode(), v); err != nil {
			return err
		}
	}
	l.app.Set(VerifierKey, v)
	return nil
}

// WithMode returns ctx carrying the guard for mode m. Read by httphost.Serve.
func (l *listeners) WithMode(ctx context.Context, m libauth.Mode) (context.Context, error) {
	g, err := l.guard(m)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, listenerKey{}, g), nil
}

// GuardFor returns the listener's guard carried by ctx, or the app guard. Read by mcp.
func (l *listeners) GuardFor(ctx context.Context) *libauth.Guard {
	if g, ok := ctx.Value(listenerKey{}).(*libauth.Guard); ok {
		return g
	}
	return l.app0
}

// Middleware guards next with the guard for each request: its listener's, or the
// app's. Read by httphost for /api/ and declared routes, and by mcp for /mcp.
func (l *listeners) Middleware(next http.Handler) http.Handler {
	appH := l.app0.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g, ok := r.Context().Value(listenerKey{}).(*libauth.Guard); ok {
			g.Middleware(next).ServeHTTP(w, r)
			return
		}
		appH.ServeHTTP(w, r)
	})
}

func listenersOf(app *module.App) *listeners {
	v, _ := app.Value(ListenersKey)
	l, _ := v.(*listeners)
	return l
}

// WithMode returns ctx carrying a guard in mode m for every request served under it:
// the guard httphost puts on /api/, declared routes and /mcp, Middleware, and MCP tool
// checks then apply m instead of the app's mode. It shares the app guard's verifiers
// (AddVerifier included), scope table and open paths; only the mode differs, and it is
// fixed. It is what httphost.ServeOptions.AuthMode does to a listener's base context;
// use it as the BaseContext of a server the app runs itself. Mode enforce needs a
// verifier. The bind is not checked here: check it with libauth.ValidateBind(addr, m).
func WithMode(ctx context.Context, app *module.App, m libauth.Mode) (context.Context, error) {
	l := listenersOf(app)
	if l == nil {
		return nil, fmt.Errorf("auth module is not started")
	}
	return l.WithMode(ctx, m)
}

// GuardFor returns the guard that applies to a request with ctx: its listener's (see
// WithMode) or the app's. Nil when the auth module is not started.
func GuardFor(ctx context.Context, app *module.App) *libauth.Guard {
	if l := listenersOf(app); l != nil {
		return l.GuardFor(ctx)
	}
	return nil
}

// MiddlewareMode guards next in mode m whatever the app's mode, and carries that guard
// in the request context (WithMode) so guards and MCP tool checks inside next apply m
// too. For a listener the app serves without httphost.Serve; with Serve, set
// ServeOptions.AuthMode instead. A guard inside next (httphost.Handler's) checks the
// request again in the same mode, so its audit events appear twice.
func MiddlewareMode(app *module.App, m libauth.Mode, next http.Handler) (http.Handler, error) {
	l := listenersOf(app)
	if l == nil {
		return nil, fmt.Errorf("auth module is not started")
	}
	g, err := l.guard(m)
	if err != nil {
		return nil, err
	}
	h := g.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), listenerKey{}, g)))
	}), nil
}
