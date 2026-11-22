-- +goose Up
-- Passkeys (WebAuthn credentials, stored as go-webauthn's Credential JSON), the server half of
-- each ceremony (single use, short-lived), a per-user passkey-only switch, and how each session
-- was signed in (password, passkey, setup).
CREATE TABLE IF NOT EXISTS identity_passkeys (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  credential_id TEXT NOT NULL UNIQUE,
  credential TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  last_used_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS identity_passkeys_user ON identity_passkeys (user_id);

CREATE TABLE IF NOT EXISTS identity_webauthn_challenges (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  session TEXT NOT NULL,
  expires_at BIGINT NOT NULL
);

ALTER TABLE identity_users ADD COLUMN passkey_only INTEGER NOT NULL DEFAULT 0;

ALTER TABLE identity_sessions ADD COLUMN method TEXT NOT NULL DEFAULT 'password';

-- +goose Down
-- No down migration: identity data is not dropped by a downgrade.
