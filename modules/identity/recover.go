package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/substrate/apierr"
)

// Account recovery, offline: someone locked out (a lost passkey, authenticator and recovery
// codes, a forgotten password) with no other admin to help. Whoever can run the binary against
// the app's data ("<app> identity recover --user <name>", like setup-link) gets a single-use,
// short-lived link, <url>/recover#r=<token>. Opening it signs that user in and lets them set a
// new password and clear their passkeys and TOTP; their other sessions end. Only the token's
// hash is stored, and minting and using it are logged.
//
//	POST /api/recover/check  public: {token} -> {username, display_name, totp, passkeys, passkey_only}
//	POST /api/recover        public: {token, new_password?, clear_passkeys, clear_totp} -> session

// DefaultRecoveryTTL is how long a recovery link stays valid.
const DefaultRecoveryTTL = 30 * time.Minute

const (
	maxRecoveryTTL = 24 * time.Hour
	recoverPrefix  = "recover_"
	// MethodRecoveryLink is a session started from a recovery link.
	MethodRecoveryLink = "recovery"
)

// ErrRecoveryToken is a recovery token that is unknown, used or expired.
var ErrRecoveryToken = errors.New("identity: the recovery link is not valid (unknown, used or expired)")

// RecoveryLink mints a single-use recovery token for the user named username, valid for ttl
// (DefaultRecoveryTTL when 0, at most 24h). The token is returned once.
func (s *Service) RecoveryLink(ctx context.Context, username string, ttl time.Duration) (string, time.Time, User, error) {
	if ttl <= 0 {
		ttl = DefaultRecoveryTTL
	}
	if ttl > maxRecoveryTTL {
		return "", time.Time{}, User{}, errors.New("a recovery link lasts at most 24 hours")
	}
	u, err := s.db.userByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return "", time.Time{}, User{}, ErrNotFound
	}
	secret := randomString(32)
	now := s.now()
	exp := now.Add(ttl)
	if _, err := s.db.st.ExecContext(ctx, s.db.q(`INSERT INTO identity_recovery_links (id, token_hash, user_id, created_at, expires_at) VALUES (?,?,?,?,?)`),
		newID("rec_"), hashToken(secret), u.ID, now.Unix(), exp.Unix()); err != nil {
		return "", time.Time{}, User{}, err
	}
	slog.Warn("identity: recovery link minted", "user", u.Username, "expires", exp.UTC().Format(time.RFC3339))
	return recoverPrefix + secret, exp, u, nil
}

// RecoverURL is the browser link for a recovery token (in the fragment, out of logs).
func RecoverURL(base, token string) string {
	return strings.TrimRight(base, "/") + "/recover#r=" + token
}

// recoveryUser is the live (unused, unexpired) link's user.
func (s *Service) recoveryUser(ctx context.Context, token string) (User, error) {
	secret, ok := strings.CutPrefix(strings.TrimSpace(token), recoverPrefix)
	if !ok || secret == "" {
		return User{}, ErrRecoveryToken
	}
	var userID string
	var exp, used int64
	err := s.db.st.QueryRowContext(ctx, s.db.q(`SELECT user_id, expires_at, used_at FROM identity_recovery_links WHERE token_hash = ?`), hashToken(secret)).Scan(&userID, &exp, &used)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (used != 0 || s.now().Unix() >= exp)) {
		return User{}, ErrRecoveryToken
	}
	if err != nil {
		return User{}, err
	}
	u, err := s.db.userByID(ctx, userID)
	if err != nil {
		return User{}, ErrRecoveryToken
	}
	return u, nil
}

type recoverChanges struct {
	passwordHash  string // "" keeps the password
	clearPasskeys bool
	clearTOTP     bool
}

