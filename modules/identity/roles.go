package identity

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/hollis-labs/libs/core/store"
)

// Spaces and roles. A space is the unit people belong to (later an org, a team or a
// workspace); there is one, DefaultSpace, until an app needs more. Every user has a role
// in it, and the role grants scopes on top of the user's own stored scopes, so the guard
// keeps checking scopes and nothing downstream needs to know about roles:
//
//	owner   "*"                               (the first user; everything)
//	admin   "<app>.admin" "<app>.use" "<app>.read"
//	member  "<app>.use" "<app>.read"
//	guest   "<app>.read"
//
// An app decides what "<app>.use" and "<app>.read" allow on its own routes and tools.

// DefaultSpace is the one space until spaces are made configurable.
const DefaultSpace = "default"

// Roles, strongest first.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleGuest  = "guest"
)

// Roles lists the roles, strongest first.
var Roles = []string{RoleOwner, RoleAdmin, RoleMember, RoleGuest}

// ErrBadRole is a role that is not one of Roles.
var ErrBadRole = errors.New("identity: role must be owner, admin, member or guest")

// ValidRole reports whether r is one of Roles.
func ValidRole(r string) bool { return slices.Contains(Roles, r) }

// RoleScopes are the scopes a role grants in the app named app.
func RoleScopes(app, role string) []string {
	switch role {
	case RoleOwner:
		return []string{"*"}
	case RoleAdmin:
		return []string{app + ".admin", app + ".use", app + ".read"}
	case RoleMember:
		return []string{app + ".use", app + ".read"}
	case RoleGuest:
		return []string{app + ".read"}
	}
	return nil
}

// effectiveScopes is what a user may do: their own scopes and their role's.
func effectiveScopes(app string, own []string, role string) []string {
	out := slices.Clone(own)
	for _, s := range RoleScopes(app, role) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// ---- store ----

// roleOf returns a user's role in the default space ("" when they have none, e.g. an
// account created before memberships and not migrated, which then has its own scopes only).
func (d db) roleOf(ctx context.Context, q store.Querier, userID string) (string, error) {
	var role string
	err := q.QueryRowContext(ctx, d.q(`SELECT role FROM identity_memberships WHERE space_id = ? AND user_id = ?`), DefaultSpace, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (d db) setRole(ctx context.Context, tx execer, userID, role string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, d.q(`DELETE FROM identity_memberships WHERE space_id = ? AND user_id = ?`), DefaultSpace, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, d.q(`INSERT INTO identity_memberships (space_id, user_id, role, created_at) VALUES (?,?,?,?)`),
		DefaultSpace, userID, role, now.Unix())
	return err
}

// roles returns every user's role in the default space, by user id.
func (d db) roles(ctx context.Context) (map[string]string, error) {
	rows, err := d.st.QueryContext(ctx, d.q(`SELECT user_id, role FROM identity_memberships WHERE space_id = ?`), DefaultSpace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		out[id] = role
	}
	return out, rows.Err()
}

// ---- service ----

// Role returns a user's role in the default space ("" when they have none).
func (s *Service) Role(ctx context.Context, userID string) (string, error) {
	if _, err := s.db.userByID(ctx, userID); err != nil {
		return "", err
	}
	return s.db.roleOf(ctx, s.db.st, userID)
}

// SetRole gives a user a role in the default space. It refuses to take the owner role
// from the last owner, so an instance always keeps one.
func (s *Service) SetRole(ctx context.Context, userID, role string) error {
	if !ValidRole(role) {
		return ErrBadRole
	}
	return s.db.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		cur, err := s.db.roleOf(ctx, tx, userID)
		if err != nil {
			return err
		}
		if cur == RoleOwner && role != RoleOwner {
			var owners int
			if err := tx.QueryRowContext(ctx, s.db.q(`SELECT COUNT(*) FROM identity_memberships WHERE space_id = ? AND role = ?`), DefaultSpace, RoleOwner).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
		return s.db.setRole(ctx, tx, userID, role, s.now())
	})
}

// ErrLastOwner refuses to leave an instance without an owner.
var ErrLastOwner = errors.New("identity: the last owner cannot be given another role")
