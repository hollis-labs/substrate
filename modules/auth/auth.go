// Package auth is the identity module: libs security/auth wired from config.
// Substrate supplies the guard; the app owns identity (issuer, keys) and the
// route/tool scope table.
//
// Config under "auth":
//
//	mode:               off | permissive | enforce. Default: HOLLIS_AUTH_<APP>,
//	                    then HOLLIS_AUTH, then off (dev).
//	issuer, audience:   JWT checks; audience defaults to the app name.
//	jwks_url:           issuer JWKS endpoint (enables the JWT verifier).
//	bootstrap_key_file: JWKS JSON or PEM public key used until the first fetch.
//	api_keys:           [{id, hash, scopes: [..]}] static service keys; hash is
//	                    the hex sha256 of the key (never the raw key).
//	denylist_url:       the issuer's deny-list endpoint (revoked jti, sub, sid), polled
//	                    with ETag revalidation; needs the JWT verifier.
//	denylist_interval_seconds: poll interval (default 30).
//
// Outbound (the app or its CLI calling another service, or its own daemon):
//
//	token_file:           a file holding a non-expiring service token, re-read on
//	                      every use so a rotated file is picked up.
//	mint_url:             an issuer endpoint minting short-lived tokens (Tether's
//	                      POST /auth/token); the token is refreshed at two thirds of
//	                      its life. Needs mint_credential_file.
//	mint_credential_file: the parent bearer credential presented to mint_url.
//	mint_body:            the JSON object sent to mint_url.
//
// auth.Token, auth.Transport and auth.HTTPClient attach the configured token. With
// neither token_file nor mint_url set (the dev default) they attach nothing.
//
// After boot.Start, the app registers routes and tools on ScopeTable(app).
// httphost wraps /api/ with the guard when this module is started.
package auth

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/ui"
)

//go:embed ui.json
var uiJSON []byte

// UI declares GET /api/me: the caller's own identity, for any verified caller.
func (*mod) UI() ui.Contribution { return ui.MustParse(uiJSON) }