// recover spends the link and applies the changes, atomically, and ends the user's sessions.
func (d db) recover(ctx context.Context, token, userID string, ch recoverChanges, now time.Time) error {
	secret := strings.TrimPrefix(strings.TrimSpace(token), recoverPrefix)
	return d.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		res, err := tx.ExecContext(ctx, d.q(`UPDATE identity_recovery_links SET used_at = ? WHERE token_hash = ? AND user_id = ? AND used_at = 0 AND expires_at > ?`),
			now.Unix(), hashToken(secret), userID, now.Unix())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrRecoveryToken
		}
		exec := func(q string, args ...any) error { _, err := tx.ExecContext(ctx, d.q(q), args...); return err }
		if ch.passwordHash != "" {
			if err := exec(`UPDATE identity_users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, ch.passwordHash, now.Unix(), now.Unix(), userID); err != nil {
				return err
			}
		}
		if ch.clearPasskeys {
			if err := exec(`DELETE FROM identity_passkeys WHERE user_id = ?`, userID); err != nil {
				return err
			}
			if err := exec(`UPDATE identity_users SET passkey_only = 0 WHERE id = ?`, userID); err != nil {
				return err
			}
		}
		if ch.clearTOTP {
			if err := exec(`DELETE FROM identity_totp WHERE user_id = ?`, userID); err != nil {
				return err
			}
			if err := exec(`DELETE FROM identity_recovery_codes WHERE user_id = ?`, userID); err != nil {
				return err
			}
		}
		return exec(`UPDATE identity_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at = 0`, now.Unix(), userID)
	})
}

// ---- handlers ----

func (s *Service) recoverRoutes(h func(http.HandlerFunc) http.Handler) map[string]http.Handler {
	return map[string]http.Handler{
		"POST /api/recover/check": h(s.recoverCheck),
		"POST /api/recover":       h(s.recoverApply),
	}
}

func recoveryInvalid(w http.ResponseWriter, app string) {
	apierr.Write(w, http.StatusForbidden, "recovery_link_invalid", "this recovery link is not valid: it was used, it expired, or it is wrong. Make a new one with `"+app+" identity recover --user <name>`.", nil)
}

// recoverAttempt checks origin, header and rate limit; false has answered.
func (s *Service) recoverAttempt(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.sameOrigin(r) {
		apierr.Write(w, http.StatusForbidden, "bad_origin", "the request did not come from this app", nil)
		return "", false
	}
	if r.Header.Get("X-Requested-With") == "" {
		apierr.Write(w, http.StatusForbidden, "csrf", "missing X-Requested-With", nil)
		return "", false
	}
	key := "recover|" + clientAddr(r)
	if wait := s.limiter.wait(key, s.now()); wait > 0 {
		tooMany(w, wait)
		return "", false
	}
	return key, true
}

func (s *Service) recoverCheck(w http.ResponseWriter, r *http.Request) {
	key, ok := s.recoverAttempt(w, r)
	if !ok {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.recoveryUser(r.Context(), in.Token)
	if err != nil {
		s.limiter.fail(key, s.now())
		recoveryInvalid(w, s.app.Name)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": u.Username, "display_name": u.DisplayName,
		"totp": s.db.totpEnabled(r.Context(), u.ID), "passkeys": s.db.passkeyCount(r.Context(), u.ID),
		"passkey_only": s.db.passkeyOnly(r.Context(), u.ID)})
}

func (s *Service) recoverApply(w http.ResponseWriter, r *http.Request) {
	key, ok := s.recoverAttempt(w, r)
	if !ok {
		return
	}
	var in struct {
		Token         string `json:"token"`
		NewPassword   string `json:"new_password"`
		ClearPasskeys bool   `json:"clear_passkeys"`
		ClearTOTP     bool   `json:"clear_totp"`
	}
	if !decode(w, r, &in) {
		return
	}
	now := s.now()
	u, err := s.recoveryUser(r.Context(), in.Token)
	if err != nil {
		s.limiter.fail(key, now)
		recoveryInvalid(w, s.app.Name)
		return
	}
	var ch recoverChanges
	if in.NewPassword != "" {
		if err := s.checkPassword(in.NewPassword); err != nil {
			bad(w, err.Error())
			return
		}
		if ch.passwordHash, err = s.hashPassword(in.NewPassword); err != nil {
			failed(w)
			return
		}
	}
	ch.clearPasskeys, ch.clearTOTP = in.ClearPasskeys, in.ClearTOTP
	switch err := s.db.recover(r.Context(), in.Token, u.ID, ch, now); {
	case errors.Is(err, ErrRecoveryToken):
		s.limiter.fail(key, now)
		recoveryInvalid(w, s.app.Name)
		return
	case err != nil:
		failed(w)
		return
	}
	s.limiter.clear(key)
	slog.Warn("identity: account recovered with a recovery link", "user", u.Username, "from", clientAddr(r),
		"password_reset", ch.passwordHash != "", "passkeys_cleared", ch.clearPasskeys, "totp_cleared", ch.clearTOTP)
	s.startSession(w, r, u, MethodRecoveryLink)
}

// recoverCmd is "<app> identity recover --user NAME [--url URL] [--ttl 30m]".
func (s *Service) recoverCmd() *cobra.Command {
	var base, user string
	var ttl time.Duration
	c := &cobra.Command{
		Use:   "recover",
		Short: "Print a single-use link that signs a locked-out user in to reset their sign-in",
		Long: "Mints a single-use recovery link for --user (valid for --ttl, default 30m) and prints it. Opening it signs\n" +
			"that user in and lets them set a new password and remove their passkeys and two-step sign-in; their other\n" +
			"sessions end. For an owner with no other admin to help. Only whoever can run this binary against the app's\n" +
			"data can make one. Minting and using it are logged.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			c.SilenceUsage = true
			if strings.TrimSpace(user) == "" {
				return errors.New("--user is required")
			}
			token, exp, u, err := s.RecoveryLink(c.Context(), user, ttl)
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("no user %q", user)
			}
			if err != nil {
				return err
			}
			if base == "" {
				_, _ = fmt.Fprintln(c.OutOrStdout(), token)
				_, _ = fmt.Fprintf(c.ErrOrStderr(), "open <app URL>/recover#r=<that token> in a browser before %s (or pass --url); it signs in as %s, once\n", exp.Local().Format(time.Kitchen), u.Username)
				return nil
			}
			_, _ = fmt.Fprintln(c.OutOrStdout(), RecoverURL(base, token))
			_, _ = fmt.Fprintf(c.ErrOrStderr(), "open it in a browser before %s; it signs in as %s, once\n", exp.Local().Format(time.Kitchen), u.Username)
			return nil
		},
	}
	c.Flags().StringVar(&user, "user", "", "the username to recover")
	c.Flags().StringVar(&base, "url", "", "the app's URL, e.g. https://app.example.com (print the whole link)")
	c.Flags().DurationVar(&ttl, "ttl", DefaultRecoveryTTL, "how long the link stays valid (at most 24h)")
	return c
}
