// Package identity lets an app be its own identity provider: the mechanism for
// accounts, sign-in, sessions, API keys, app-to-app pairing and token minting,
// so an app stands alone and decides how far to integrate with others. The app
// owns the users and the policy; this module supplies the machinery.
//
// What it gives an app
//
//	people    first-run setup (the owner), users, password sign-in and sign-out,
//	          password change, profile, session list and revoke. Browsers keep a
//	          session cookie; Middleware turns it into the bearer the guard reads.
//	machines  API keys with scopes and audiences, pairing codes another app
//	          redeems for a key, and POST /api/token, which trades a key or a
//	          session for a short-lived EdDSA JWT. That endpoint has Tether's
//	          mint shape, so a client configures auth.mint_url at it.
//	trust     GET /api/identity/jwks publishes the signing key. Another app trusts
//	          this one by setting its auth.jwks_url to it; this app can equally
//	          trust any other issuer by setting auth.jwks_url, or none.
//
// Sessions, API keys and local JWTs are accepted by the app's auth guard through
// auth.AddVerifier, in whatever mode the guard is in. Selecting this module in
// enforce mode needs no other verifier.
//
// Config under "identity" (the block must exist, even empty, when the module is
// selected; the auth module reads its presence):
//
//	key_file:        Ed25519 signing key (PEM), created 0600 on first start.
//	                 Default "identity-signing.key" in the working directory. A
//	                 public JWKS is written beside it ("<key_file>.jwks.json").
//	issuer:          the iss claim. Default: the app name.
//	audience:        the aud of tokens for this app. Default: the app name.
//	session_ttl:     absolute session life. Default "720h".
//	token_ttl:       default token life. Default "15m"; the most a caller may ask is 1h.
//	min_password:    minimum password length. Default 12.
//	bcrypt_cost:     password hashing cost. Default 12.
//	secure_cookies:  "auto" (default: secure when the request arrived over TLS or
//	                 trusted_proxy is set and says https), "always" or "never".
//	trusted_proxy:   true to believe a single X-Forwarded-Proto from the proxy.
//	retired_key_ttl: how long a rotated-out signing key stays published and
//	                 verifies. Default and minimum "70m" (the longest token life
//	                 plus ten minutes); a lower value refuses to start.
//	allowed_origins: extra exact origins whose cookie-authenticated writes are
//	                 accepted; the request's own origin always is.
//
// Signing-key rotation: POST /api/identity/signing-key/rotate (scope
// "<app>.admin", body {"compromised": bool}) swaps the signing key in the
// running process; GET /api/identity/signing-key reports the active and retired
// keys. The old key stays in the published set and keeps verifying until
// retired_key_ttl has passed, so no token dies before its own exp, unless
// "compromised" is set, which drops it at once. Single process per key_file.
// Boot hooks (boot.Options) must not register modules; nothing here does.
//
// Strict-mode extras (passkeys, signed operation approval) are not here; they
// belong to a later, opt-in layer on the same users and sessions.
package identity

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/ui"
)

//go:embed ui.json
var uiJSON []byte

// Key under which the *Service is published.
const Key = "identity.service"

func init() { module.Register(&mod{}) }

type mod struct{ svc *Service }

func (*mod) Name() string { return "identity" }

// UI declares the identity routes with their scopes.
func (*mod) UI() ui.Contribution { return ui.MustParse(uiJSON) }

// From returns the service of a started identity module, or nil.
func From(app *module.App) *Service {
	v, _ := app.Value(Key)
	s, _ := v.(*Service)
	return s
}

