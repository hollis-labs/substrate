package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrNoOwner is returned by CreateKey before the first owner exists: setup
// comes first.
var ErrNoOwner = errors.New("identity: no owner yet; run setup first")

// LocalOperator is the CreatedBy of a key made offline by whoever runs the
// binary on the host (the same id the local stdio principal uses).
const LocalOperator = "local:operator"

// OwnerInput is the first account. Password is the plain password; it is hashed
// here and never stored or returned.
type OwnerInput struct {
	Username    string
	DisplayName string
	Password    string
}

// SetupOwner creates the first owner with the scope "*", under the same rules
// as POST /api/setup: the same validation (username shape, password length and
// cost) and the same atomic check, so ErrSetupClosed comes back when any user
// exists. It ignores the identity.setup setting (that setting closes the HTTP
// route; this is the offline path) and creates no session.
func (s *Service) SetupOwner(ctx context.Context, in OwnerInput) (PublicUser, error) {
	u, err := s.newUser(credentials{Username: in.Username, DisplayName: in.DisplayName, Password: in.Password}, []string{"*"})
	if err != nil {
		return PublicUser{}, err
	}
	if err := s.db.createFirstUser(ctx, u); err != nil {
		return PublicUser{}, err
	}
	return PublicUser{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Scopes: slices.Clone(u.Scopes)}, nil
}

// KeyInput describes an API key made without an HTTP caller. Any scopes may be
// granted, including "*": the caller already holds the database.
type KeyInput struct {
	Name      string
	Scopes    []string
	Audiences []string
	TTL       time.Duration // 0 = no expiry
	CreatedBy string        // recorded on the key; default LocalOperator
}

// CreateKey makes an API key and returns the key string, which exists only in
// the return value (the database holds its hash). It needs an owner
// (ErrNoOwner otherwise).
func (s *Service) CreateKey(ctx context.Context, in KeyInput) (string, Credential, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return "", Credential{}, errors.New("a key needs a name of 1 to 120 characters")
	}
	if len(in.Scopes) == 0 {
		return "", Credential{}, errors.New("a key needs at least one scope")
	}
	for _, sc := range in.Scopes {
		if sc == "" || strings.ContainsAny(sc, " \t\r\n") {
			return "", Credential{}, fmt.Errorf("scope %q is not valid", sc)
		}
	}
	if in.TTL < 0 {
		return "", Credential{}, errors.New("ttl must not be negative")
	}
	if n, err := s.db.userCount(ctx); err != nil {
		return "", Credential{}, err
	} else if n == 0 {
		return "", Credential{}, ErrNoOwner
	}
	by := in.CreatedBy
	if by == "" {
		by = LocalOperator
	}
	now := time.Now().UTC()
	c := Credential{ID: newID("k_"), Name: name, Scopes: slices.Clone(in.Scopes), Audiences: cleanList(in.Audiences), CreatedBy: by, CreatedAt: now}
	if in.TTL > 0 {
		c.ExpiresAt = now.Add(in.TTL)
	}
	secret := randomString(32)
	if err := s.db.createCredential(ctx, c, secret); err != nil {
		return "", Credential{}, err
	}
	return keyPrefix + secret, c, nil
}

// RevokeKey revokes the API key with the given id (the Credential.ID CreateKey
// returned, or the id in GET /api/identity/keys). It is the one path: the
// DELETE /api/identity/keys/{id} handler calls it.
//
// The effect is immediate for the key itself: credentials are read from the
// database on every verification, so the next request or the next stdio start
// that presents the key is refused with no cache to wait out. Revoking an
// already revoked key is not an error (nil), so a caller that revokes when a
// child stops and again when its key is renewed need not track which came first.
// An id that never existed is ErrNotFound. Ending the key does not end what it
// already produced: an EdDSA token minted from it by POST /api/token keeps
// verifying until its own expiry (15 minutes by default, at most an hour), and a
// running mcp.ServeStdio process verified its token file once at start and keeps
// serving under that principal until it exits.
func (s *Service) RevokeKey(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrNotFound
	}
	return s.db.revokeCredential(ctx, id, time.Now())
}
