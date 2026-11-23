package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/substrate/apierr"
)

// My keys: anyone signed in can make API keys for their own tools (an agent, a script) with
// scopes they hold, list them and revoke them. Administrators also see and revoke everyone's on
// the admin routes (GET/DELETE /api/identity/keys).
//
//	GET    /api/identity/me/keys       {keys}: the caller's keys, not revoked
//	POST   /api/identity/me/keys       {name, scopes, ttl_hours} -> {key, credential} (the secret once)
//	DELETE /api/identity/me/keys/{id}  revoke one of the caller's keys
//
// They take a signed-in session, never a key or a token: a key cannot make more keys. A key a
// person made works only while they do (not disabled) and with no more than they hold now (a
// lowered role lowers the key's scopes too).

// MaxMyKeyTTL is the longest a self-service key may live; 0 asks for no expiry.
const MaxMyKeyTTL = 366 * 24 * time.Hour

// sessionUser is the signed-in person behind a session (not a key or token); false has answered.
func (s *Service) sessionUser(w http.ResponseWriter, r *http.Request) (User, string, bool) {
	u, p, ok := s.callerUser(r)
	if !ok || p.SessionID == "" {
		apierr.Write(w, http.StatusForbidden, "session_required", "sign in in the browser to manage your keys (a key cannot make keys)", nil)
		return User{}, "", false
	}
	return u, p.ID, true
}

func (s *Service) myKeys(ctx context.Context, principal string) ([]Credential, error) {
	all, err := s.ListKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := []Credential{}
	for _, c := range all {
		if c.CreatedBy == principal {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Service) myKeysRoutes(h func(http.HandlerFunc) http.Handler) map[string]http.Handler {
	return map[string]http.Handler{
		"GET /api/identity/me/keys":     h(s.myKeysList),
		"POST /api/identity/me/keys":    h(s.myKeysCreate),
		"DELETE /api/identity/me/keys/": h(s.myKeysRevoke),
	}
}

func (s *Service) myKeysList(w http.ResponseWriter, r *http.Request) {
	_, principal, ok := s.sessionUser(w, r)
	if !ok {
		return
	}
	keys, err := s.myKeys(r.Context(), principal)
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Service) myKeysCreate(w http.ResponseWriter, r *http.Request) {
	_, principal, ok := s.sessionUser(w, r)
	if !ok {
		return
	}
	var in keyRequest
	if !decode(w, r, &in) {
		return
	}
	p, _ := caller(r)
	name := strings.TrimSpace(in.Name)
	scopes, held := grantable(p.Scopes, in.Scopes)
	if name == "" || len(name) > 120 || len(in.Scopes) == 0 || !held {
		bad(w, "give a name, and scopes you hold")
		return
	}
	ttl := time.Duration(in.TTLHours) * time.Hour
	if in.TTLHours < 0 || ttl > MaxMyKeyTTL {
		bad(w, "a key lasts at most 366 days (ttl_hours 0 for no expiry)")
		return
	}
	now := time.Now().UTC()
	c := Credential{ID: newID("k_"), Name: name, Scopes: scopes, Audiences: cleanList(in.Audiences), CreatedBy: principal, CreatedAt: now}
	if ttl > 0 {
		c.ExpiresAt = now.Add(ttl)
	}
	secret := randomString(32)
	if err := s.db.createCredential(r.Context(), c, secret); err != nil {
		failed(w)
		return
	}
	s.emitKey(KeyCreated, c, principal)
	writeJSON(w, http.StatusCreated, map[string]any{"key": keyPrefix + secret, "credential": c})
}

func (s *Service) myKeysRevoke(w http.ResponseWriter, r *http.Request) {
	_, principal, ok := s.sessionUser(w, r)
	if !ok {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/identity/me/keys/")
	mine, err := s.myKeys(r.Context(), principal)
	if err != nil {
		failed(w)
		return
	}
	found := false
	for _, c := range mine {
		found = found || c.ID == id
	}
	if !found {
		notFound(w)
		return
	}
	switch err := s.RevokeKey(r.Context(), id); {
	case errors.Is(err, ErrNotFound):
		notFound(w)
	case err != nil:
		failed(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// boundByCreator limits a key a person made to what that person holds now: none when they are
// gone or disabled. Keys made by a module or the CLI are left as they are.
func (s *Service) boundByCreator(ctx context.Context, c Credential) ([]string, bool) {
	id, ok := strings.CutPrefix(c.CreatedBy, "user:")
	if !ok {
		return c.Scopes, true
	}
	u, err := s.db.userByID(ctx, id)
	if err != nil || u.Disabled {
		return nil, false
	}
	role, _ := s.db.roleOf(ctx, s.db.st, u.ID)
	held := effectiveScopes(s.app.Name, u.Scopes, role)
	out := []string{}
	for _, sc := range c.Scopes {
		if covers(held, sc) {
			out = append(out, sc)
		}
	}
	return out, true
}
