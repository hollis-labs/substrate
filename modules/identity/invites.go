package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/substrate/apierr"
)

// Invites: how more people join. An administrator makes an invite for a role (and a label, to
// remember who it is for); the person opens the link (<app>/invite#c=<code>), chooses a username
// and password, and is signed in as a member of the space with that role. An invite works once
// and expires (default 7 days, at most 30); only its code's hash is kept. Nobody can invite to
// a role whose scopes they do not hold.
//
//	POST   /api/identity/invites      admin: {role, label?, ttl_hours?} -> {id, code, role, expires_at}
//	GET    /api/identity/invites      admin: the pending invites (not used, revoked or expired)
//	DELETE /api/identity/invites/{id} admin: revoke
//	POST   /api/invite/check          public: {code} -> {role, label, app}, or 404
//	POST   /api/invite/accept         public: {code, username, display_name, password} -> session

// DefaultInviteTTL is how long an invite stays valid unless told otherwise.
const DefaultInviteTTL = 7 * 24 * time.Hour

const (
	maxInviteTTL = 30 * 24 * time.Hour
	invitePrefix = "invite_"
)

// ErrInvite is an invite code that is unknown, used, revoked or expired.
var ErrInvite = errors.New("identity: the invite is not valid (used, revoked, expired or wrong)")

