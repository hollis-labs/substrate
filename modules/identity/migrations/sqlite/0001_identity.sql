-- +goose Up
-- The identity tables as they were created before numbered migrations (CREATE TABLE IF NOT
-- EXISTS, so a database that already has them is unchanged). Times are Unix seconds.
CREATE TABLE IF NOT EXISTS identity_users (
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
  );

CREATE TABLE IF NOT EXISTS identity_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  last_seen_at BIGINT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  revoked_at BIGINT NOT NULL DEFAULT 0
  );

CREATE TABLE IF NOT EXISTS identity_credentials (
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
  );

CREATE TABLE IF NOT EXISTS identity_pairing_codes (
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
  );

-- +goose Down
-- No down migration: identity data is not dropped by a downgrade.
