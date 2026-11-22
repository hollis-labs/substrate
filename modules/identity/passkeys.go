package identity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/hollis-labs/substrate/apierr"
)

// Passkeys (WebAuthn). On when identity.public_url is a secure context (https, or http on
// localhost): the relying party is public_url's host and the only accepted origin its own.
// People add passkeys to their account and sign in with one, without a username (discoverable
// credentials); an account may then be made passkey-only, which refuses its password. Each
// ceremony's server half is kept in identity_webauthn_challenges, used once, for 5 minutes.
//
//	POST   /api/passkey/begin                       public: options for a sign-in
//	POST   /api/passkey/finish                      public: {challenge_id, credential} -> session
//	GET    /api/identity/passkeys                   the caller's passkeys, passkey_only, enabled
//	POST   /api/identity/passkeys/register/begin    options to create one
//	POST   /api/identity/passkeys/register/finish   {challenge_id, credential, name}
//	PUT    /api/identity/passkeys/{id}              {name}
//	DELETE /api/identity/passkeys/{id}
//	PUT    /api/identity/passkey-only               {enabled}

const challengeTTL = 5 * time.Minute

// ErrPasskeysOff is a passkey route on an app whose public_url is not a secure context.
var ErrPasskeysOff = errors.New("identity: passkeys are off (set identity.public_url to an https address)")

// Passkey is one of a user's WebAuthn credentials, as listed.
type Passkey struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
}

// waUser adapts a User and its credentials to go-webauthn. The user handle is the account id.
type waUser struct {
	u     User
	creds []webauthn.Credential
}

