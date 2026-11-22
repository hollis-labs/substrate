package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/substrate/apierr"
)

// Setup links: the friendly first run. Whoever can run the binary against the data
// ("<app> identity setup-link", or an installer calling Service.SetupLink) gets a
// single-use token, and the owner finishes setup in the browser at
// <url>/setup#t=<token>. The token is the authority, not the network: POST /api/setup
// with a valid token creates the owner even under identity.setup: offline, which keeps
// refusing the open form. Only the token's hash is stored; it expires (default 30m),
// works once, and is useless once an owner exists.

// DefaultSetupLinkTTL is how long a setup link stays valid.
const DefaultSetupLinkTTL = 30 * time.Minute

const setupTokenPrefix = "setup_"

// ErrSetupToken is a setup token that is unknown, used or expired.
var ErrSetupToken = errors.New("identity: the setup link is not valid (unknown, used or expired)")

// SetupLink mints a single-use setup token valid for ttl (DefaultSetupLinkTTL when 0).
// It refuses (ErrSetupClosed) once an owner exists. The token is returned once; only its
// hash is stored.
func (s *Service) SetupLink(ctx context.Context, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = DefaultSetupLinkTTL
	}
	if ttl > 7*24*time.Hour {
		return "", time.Time{}, errors.New("a setup link lasts at most 7 days")
	}
	exists, err := s.OwnerExists(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	if exists {
		return "", time.Time{}, ErrSetupClosed
	}
	secret := randomString(32)
	now := s.now()
	exp := now.Add(ttl)
	_, err = s.db.st.ExecContext(ctx, s.db.q(`INSERT INTO identity_setup_tokens (id, token_hash, created_at, expires_at) VALUES (?,?,?,?)`),
		newID("stk_"), hashToken(secret), now.Unix(), exp.Unix())
	if err != nil {
		return "", time.Time{}, err
	}
	return setupTokenPrefix + secret, exp, nil
}

// SetupURL is the browser link for a setup token: base (the app's URL) + "/setup#t=" +
// token. The token rides in the fragment so it never reaches a server log or a referrer.
func SetupURL(base, token string) string {
	return strings.TrimRight(base, "/") + "/setup#t=" + token
}

// createOwnerWithToken spends a setup token and makes the owner, atomically: the token
// must be unused and unexpired, and no user may exist.
func (d db) createOwnerWithToken(ctx context.Context, token string, u User, now time.Time) error {
	secret, ok := strings.CutPrefix(token, setupTokenPrefix)
	if !ok || secret == "" {
		return ErrSetupToken
	}
	return d.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		res, err := tx.ExecContext(ctx, d.q(`UPDATE identity_setup_tokens SET used_at = ? WHERE token_hash = ? AND used_at = 0 AND expires_at > ?`),
			now.Unix(), hashToken(secret), now.Unix())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrSetupToken
		}
		var users int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM identity_users`).Scan(&users); err != nil {
			return err
		}
		if users > 0 {
			return ErrSetupClosed
		}
		if err := d.insertUser(ctx, tx, u); err != nil {
			return err
		}
		return d.setRole(ctx, tx, u.ID, RoleOwner, now)
	})
}

// setupWithToken is POST /api/setup carrying {setup_token, ...}: it needs the token,
// this app's origin and the X-Requested-With header (a cross-site form can set neither),
// and is rate-limited per client address like sign-in.
func (s *Service) setupWithToken(w http.ResponseWriter, r *http.Request, in setupInput) {
	if r.Header.Get("X-Requested-With") == "" {
		apierr.Write(w, http.StatusForbidden, "csrf", "missing X-Requested-With", nil)
		return
	}
	key := "setup|" + clientAddr(r)
	now := s.now()
	if wait := s.limiter.wait(key, now); wait > 0 {
		tooMany(w, wait)
		return
	}
	u, err := s.newUser(in.credentials, []string{"*"})
	if err != nil {
		bad(w, err.Error())
		return
	}
	switch err := s.db.createOwnerWithToken(r.Context(), in.SetupToken, u, now); {
	case errors.Is(err, ErrSetupToken):
		s.limiter.fail(key, now)
		apierr.Write(w, http.StatusForbidden, "setup_link_invalid", "this setup link is not valid: it was used, it expired, or it is wrong. Make a new one with `"+s.app.Name+" identity setup-link`.", nil)
		return
	case errors.Is(err, ErrSetupClosed):
		apierr.Write(w, http.StatusConflict, "setup_closed", "setup is already done", nil)
		return
	case errors.Is(err, ErrConflict):
		apierr.Write(w, http.StatusConflict, "exists", "that username is taken", nil)
		return
	case err != nil:
		failed(w)
		return
	}
	s.limiter.clear(key)
	s.startSession(w, r, u)
}

type setupInput struct {
	credentials
	SetupToken string `json:"setup_token"`
}

// setupLinkCmd is "<app> identity setup-link [--url URL] [--ttl 30m]": it prints the
// link (or, with no --url, the token and how to use it) on stdout and nothing else there.
func (s *Service) setupLinkCmd() *cobra.Command {
	var base string
	var ttl time.Duration
	c := &cobra.Command{
		Use:   "setup-link",
		Short: "Print a single-use link that creates the owner in the browser",
		Long: "Mints a single-use setup link (valid for --ttl, default 30m) and prints it. Open it in a browser to\n" +
			"create the owner and sign in. Refused once an owner exists. Only whoever can run this binary against\n" +
			"the app's data can make one.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			c.SilenceUsage = true
			token, exp, err := s.SetupLink(c.Context(), ttl)
			if errors.Is(err, ErrSetupClosed) {
				return errors.New("setup is already done: this instance has an owner")
			}
			if err != nil {
				return err
			}
			if base == "" {
				_, _ = fmt.Fprintln(c.OutOrStdout(), token)
				_, _ = fmt.Fprintf(c.ErrOrStderr(), "open <app URL>/setup#t=<that token> in a browser before %s (or pass --url)\n", exp.Local().Format(time.Kitchen))
				return nil
			}
			_, _ = fmt.Fprintln(c.OutOrStdout(), SetupURL(base, token))
			_, _ = fmt.Fprintf(c.ErrOrStderr(), "open it in a browser before %s; it works once\n", exp.Local().Format(time.Kitchen))
			return nil
		},
	}
	c.Flags().StringVar(&base, "url", "", "the app's URL, e.g. http://hub.example:8470 (print the whole link)")
	c.Flags().DurationVar(&ttl, "ttl", DefaultSetupLinkTTL, "how long the link stays valid (at most 168h)")
	return c
}
