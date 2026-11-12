package identity

import (
	"context"
	"slices"
)

// PublicUser is the view of an account an app may read: who it is and what it
// may do. It carries no password hash, session, key or timestamp of a secret.
type PublicUser struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Email       string   `json:"email,omitempty"`
	Scopes      []string `json:"scopes"`
	Disabled    bool     `json:"disabled"`
}

// User returns the account with the given id. A disabled account is returned
// with Disabled set (the caller decides what that means); an unknown id gives
// ErrNotFound. The returned Scopes slice is a copy.
func (s *Service) User(ctx context.Context, id string) (PublicUser, error) {
	u, err := s.db.userByID(ctx, id)
	if err != nil {
		return PublicUser{}, err
	}
	return PublicUser{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email,
		Scopes: slices.Clone(u.Scopes), Disabled: u.Disabled,
	}, nil
}