// me answers who the caller is and which mode the guard is in. A permissive or
// off-mode caller without a token is reported as unauthenticated, not as an error.
func me(g *libauth.Guard) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"mode": string(g.Mode()), "authenticated": false}
		if p, ok := libauth.FromContext(r.Context()); ok {
			out["authenticated"], out["principal"] = p.Verified, p
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// Keys under which the guard and scope table are published. httphost reads
// GuardKey by name, so it does not import this package.
const (
	GuardKey = "auth.guard"
	ScopeKey = "auth.scopes"
	TokenKey = "auth.token_source"
	// VerifierKey holds the libauth.Verifier chain (nil when none is configured), for
	// transports that identify a caller once instead of per request, such as MCP stdio.
	VerifierKey = "auth.verifier"
)

func init() { module.Register(&mod{}) }

type mod struct{ cancel context.CancelFunc }

func (*mod) Name() string { return "auth" }

func (m *mod) Start(ctx context.Context, app *module.App) error {
	cfg, _ := app.Config["auth"].(map[string]any)
	str := func(k, def string) string {
		if s, ok := cfg[k].(string); ok && s != "" {
			return s
		}
		return def
	}

	mode, err := libauth.ModeFromEnv(app.Name, libauth.ModeOff)
	if err != nil {
		return err
	}
	if s := str("mode", ""); s != "" {
		if mode, err = libauth.ParseMode(s); err != nil {
			return err
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	var verifiers []libauth.Verifier
	dlURL := str("denylist_url", "")
	url, file := str("jwks_url", ""), str("bootstrap_key_file", "")
	if dlURL != "" && url == "" && file == "" {
		cancel()
		return fmt.Errorf("denylist_url needs the JWT verifier: set jwks_url or bootstrap_key_file")
	}
	if url != "" || file != "" {
		var deny *libauth.DenyList
		if dlURL != "" {
			deny = libauth.NewDenyList()
			iv := 30 * time.Second
			if n, ok := cfg["denylist_interval_seconds"].(float64); ok && n > 0 {
				iv = time.Duration(n * float64(time.Second))
			}
			go (&libauth.DenyListPoller{URL: dlURL, List: deny, Interval: iv}).Run(runCtx)
		}
		jv, err := libauth.NewJWTVerifier(libauth.JWTConfig{
			Issuer:           str("issuer", ""),
			Audience:         str("audience", app.Name),
			JWKSURL:          url,
			BootstrapKeyFile: file,
			DenyList:         deny,
		})
		if err != nil {
			cancel()
			return fmt.Errorf("jwt verifier: %w", err)
		}
		if url != "" {
			go jv.Run(runCtx)
		}
		verifiers = append(verifiers, jv)
	}
	keys, err := apiKeys(cfg["api_keys"])
	if err != nil {
		cancel()
		return err
	}
	if len(keys) > 0 {
		verifiers = append(verifiers, libauth.NewAPIKeyVerifier(keys...))
	}
	// An app that selects the identity module is its own issuer: the module adds
	// its verifier after this one starts (AddVerifier), so none is needed here.
	_, localIdentity := app.Config["identity"].(map[string]any)
	if mode == libauth.ModeEnforce && len(verifiers) == 0 && !localIdentity {
		cancel()
		return fmt.Errorf("mode enforce needs a verifier: set jwks_url, bootstrap_key_file or api_keys (or select the identity module)")
	}

	scopes := libauth.NewScopeTable()
	gc := libauth.Config{Mode: mode, Scopes: scopes, Logger: slog.New(quietNotEnforcing{}), OpenPath: func(p string) bool { return module.IsPublic(app, p) }}
	switch {
	case len(verifiers) > 0:
		gc.Verifier = libauth.Chain(verifiers...)
	case localIdentity:
		gc.Verifier = refuseAll{} // until the identity module adds its own
	}
	g, err := libauth.New(gc)
	if err != nil {
		cancel()
		return fmt.Errorf("guard: %w", err)
	}
	m.cancel = cancel
	ts, err := tokenSource(runCtx, cfg)
	if err != nil {
		cancel()
		return err
	}
	app.Set(GuardKey, g)
	app.Set(ScopeKey, scopes)
	if ts != nil {
		app.Set(TokenKey, ts)
	}
	if gc.Verifier != nil {
		app.Set(VerifierKey, gc.Verifier)
	}
	if err := module.AddRoutes(app, m.UI(), map[string]http.Handler{"GET /api/me": me(g)}); err != nil {
		cancel()
		return err
	}
	return nil
}

func (m *mod) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}

// NotEnforcingMsg is the message libs logs when a guard is built in a mode other
// than enforce. The auth module silences it at Start (a short-lived command in a
// CLI-variant app would print it on every run) and httphost.Serve logs it where a
// listener starts.
const NotEnforcingMsg = "auth is not enforcing"

// quietNotEnforcing is the guard's logger: the process default logger (looked up
// per record, so a logging module that starts later is honoured) without the
// startup "not enforcing" warning.
type quietNotEnforcing struct {
	attrs []slog.Attr
	group string
}

func (q quietNotEnforcing) base() slog.Handler {
	h := slog.Default().Handler()
	if len(q.attrs) > 0 {
		h = h.WithAttrs(q.attrs)
	}
	if q.group != "" {
		h = h.WithGroup(q.group)
	}
	return h
}

func (q quietNotEnforcing) Enabled(ctx context.Context, l slog.Level) bool {
	return q.base().Enabled(ctx, l)
}

func (q quietNotEnforcing) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == NotEnforcingMsg {
		return nil
	}
	return q.base().Handle(ctx, r)
}

func (q quietNotEnforcing) WithAttrs(a []slog.Attr) slog.Handler {
	q.attrs = append(append([]slog.Attr{}, q.attrs...), a...)
	return q
}

func (q quietNotEnforcing) WithGroup(n string) slog.Handler { q.group = n; return q }

// refuseAll is the verifier of an app whose only verifier is added later: until
// then every credential is invalid.
type refuseAll struct{}

func (refuseAll) Verify(context.Context, string) (libauth.Principal, error) {
	return libauth.Principal{}, libauth.ErrInvalidToken
}

// AddVerifier makes the guard also accept credentials v resolves. It is how a
// module that issues credentials (identity) gets them accepted by the app's one
// guard, in the mode the guard is already in. Call it from the module's Start,
// after auth has started.
func AddVerifier(app *module.App, v libauth.Verifier) error {
	g := Guard(app)
	if g == nil {
		return fmt.Errorf("auth module is not started")
	}
	current, _ := app.Value(VerifierKey)
	cur, _ := current.(libauth.Verifier)
	next := v
	if _, isRefuse := cur.(refuseAll); cur != nil && !isRefuse {
		next = libauth.Chain(cur, v)
	}
	if err := g.Apply(g.Mode(), next); err != nil {
		return err
	}
	app.Set(VerifierKey, next)
	return nil
}

// Guard returns the guard published by the auth module, or nil.
func Guard(app *module.App) *libauth.Guard {
	v, _ := app.Value(GuardKey)
	g, _ := v.(*libauth.Guard)
	return g
}

// ScopeTable returns the table the app registers routes and tools on, or nil.
func ScopeTable(app *module.App) *libauth.ScopeTable {
	v, _ := app.Value(ScopeKey)
	t, _ := v.(*libauth.ScopeTable)
	return t
}

// Verify checks a token against the configured verifiers (JWT, API keys) and
// returns the principal. It errors when no verifier is configured.
func Verify(ctx context.Context, app *module.App, token string) (libauth.Principal, error) {
	v, _ := app.Value(VerifierKey)
	vf, ok := v.(libauth.Verifier)
	if !ok {
		return libauth.Principal{}, fmt.Errorf("auth: no verifier is configured")
	}
	return vf.Verify(ctx, token)
}

// ValidateBind refuses a listener address the configured mode may not use
// (off and permissive are loopback, tailnet or unix socket only).
func ValidateBind(app *module.App, addr string) error {
	g := Guard(app)
	if g == nil {
		return fmt.Errorf("auth module is not started")
	}
	return libauth.ValidateBind(addr, g.Mode())
}

// Middleware is a convenience for apps that mount their own handlers.
func Middleware(app *module.App, next http.Handler) http.Handler {
	if g := Guard(app); g != nil {
		return g.Middleware(next)
	}
	return next
}

func apiKeys(v any) ([]libauth.APIKey, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, nil
	}
	var out []libauth.APIKey
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("auth.api_keys[%d]: want a mapping", i)
		}
		id, _ := m["id"].(string)
		hash, _ := m["hash"].(string)
		if id == "" || hash == "" {
			return nil, fmt.Errorf("auth.api_keys[%d]: id and hash are required", i)
		}
		k := libauth.APIKey{ID: id, Hash: hash}
		if ss, ok := m["scopes"].([]any); ok {
			for _, s := range ss {
				if str, ok := s.(string); ok {
					k.Scopes = append(k.Scopes, str)
				}
			}
		}
		out = append(out, k)
	}
	return out, nil
}
