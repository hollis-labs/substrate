-- +goose Up
-- TOTP (authenticator-app codes, RFC 6238) as a second step after the password, and the
-- single-use recovery codes that stand in for the app when it is lost (only their hashes).
-- A row with enabled_at = 0 is an enrollment not yet confirmed with a code.
CREATE TABLE IF NOT EXISTS identity_totp (
  user_id TEXT PRIMARY KEY,
  secret TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  enabled_at BIGINT NOT NULL DEFAULT 0,
  last_step BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS identity_recovery_codes (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  code_hash TEXT NOT NULL UNIQUE,
  created_at BIGINT NOT NULL,
  used_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS identity_recovery_codes_user ON identity_recovery_codes (user_id);

-- +goose Down
-- No down migration: identity data is not dropped by a downgrade.