func (w waUser) WebAuthnID() []byte                         { return []byte(w.u.ID) }
func (w waUser) WebAuthnName() string                       { return w.u.Username }
func (w waUser) WebAuthnDisplayName() string                { return w.u.DisplayName }
func (w waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

// webAuthn is the relying party for this app, or ErrPasskeysOff.
func (s *Service) webAuthn() (*webauthn.WebAuthn, error) {
	if !s.cfg.passkeys {
		return nil, ErrPasskeysOff
	}
	return webauthn.New(&webauthn.Config{RPID: s.cfg.rpID, RPDisplayName: s.app.Name, RPOrigins: []string{s.cfg.publicOrigin}})
}

// PasskeysEnabled reports whether passkeys are on for this app.
func (s *Service) PasskeysEnabled() bool { return s.cfg.passkeys }

// ---- store ----

func credID(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func (d db) passkeyCreds(ctx context.Context, userID string) ([]webauthn.Credential, error) {
	rows, err := d.st.QueryContext(ctx, d.q(`SELECT credential FROM identity_passkeys WHERE user_id = ? ORDER BY created_at`), userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []webauthn.Credential
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d db) listPasskeys(ctx context.Context, userID string) ([]Passkey, error) {
	rows, err := d.st.QueryContext(ctx, d.q(`SELECT id, name, created_at, last_used_at FROM identity_passkeys WHERE user_id = ? ORDER BY created_at`), userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Passkey{}
	for rows.Next() {
		var p Passkey
		var created, used int64
		if err := rows.Scan(&p.ID, &p.Name, &created, &used); err != nil {
			return nil, err
		}
		p.CreatedAt, p.LastUsedAt = fromUnix(created), fromUnix(used)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d db) addPasskey(ctx context.Context, userID, name string, c *webauthn.Credential, now time.Time) (Passkey, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return Passkey{}, err
	}
	p := Passkey{ID: newID("pk_"), Name: name, CreatedAt: now}
	_, err = d.st.ExecContext(ctx, d.q(`INSERT INTO identity_passkeys (id, user_id, credential_id, credential, name, created_at) VALUES (?,?,?,?,?,?)`),
		p.ID, userID, credID(c.ID), string(raw), name, now.Unix())
	if err != nil && isUnique(err) {
		return Passkey{}, ErrConflict
	}
	return p, err
}

// usePasskey stores a credential after a sign-in (its sign count and flags move on).
func (d db) usePasskey(ctx context.Context, c *webauthn.Credential, now time.Time) {
	raw, err := json.Marshal(c)
	if err != nil {
		return
	}
	_, _ = d.st.ExecContext(ctx, d.q(`UPDATE identity_passkeys SET credential = ?, last_used_at = ? WHERE credential_id = ?`), string(raw), now.Unix(), credID(c.ID))
}

func (d db) passkeyCount(ctx context.Context, userID string) int {
	var n int
	_ = d.st.QueryRowContext(ctx, d.q(`SELECT COUNT(*) FROM identity_passkeys WHERE user_id = ?`), userID).Scan(&n)
	return n
}

func (d db) passkeyOnly(ctx context.Context, userID string) bool {
	var n int
	_ = d.st.QueryRowContext(ctx, d.q(`SELECT passkey_only FROM identity_users WHERE id = ?`), userID).Scan(&n)
	return n != 0
}

func (d db) saveChallenge(ctx context.Context, userID, kind string, sd *webauthn.SessionData, now time.Time) (string, error) {
	raw, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	id := newID("wac_") + randomString(8)
	_, err = d.st.ExecContext(ctx, d.q(`INSERT INTO identity_webauthn_challenges (id, user_id, kind, session, expires_at) VALUES (?,?,?,?,?)`),
		id, userID, kind, string(raw), now.Add(challengeTTL).Unix())
	return id, err
}

// takeChallenge returns and deletes a challenge of the given kind (and user, when given):
// each one is good for a single attempt.
func (d db) takeChallenge(ctx context.Context, id, kind, userID string, now time.Time) (webauthn.SessionData, error) {
	var sd webauthn.SessionData
	var owner, raw string
	var exp int64
	err := d.st.QueryRowContext(ctx, d.q(`SELECT user_id, session, expires_at FROM identity_webauthn_challenges WHERE id = ? AND kind = ?`), id, kind).Scan(&owner, &raw, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return sd, ErrNotFound
	}
	if err != nil {
		return sd, err
	}
	_, _ = d.st.ExecContext(ctx, d.q(`DELETE FROM identity_webauthn_challenges WHERE id = ?`), id)
	if now.Unix() >= exp || owner != userID {
		return sd, ErrNotFound
	}
	return sd, json.Unmarshal([]byte(raw), &sd)
}

// ---- handlers ----

func (s *Service) passkeyRoutes(h func(http.HandlerFunc) http.Handler) map[string]http.Handler {
	return map[string]http.Handler{
		"POST /api/passkey/begin":                     h(s.passkeyLoginBegin),
		"POST /api/passkey/finish":                    h(s.passkeyLoginFinish),
		"GET /api/identity/passkeys":                  h(s.passkeyList),
		"POST /api/identity/passkeys/register/begin":  h(s.passkeyRegisterBegin),
		"POST /api/identity/passkeys/register/finish": h(s.passkeyRegisterFinish),
		"PUT /api/identity/passkeys/":                 h(s.passkeyRename),
		"DELETE /api/identity/passkeys/":              h(s.passkeyDelete),
		"PUT /api/identity/passkey-only":              h(s.passkeyOnlySet),
	}
}

func passkeysOff(w http.ResponseWriter) {
	apierr.Write(w, http.StatusNotFound, "passkeys_off", ErrPasskeysOff.Error(), nil)
}

func (s *Service) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	wa, err := s.webAuthn()
	if err != nil {
		passkeysOff(w)
		return
	}
	if !s.sameOrigin(r) {
		apierr.Write(w, http.StatusForbidden, "bad_origin", "the request did not come from this app", nil)
		return
	}
	opts, sd, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		failed(w)
		return
	}
	id, err := s.db.saveChallenge(r.Context(), "", "login", sd, s.now())
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"challenge_id": id, "options": opts})
}

func (s *Service) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	wa, err := s.webAuthn()
	if err != nil {
		passkeysOff(w)
		return
	}
	if !s.sameOrigin(r) {
		apierr.Write(w, http.StatusForbidden, "bad_origin", "the request did not come from this app", nil)
		return
	}
	var in struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := "passkey|" + clientAddr(r)
	now := s.now()
	if wait := s.limiter.wait(key, now); wait > 0 {
		tooMany(w, wait)
		return
	}
	fail := func() {
		s.limiter.fail(key, now)
		apierr.Write(w, http.StatusUnauthorized, "invalid_login", "that passkey did not sign in", nil)
	}
	sd, err := s.db.takeChallenge(r.Context(), in.ChallengeID, "login", "", now)
	if err != nil {
		fail()
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	if err != nil {
		fail()
		return
	}
	var who User
	known := false // the passkey is one this app has for that user
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		u, err := s.db.userByID(r.Context(), string(userHandle))
		if err != nil || u.Disabled {
			return nil, ErrNotFound
		}
		creds, err := s.db.passkeyCreds(r.Context(), u.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range creds {
			if bytes.Equal(c.ID, rawID) {
				known = true
			}
		}
		who = u
		return waUser{u: u, creds: creds}, nil
	}
	cred, err := wa.ValidateDiscoverableLogin(handler, sd, parsed)
	if err != nil || who.ID == "" {
		if !known {
			// A passkey saved for this site that the app does not have (its user or the passkey
			// was removed, or it was made for another install): say so, so the page can send the
			// person to their password instead of retrying.
			s.limiter.fail(key, now)
			apierr.Write(w, http.StatusUnauthorized, "passkey_unknown", "this passkey isn't registered here. Sign in with your password, then add it again from Passkeys", nil)
			return
		}
		fail()
		return
	}
	s.db.usePasskey(r.Context(), cred, now)
	s.limiter.clear(key)
	s.startSession(w, r, who, MethodPasskey)
}

