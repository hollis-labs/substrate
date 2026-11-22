-- +goose Up
-- Invites: a single-use, expiring link an admin makes for someone to join a space with a role.
-- Only the code's hash is stored.
CREATE TABLE IF NOT EXISTS identity_invites (
  id TEXT PRIMARY KEY,
  code_hash TEXT NOT NULL UNIQUE,
  space_id TEXT NOT NULL,
  role TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  used_at BIGINT NOT NULL DEFAULT 0,
  used_by TEXT NOT NULL DEFAULT '',
  revoked_at BIGINT NOT NULL DEFAULT 0
);

-- +goose Down
-- No down migration: identity data is not dropped by a downgrade.
