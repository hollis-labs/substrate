package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
)

// The tables are prefixed identity_ and created idempotently at start, so the
// app's own migrations never mention them. Times are Unix seconds and flags are
// integers, which both SQLite and Postgres store the same way.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS identity_users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL,
		scopes TEXT NOT NULL DEFAULT '',
		disabled INTEGER NOT NULL DEFAULT 0,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		password_changed_at BIGINT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS identity_sessions (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL,
		last_seen_at BIGINT NOT NULL,
		user_agent TEXT NOT NULL DEFAULT '',
		revoked_at BIGINT NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS identity_credentials (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		scopes TEXT NOT NULL DEFAULT '',
		audiences TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL DEFAULT 0,
		revoked_at BIGINT NOT NULL DEFAULT 0,
		last_used_at BIGINT NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS identity_pairing_codes (
		id TEXT PRIMARY KEY,
		code_hash TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		scopes TEXT NOT NULL DEFAULT '',
		audiences TEXT NOT NULL DEFAULT '',
		key_ttl_seconds BIGINT NOT NULL DEFAULT 0,
		created_by TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL,
		used_at BIGINT NOT NULL DEFAULT 0
	)`,
}

// Errors the HTTP layer maps to responses.
var (
	ErrNotFound     = errors.New("identity: not found")
	ErrConflict     = errors.New("identity: already exists")
	ErrSetupClosed  = errors.New("identity: setup is closed")
	ErrInvalidLogin = errors.New("identity: invalid username or password")
)

type db struct {
	st *store.Store
}

func (d db) q(query string) string { return d.st.Dialect().Rebind(query) }

func (d db) migrate(ctx context.Context) error {
	for _, ddl := range schema {
		if _, err := d.st.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("identity: schema: %w", err)
		}
	}
	return nil
}

// User is an account.
type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email,omitempty"`
	Scopes      []string  `json:"scopes"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	passwordHash      string
	passwordChangedAt time.Time
}

// Session is one signed-in browser or client.
type Session struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current,omitempty"`
}

// Credential is a long-lived API key held by a person's tool or another app.
type Credential struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Scopes     []string  `json:"scopes"`
	Audiences  []string  `json:"audiences,omitempty"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at,omitzero"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

func fields(s string) []string { return strings.Fields(s) }

func newID(prefix string) string { return prefix + randomString(12) }

// randomString is n random bytes, base64url without padding.
func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("identity: no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken is the stored form of a high-entropy secret: a fast hash is right
// because the secret is 256 random bits, not a password.
func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// ---- users ----

const userCols = "id, username, display_name, email, password_hash, scopes, disabled, created_at, updated_at, password_changed_at"

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var scopes string
	var disabled int
	var created, updated, pwChanged int64
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.passwordHash, &scopes, &disabled, &created, &updated, &pwChanged); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u.Scopes, u.Disabled = fields(scopes), disabled != 0
	u.CreatedAt, u.UpdatedAt, u.passwordChangedAt = fromUnix(created), fromUnix(updated), fromUnix(pwChanged)
	return u, nil
}

func (d db) userCount(ctx context.Context) (int, error) {
	var n int
	err := d.st.QueryRowContext(ctx, `SELECT COUNT(*) FROM identity_users`).Scan(&n)
	return n, err
}

func (d db) credentialCount(ctx context.Context) (int, error) {
	var n int
	err := d.st.QueryRowContext(ctx, `SELECT COUNT(*) FROM identity_credentials`).Scan(&n)
	return n, err
}

// createFirstUser makes the owner, once: it fails with ErrSetupClosed when any
// user exists, atomically with the insert.
func (d db) createFirstUser(ctx context.Context, u User) error {
	return d.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM identity_users`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrSetupClosed
		}
		return d.insertUser(ctx, tx, u)
	})
}