func (s *Service) passkeyCaller(w http.ResponseWriter, r *http.Request) (User, bool) {
	u, _, ok := s.callerUser(r)
	if !ok {
		apierr.Write(w, http.StatusForbidden, "user_required", "this needs a signed-in user", nil)
		return User{}, false
	}
	return u, true
}

func (s *Service) passkeyList(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	list, err := s.db.listPasskeys(r.Context(), u.ID)
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": list, "passkey_only": s.db.passkeyOnly(r.Context(), u.ID), "enabled": s.cfg.passkeys})
}

func (s *Service) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	wa, err := s.webAuthn()
	if err != nil {
		passkeysOff(w)
		return
	}
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	creds, err := s.db.passkeyCreds(r.Context(), u.ID)
	if err != nil {
		failed(w)
		return
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(creds))
	for _, c := range creds {
		exclude = append(exclude, c.Descriptor())
	}
	opts, sd, err := wa.BeginRegistration(waUser{u: u, creds: creds},
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(exclude))
	if err != nil {
		failed(w)
		return
	}
	id, err := s.db.saveChallenge(r.Context(), u.ID, "register", sd, s.now())
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"challenge_id": id, "options": opts})
}

func (s *Service) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	wa, err := s.webAuthn()
	if err != nil {
		passkeysOff(w)
		return
	}
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	var in struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
		Name        string          `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	now := s.now()
	sd, err := s.db.takeChallenge(r.Context(), in.ChallengeID, "register", u.ID, now)
	if err != nil {
		bad(w, "the passkey request expired or was already used; start again")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	if err != nil {
		bad(w, "the browser's answer could not be read")
		return
	}
	creds, err := s.db.passkeyCreds(r.Context(), u.ID)
	if err != nil {
		failed(w)
		return
	}
	cred, err := wa.CreateCredential(waUser{u: u, creds: creds}, sd, parsed)
	if err != nil {
		bad(w, "the passkey could not be verified")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	p, err := s.db.addPasskey(r.Context(), u.ID, name, cred, now)
	switch {
	case errors.Is(err, ErrConflict):
		apierr.Write(w, http.StatusConflict, "exists", "that passkey is already on this account", nil)
	case err != nil:
		failed(w)
	default:
		writeJSON(w, http.StatusCreated, p)
	}
}

func (s *Service) passkeyRename(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/identity/passkeys/")
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		bad(w, "a passkey needs a name of 1 to 80 characters")
		return
	}
	res, err := s.db.st.ExecContext(r.Context(), s.db.q(`UPDATE identity_passkeys SET name = ? WHERE id = ? AND user_id = ?`), name, id, u.ID)
	if err != nil {
		failed(w)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		notFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) passkeyDelete(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/identity/passkeys/")
	if s.db.passkeyOnly(r.Context(), u.ID) && s.db.passkeyCount(r.Context(), u.ID) <= 1 {
		apierr.Write(w, http.StatusConflict, "last_passkey", "this is your last passkey and the account is passkey-only: turn passkey-only off first", nil)
		return
	}
	if role, _ := s.db.roleOf(r.Context(), s.db.st, u.ID); slices.Contains(s.cfg.requireSecondFactor, role) &&
		!s.db.totpEnabled(r.Context(), u.ID) && s.db.passkeyCount(r.Context(), u.ID) <= 1 {
		apierr.Write(w, http.StatusConflict, "second_factor_required", "your role needs a second factor: turn TOTP on before removing your last passkey", nil)
		return
	}
	res, err := s.db.st.ExecContext(r.Context(), s.db.q(`DELETE FROM identity_passkeys WHERE id = ? AND user_id = ?`), id, u.ID)
	if err != nil {
		failed(w)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		notFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) passkeyOnlySet(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled && s.db.passkeyCount(r.Context(), u.ID) == 0 {
		apierr.Write(w, http.StatusConflict, "no_passkey", "add a passkey before turning passkey-only on", nil)
		return
	}
	v := 0
	if in.Enabled {
		v = 1
	}
	if _, err := s.db.st.ExecContext(r.Context(), s.db.q(`UPDATE identity_users SET passkey_only = ? WHERE id = ?`), v, u.ID); err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"passkey_only": in.Enabled})
}
