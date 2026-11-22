-- +goose Up
-- Recovery links: a single-use, short-lived link, minted offline ("<app> identity recover"), that
-- signs one user in and lets them set a new password and clear their passkeys and TOTP. Only the
-- token's hash is stored.
CREATE TABLE IF NOT EXISTS identity_recovery_links (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  user_id TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  used_at BIGINT NOT NULL DEFAULT 0
);

-- +goose Down
-- No down migration: identity data is not dropped by a downgrade.
