-- +goose Up
-- Spaces and memberships: who belongs to what, with which role. There is one space
-- ("default") until an app needs more; a role maps to scopes in code (roles.go). Existing
-- users join the default space: "*" makes the owner, a "<app>.admin" scope an admin, anyone
-- else a member. Setup tokens are the single-use links "<app> identity setup-link" prints.
CREATE TABLE IF NOT EXISTS identity_spaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS identity_memberships (
  space_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  role TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (space_id, user_id)
);

CREATE TABLE IF NOT EXISTS identity_setup_tokens (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  used_at BIGINT NOT NULL DEFAULT 0
);

INSERT INTO identity_spaces (id, name, created_at) VALUES ('default', 'Default', 0);

INSERT INTO identity_memberships (space_id, user_id, role, created_at)
SELECT 'default', id,
  CASE
    WHEN (' ' || scopes || ' ') LIKE '% * %' THEN 'owner'
    WHEN (' ' || scopes || ' ') LIKE '%.admin %' THEN 'admin'
    ELSE 'member'
  END,
  created_at
FROM identity_users;

-- +goose Down
-- No down migration: identity data is not dropped by a downgrade.