// Service is the running identity module.
type Service struct {
	app *module.App
	db  db
	cfg config

	keys  atomic.Pointer[keyState] // the signing key ring; see keyring.go
	rot   sync.Mutex               // serialises rotation and pruning
	now   func() time.Time
	fault func(point string) error // test hook at rotation steps; nil in production

	limiter *limiter
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type config struct {
	issuer, audience, cookie string
	sessionTTL, tokenTTL     time.Duration
	minPassword, cost        int
	secure                   string
	trustedProxy             bool
	origins                  map[string]bool
	keyFile                  string
	retiredTTL               time.Duration
}

const maxTokenTTL = time.Hour

func (m *mod) Start(ctx context.Context, app *module.App) error {
	cfg, err := parseConfig(app)
	if err != nil {
		return err
	}
	st := database.From(app)
	if st == nil {
		return errors.New("identity: needs the database module")
	}
	svc := &Service{app: app, db: db{st}, cfg: cfg, limiter: newLimiter(), now: time.Now}
	if err := svc.db.migrate(ctx); err != nil {
		return err
	}
	if err := svc.loadKey(); err != nil {
		return err
	}
	if err := auth.AddVerifier(app, svc); err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	svc.cancel = cancel
	svc.wg.Add(1)
	go svc.sweep(runCtx)
	m.svc = svc
	app.Set(Key, svc)
	return module.AddRoutes(app, m.UI(), svc.handlers())
}

func (m *mod) Stop(context.Context) error {
	if m.svc != nil && m.svc.cancel != nil {
		m.svc.cancel()
		m.svc.wg.Wait()
		m.svc = nil
	}
	return nil
}

func parseConfig(app *module.App) (config, error) {
	m, _ := app.Config["identity"].(map[string]any)
	if m == nil {
		return config{}, errors.New(`identity: add an "identity" block to the manifest config (it may be empty)`)
	}
	str := func(k, def string) string {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
		return def
	}
	dur := func(k, def string) (time.Duration, error) {
		d, err := time.ParseDuration(str(k, def))
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("identity: %s must be a positive duration", k)
		}
		return d, nil
	}
	num := func(k string, def int) int {
		if n, ok := m[k].(float64); ok && n > 0 {
			return int(n)
		}
		return def
	}
	c := config{
		issuer: str("issuer", app.Name), audience: str("audience", app.Name),
		cookie: app.Name + "_session", keyFile: str("key_file", "identity-signing.key"),
		minPassword: num("min_password", 12), cost: num("bcrypt_cost", 12), secure: str("secure_cookies", "auto"),
		origins: map[string]bool{},
	}
	c.trustedProxy, _ = m["trusted_proxy"].(bool)
	var err error
	if c.sessionTTL, err = dur("session_ttl", "720h"); err != nil {
		return c, err
	}
	if c.tokenTTL, err = dur("token_ttl", "15m"); err != nil {
		return c, err
	}
	if c.tokenTTL > maxTokenTTL {
		return c, errors.New("identity: token_ttl is at most 1h")
	}
	if c.retiredTTL, err = dur("retired_key_ttl", "70m"); err != nil {
		return c, err
	}
	if c.retiredTTL < minRetiredTTL {
		return c, fmt.Errorf("identity: retired_key_ttl %s is below the minimum %s (an hour of token life plus ten minutes of skew); a lower value would cut off live tokens", c.retiredTTL, minRetiredTTL)
	}
	if c.cost < 4 || c.cost > 14 {
		return c, errors.New("identity: bcrypt_cost must be between 4 and 14")
	}
	switch c.secure {
	case "auto", "always", "never":
	default:
		return c, errors.New(`identity: secure_cookies must be "auto", "always" or "never"`)
	}
	if l, ok := m["allowed_origins"].([]any); ok {
		for _, o := range l {
			if s, ok := o.(string); ok {
				c.origins[strings.TrimRight(s, "/")] = true
			}
		}
	}
	return c, nil
}

// ---- the verifier the guard consults ----

// Verify implements libauth.Verifier for sessions, API keys and local tokens.
func (s *Service) Verify(ctx context.Context, token string) (libauth.Principal, error) {
	now := time.Now()
	switch {
	case strings.HasPrefix(token, sessionPrefix):
		sess, u, err := s.db.sessionByToken(ctx, strings.TrimPrefix(token, sessionPrefix), now)
		if err != nil {
			return libauth.Principal{}, libauth.ErrInvalidToken
		}
		s.db.touchSession(ctx, sess.ID, now)
		return libauth.Principal{Verified: true, ID: userPrincipal(u.ID), Kind: "user", Display: u.DisplayName,
			Scopes: u.Scopes, Audience: s.cfg.audience, SessionID: sess.ID, ExpiresAt: sess.ExpiresAt}, nil
	case strings.HasPrefix(token, keyPrefix):
		c, err := s.db.credentialByToken(ctx, strings.TrimPrefix(token, keyPrefix), now)
		if err != nil {
			return libauth.Principal{}, libauth.ErrInvalidToken
		}
		return libauth.Principal{Verified: true, ID: "key:" + c.ID, Kind: "service", Display: c.Name,
			Scopes: c.Scopes, Audience: s.cfg.audience, TokenID: c.ID, ExpiresAt: c.ExpiresAt}, nil
	}
	return s.current().jwt.Verify(ctx, token)
}

const (
	sessionPrefix = "ses_"
	keyPrefix     = "key_"
)

func userPrincipal(id string) string { return "user:" + id }

// sweep drops expired and revoked rows now and then.
func (s *Service) sweep(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.prune(); err != nil {
				slog.Warn("identity: pruning retired signing keys", "error", err)
			}
			cutoff := time.Now().Add(-24 * time.Hour).Unix()
			_, _ = s.db.st.ExecContext(ctx, s.db.q(`DELETE FROM identity_sessions WHERE expires_at < ? OR (revoked_at <> 0 AND revoked_at < ?)`), cutoff, cutoff)
			_, _ = s.db.st.ExecContext(ctx, s.db.q(`DELETE FROM identity_pairing_codes WHERE expires_at < ?`), cutoff)
		}
	}
}