// Invite is a pending invite, as listed (never its code).
type Invite struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Label     string    `json:"label,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// InviteInput is an invite to make. CreatedBy is the inviter's principal.
type InviteInput struct {
	Role      string
	Label     string
	TTL       time.Duration // 0: DefaultInviteTTL
	CreatedBy string
}

// CreateInvite makes an invite and returns its code once (only the hash is stored). The
// caller has checked that the inviter may grant the role.
func (s *Service) CreateInvite(ctx context.Context, in InviteInput) (Invite, string, error) {
	if !ValidRole(in.Role) {
		return Invite{}, "", ErrBadRole
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = DefaultInviteTTL
	}
	if ttl > maxInviteTTL {
		return Invite{}, "", errors.New("an invite lasts at most 30 days")
	}
	label := strings.TrimSpace(in.Label)
	if len(label) > 120 {
		return Invite{}, "", errors.New("the label is too long")
	}
	now := s.now()
	inv := Invite{ID: newID("inv_"), Role: in.Role, Label: label, CreatedBy: in.CreatedBy, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	secret := randomString(24)
	_, err := s.db.st.ExecContext(ctx, s.db.q(`INSERT INTO identity_invites (id, code_hash, space_id, role, label, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?)`),
		inv.ID, hashToken(secret), DefaultSpace, inv.Role, inv.Label, inv.CreatedBy, now.Unix(), inv.ExpiresAt.Unix())
	if err != nil {
		return Invite{}, "", err
	}
	return inv, invitePrefix + secret, nil
}

// PendingInvites lists invites not used, revoked or expired, newest first.
func (s *Service) PendingInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.db.st.QueryContext(ctx, s.db.q(`SELECT id, role, label, created_by, created_at, expires_at FROM identity_invites
		WHERE used_at = 0 AND revoked_at = 0 AND expires_at > ? ORDER BY created_at DESC`), s.now().Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Invite{}
	for rows.Next() {
		var in Invite
		var created, exp int64
		if err := rows.Scan(&in.ID, &in.Role, &in.Label, &in.CreatedBy, &created, &exp); err != nil {
			return nil, err
		}
		in.CreatedAt, in.ExpiresAt = fromUnix(created), fromUnix(exp)
		out = append(out, in)
	}
	return out, rows.Err()
}

// RevokeInvite ends an invite (idempotent); ErrNotFound for an unknown id.
func (s *Service) RevokeInvite(ctx context.Context, id string) error {
	var n int
	if err := s.db.st.QueryRowContext(ctx, s.db.q(`SELECT COUNT(*) FROM identity_invites WHERE id = ?`), id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	_, err := s.db.st.ExecContext(ctx, s.db.q(`UPDATE identity_invites SET revoked_at = ? WHERE id = ? AND revoked_at = 0`), s.now().Unix(), id)
	return err
}

// inviteByCode is the live invite a code names.
func (s *Service) inviteByCode(ctx context.Context, code string) (Invite, error) {
	secret, ok := strings.CutPrefix(strings.TrimSpace(code), invitePrefix)
	if !ok || secret == "" {
		return Invite{}, ErrInvite
	}
	var in Invite
	var created, exp, used, revoked int64
	err := s.db.st.QueryRowContext(ctx, s.db.q(`SELECT id, role, label, created_at, expires_at, used_at, revoked_at FROM identity_invites WHERE code_hash = ?`), hashToken(secret)).
		Scan(&in.ID, &in.Role, &in.Label, &created, &exp, &used, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrInvite
	}
	if err != nil {
		return Invite{}, err
	}
	in.CreatedAt, in.ExpiresAt = fromUnix(created), fromUnix(exp)
	if used != 0 || revoked != 0 || !s.now().Before(in.ExpiresAt) {
		return Invite{}, ErrInvite
	}
	return in, nil
}

// acceptInvite spends the invite and creates the user with its role, atomically.
func (d db) acceptInvite(ctx context.Context, inviteID string, u User, role string, now time.Time) error {
	return d.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		res, err := tx.ExecContext(ctx, d.q(`UPDATE identity_invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at = 0 AND revoked_at = 0 AND expires_at > ?`),
			now.Unix(), u.ID, inviteID, now.Unix())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrInvite
		}
		if err := d.insertUser(ctx, tx, u); err != nil {
			return err
		}
		return d.setRole(ctx, tx, u.ID, role, now)
	})
}

// ---- handlers ----

func (s *Service) inviteRoutes(h func(http.HandlerFunc) http.Handler) map[string]http.Handler {
	return map[string]http.Handler{
		"POST /api/identity/invites":    h(s.inviteCreate),
		"GET /api/identity/invites":     h(s.inviteList),
		"DELETE /api/identity/invites/": h(s.inviteRevoke),
		"POST /api/invite/check":        h(s.inviteCheck),
		"POST /api/invite/accept":       h(s.inviteAccept),
	}
}

func (s *Service) inviteCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role     string `json:"role"`
		Label    string `json:"label"`
		TTLHours int    `json:"ttl_hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role == "" {
		in.Role = RoleMember
	}
	p, _ := caller(r)
	if !s.mayGrantRole(p, in.Role) {
		bad(w, "invite to a role whose scopes you hold (owner, admin, member or guest)")
		return
	}
	inv, code, err := s.CreateInvite(r.Context(), InviteInput{Role: in.Role, Label: in.Label, TTL: time.Duration(in.TTLHours) * time.Hour, CreatedBy: p.ID})
	if err != nil {
		bad(w, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": inv.ID, "code": code, "role": inv.Role, "label": inv.Label, "expires_at": inv.ExpiresAt})
}

func (s *Service) inviteList(w http.ResponseWriter, r *http.Request) {
	list, err := s.PendingInvites(r.Context())
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": list})
}

func (s *Service) inviteRevoke(w http.ResponseWriter, r *http.Request) {
	switch err := s.RevokeInvite(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/identity/invites/")); {
	case errors.Is(err, ErrNotFound):
		notFound(w)
	case err != nil:
		failed(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// inviteCheck tells the page what an invite is for, so it can say "join as a member".
func (s *Service) inviteCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := "invite|" + clientAddr(r)
	now := s.now()
	if wait := s.limiter.wait(key, now); wait > 0 {
		tooMany(w, wait)
		return
	}
	inv, err := s.inviteByCode(r.Context(), in.Code)
	if err != nil {
		s.limiter.fail(key, now)
		apierr.Write(w, http.StatusNotFound, "invite_invalid", "this invite is not valid: it was used, revoked or has expired. Ask for a new one.", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": inv.Role, "label": inv.Label, "app": s.app.Name, "expires_at": inv.ExpiresAt})
}

func (s *Service) inviteAccept(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		apierr.Write(w, http.StatusForbidden, "bad_origin", "the request did not come from this app", nil)
		return
	}
	if r.Header.Get("X-Requested-With") == "" {
		apierr.Write(w, http.StatusForbidden, "csrf", "missing X-Requested-With", nil)
		return
	}
	var in struct {
		credentials
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := "invite|" + clientAddr(r)
	now := s.now()
	if wait := s.limiter.wait(key, now); wait > 0 {
		tooMany(w, wait)
		return
	}
	inv, err := s.inviteByCode(r.Context(), in.Code)
	if err != nil {
		s.limiter.fail(key, now)
		apierr.Write(w, http.StatusForbidden, "invite_invalid", "this invite is not valid: it was used, revoked or has expired. Ask for a new one.", nil)
		return
	}
	u, err := s.newUser(in.credentials, []string{})
	if err != nil {
		bad(w, err.Error())
		return
	}
	switch err := s.db.acceptInvite(r.Context(), inv.ID, u, inv.Role, now); {
	case errors.Is(err, ErrInvite):
		apierr.Write(w, http.StatusForbidden, "invite_invalid", "this invite was just used", nil)
		return
	case errors.Is(err, ErrConflict):
		apierr.Write(w, http.StatusConflict, "exists", "that username is taken", nil)
		return
	case err != nil:
		failed(w)
		return
	}
	s.limiter.clear(key)
	s.startSession(w, r, u, MethodPassword)
}
