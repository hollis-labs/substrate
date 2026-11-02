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
//
// After boot.Start, the app registers routes and tools on ScopeTable(app).
// httphost wraps /api/ with the guard when this module is started.
package auth

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

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
	if url, file := str("jwks_url", ""), str("bootstrap_key_file", ""); url != "" || file != "" {
		jv, err := libauth.NewJWTVerifier(libauth.JWTConfig{
			Issuer:           str("issuer", ""),
			Audience:         str("audience", app.Name),
			JWKSURL:          url,
			BootstrapKeyFile: file,
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
	if mode == libauth.ModeEnforce && len(verifiers) == 0 {
		cancel()
		return fmt.Errorf("mode enforce needs a verifier: set jwks_url, bootstrap_key_file or api_keys")
	}

	scopes := libauth.NewScopeTable()
	gc := libauth.Config{Mode: mode, Scopes: scopes, OpenPath: func(string) bool { return false }}
	if len(verifiers) > 0 {
		gc.Verifier = libauth.Chain(verifiers...)
	}
	g, err := libauth.New(gc)
	if err != nil {
		cancel()
		return fmt.Errorf("guard: %w", err)
	}
	m.cancel = cancel
	app.Set(GuardKey, g)
	app.Set(ScopeKey, scopes)
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