func (d db) createUser(ctx context.Context, u User) error {
	return d.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error { return d.insertUser(ctx, tx, u) })
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (d db) insertUser(ctx context.Context, tx execer, u User) error {
	_, err := tx.ExecContext(ctx, d.q(`INSERT INTO identity_users (`+userCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`),
		u.ID, u.Username, u.DisplayName, u.Email, u.passwordHash, strings.Join(u.Scopes, " "), 0,
		unix(u.CreatedAt), unix(u.UpdatedAt), unix(u.passwordChangedAt))
	if err != nil && isUnique(err) {
		return ErrConflict
	}
	return err
}

func isUnique(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique") || strings.Contains(m, "duplicate")
}

func (d db) userByID(ctx context.Context, id string) (User, error) {
	return scanUser(d.st.QueryRowContext(ctx, d.q(`SELECT `+userCols+` FROM identity_users WHERE id = ?`), id))
}

func (d db) userByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(d.st.QueryRowContext(ctx, d.q(`SELECT `+userCols+` FROM identity_users WHERE username = ?`), username))
}

func (d db) listUsers(ctx context.Context) ([]User, error) {
	rows, err := d.st.QueryContext(ctx, `SELECT `+userCols+` FROM identity_users ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d db) updateUser(ctx context.Context, id string, set map[string]any) error {
	if len(set) == 0 {
		return nil
	}
	var cols []string
	var args []any
	for _, c := range []string{"display_name", "email", "scopes", "disabled", "password_hash", "password_changed_at"} {
		if v, ok := set[c]; ok {
			cols = append(cols, c+" = ?")
			args = append(args, v)
		}
	}
	cols = append(cols, "updated_at = ?")
	args = append(args, time.Now().Unix(), id)
	res, err := d.st.ExecContext(ctx, d.q(`UPDATE identity_users SET `+strings.Join(cols, ", ")+` WHERE id = ?`), args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- sessions ----

func (d db) createSession(ctx context.Context, s Session, userID, secret string) error {
	_, err := d.st.ExecContext(ctx, d.q(`INSERT INTO identity_sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at, user_agent) VALUES (?,?,?,?,?,?,?)`),
		s.ID, userID, hashToken(secret), unix(s.CreatedAt), unix(s.ExpiresAt), unix(s.LastSeenAt), s.UserAgent)
	return err
}

// sessionByToken returns the live session and its user for a session secret.
func (d db) sessionByToken(ctx context.Context, secret string, now time.Time) (Session, User, error) {
	var s Session
	var userID string
	var created, expires, seen, revoked int64
	err := d.st.QueryRowContext(ctx, d.q(`SELECT id, user_id, created_at, expires_at, last_seen_at, user_agent, revoked_at FROM identity_sessions WHERE token_hash = ?`), hashToken(secret)).
		Scan(&s.ID, &userID, &created, &expires, &seen, &s.UserAgent, &revoked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, User{}, ErrNotFound
		}
		return Session{}, User{}, err
	}
	s.CreatedAt, s.ExpiresAt, s.LastSeenAt = fromUnix(created), fromUnix(expires), fromUnix(seen)
	if revoked != 0 || !now.Before(s.ExpiresAt) {
		return Session{}, User{}, ErrNotFound
	}
	u, err := d.userByID(ctx, userID)
	if err != nil {
		return Session{}, User{}, err
	}
	if u.Disabled {
		return Session{}, User{}, ErrNotFound
	}
	return s, u, nil
}

func (d db) touchSession(ctx context.Context, id string, now time.Time) {
	_, _ = d.st.ExecContext(ctx, d.q(`UPDATE identity_sessions SET last_seen_at = ? WHERE id = ? AND last_seen_at < ?`), now.Unix(), id, now.Unix()-60)
}

func (d db) listSessions(ctx context.Context, userID string, now time.Time) ([]Session, error) {
	rows, err := d.st.QueryContext(ctx, d.q(`SELECT id, created_at, expires_at, last_seen_at, user_agent FROM identity_sessions WHERE user_id = ? AND revoked_at = 0 AND expires_at > ? ORDER BY created_at DESC`), userID, now.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Session
	for rows.Next() {
		var s Session
		var c, e, l int64
		if err := rows.Scan(&s.ID, &c, &e, &l, &s.UserAgent); err != nil {
			return nil, err
		}
		s.CreatedAt, s.ExpiresAt, s.LastSeenAt = fromUnix(c), fromUnix(e), fromUnix(l)
		out = append(out, s)
	}
	return out, rows.Err()
}

// revokeSession revokes one session of userID (any user's when userID is "").
func (d db) revokeSession(ctx context.Context, id, userID string, now time.Time) error {
	q, args := `UPDATE identity_sessions SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, []any{now.Unix(), id}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := d.st.ExecContext(ctx, d.q(q), args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// revokeSessionsExcept revokes every session of the user but keep ("" keeps none).
func (d db) revokeSessionsExcept(ctx context.Context, userID, keep string, now time.Time) error {
	_, err := d.st.ExecContext(ctx, d.q(`UPDATE identity_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at = 0 AND id <> ?`), now.Unix(), userID, keep)
	return err
}

// ---- credentials ----

func (d db) createCredential(ctx context.Context, c Credential, secret string) error {
	_, err := d.st.ExecContext(ctx, d.q(`INSERT INTO identity_credentials (id, name, token_hash, scopes, audiences, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?)`),
		c.ID, c.Name, hashToken(secret), strings.Join(c.Scopes, " "), strings.Join(c.Audiences, " "), c.CreatedBy, unix(c.CreatedAt), unix(c.ExpiresAt))
	return err
}

func scanCredential(row interface{ Scan(...any) error }) (Credential, int64, error) {
	var c Credential
	var scopes, aud string
	var created, expires, revoked, used int64
	if err := row.Scan(&c.ID, &c.Name, &scopes, &aud, &c.CreatedBy, &created, &expires, &revoked, &used); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Credential{}, 0, ErrNotFound
		}
		return Credential{}, 0, err
	}
	c.Scopes, c.Audiences = fields(scopes), fields(aud)
	c.CreatedAt, c.ExpiresAt, c.LastUsedAt = fromUnix(created), fromUnix(expires), fromUnix(used)
	return c, revoked, nil
}

const credCols = "id, name, scopes, audiences, created_by, created_at, expires_at, revoked_at, last_used_at"

func (d db) credentialByToken(ctx context.Context, secret string, now time.Time) (Credential, error) {
	c, revoked, err := scanCredential(d.st.QueryRowContext(ctx, d.q(`SELECT `+credCols+` FROM identity_credentials WHERE token_hash = ?`), hashToken(secret)))
	if err != nil {
		return Credential{}, err
	}
	if revoked != 0 || (!c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt)) {
		return Credential{}, ErrNotFound
	}
	_, _ = d.st.ExecContext(ctx, d.q(`UPDATE identity_credentials SET last_used_at = ? WHERE id = ? AND last_used_at < ?`), now.Unix(), c.ID, now.Unix()-60)
	return c, nil
}

func (d db) listCredentials(ctx context.Context) ([]Credential, error) {
	rows, err := d.st.QueryContext(ctx, `SELECT `+credCols+` FROM identity_credentials WHERE revoked_at = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Credential
	for rows.Next() {
		c, _, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d db) revokeCredential(ctx context.Context, id string, now time.Time) error {
	res, err := d.st.ExecContext(ctx, d.q(`UPDATE identity_credentials SET revoked_at = ? WHERE id = ? AND revoked_at = 0`), now.Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Nothing changed: either the key was already revoked (success, the state the
		// caller asked for) or there is no such key.
		var one int
		err := d.st.QueryRowContext(ctx, d.q(`SELECT 1 FROM identity_credentials WHERE id = ?`), id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ---- pairing ----

type pairingCode struct {
	ID        string
	Name      string
	Scopes    []string
	Audiences []string
	KeyTTL    time.Duration
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (d db) createPairingCode(ctx context.Context, p pairingCode, secret string) error {
	_, err := d.st.ExecContext(ctx, d.q(`INSERT INTO identity_pairing_codes (id, code_hash, name, scopes, audiences, key_ttl_seconds, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?,?)`),
		p.ID, hashToken(secret), p.Name, strings.Join(p.Scopes, " "), strings.Join(p.Audiences, " "), int64(p.KeyTTL/time.Second), p.CreatedBy, unix(p.CreatedAt), unix(p.ExpiresAt))
	return err
}

// redeemPairingCode spends a code once and, in the same transaction, creates
// the credential it grants.
func (d db) redeemPairingCode(ctx context.Context, secret string, now time.Time, mint func(pairingCode) (Credential, string)) (Credential, string, error) {
	var out Credential
	var key string
	err := d.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var p pairingCode
		var scopes, aud string
		var ttl, created, expires, used int64
		err := tx.QueryRowContext(ctx, d.q(`SELECT id, name, scopes, audiences, key_ttl_seconds, created_by, created_at, expires_at, used_at FROM identity_pairing_codes WHERE code_hash = ?`), hashToken(secret)).
			Scan(&p.ID, &p.Name, &scopes, &aud, &ttl, &p.CreatedBy, &created, &expires, &used)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if used != 0 || !now.Before(fromUnix(expires)) {
			return ErrNotFound
		}
		p.Scopes, p.Audiences, p.KeyTTL = fields(scopes), fields(aud), time.Duration(ttl)*time.Second
		res, err := tx.ExecContext(ctx, d.q(`UPDATE identity_pairing_codes SET used_at = ? WHERE id = ? AND used_at = 0`), now.Unix(), p.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		c, secretKey := mint(p)
		if _, err := tx.ExecContext(ctx, d.q(`INSERT INTO identity_credentials (id, name, token_hash, scopes, audiences, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?)`),
			c.ID, c.Name, hashToken(secretKey), strings.Join(c.Scopes, " "), strings.Join(c.Audiences, " "), c.CreatedBy, unix(c.CreatedAt), unix(c.ExpiresAt)); err != nil {
			return err
		}
		out, key = c, secretKey
		return nil
	})
	return out, key, err
}
