# Identity: accounts, keys and app-to-app trust

Every app needs people who can set it up and sign in, and other apps and tools that can call it. Substrate's `auth` module only *verifies* credentials. The `identity` module is the other half: an app that selects it is its own identity provider, and stands alone. Nothing here depends on another app, and an app can still trust someone else's issuer instead (or as well).

## Choose how an app trusts

Per app, in the manifest config. These combine; the guard accepts a credential any of them can verify.

| You want | Set |
|---|---|
| The app issues its own identity (people sign in here) | select the `identity` module and add an `identity:` block |
| The app trusts another app's issuer (Tether, another Substrate app) | `auth.jwks_url` (and `auth.issuer`, `auth.audience`) |
| Fixed service keys from config | `auth.api_keys` |
| Nothing, for local development | `auth.mode: off` (the default); a request with no credential is the unverified operator |

`auth.mode` is the dial for all of them: `off`, `permissive` (log what `enforce` would refuse) or `enforce`.

### Where off and permissive may listen

"Off refuses a public address" does not mean "off is loopback only". `httphost.Serve` (and `auth.ValidateBind`) pass the listen address to libs `security/auth` `ValidateBind`, which in `off` and `permissive` accepts:

- loopback: `127.0.0.0/8`, `::1`, and the name `localhost`;
- the tailnet: the Tailscale address ranges (the CGNAT IPv4 block and the Tailscale IPv6 prefix), and any host name ending in `.ts.net`;
- a unix socket (`unix:/path` or a path starting with `/`).

It refuses a wildcard (`:8080`, `0.0.0.0`, `[::]`), any other IP and any other host name. `enforce` accepts every address.

This is intended, not a leak in the check. The libs doc comment states the rule ("loopback or tailnet-bound"), the guard's reload rule treats tailnet as a non-loopback listener on purpose ("tailnet included"), and the dev posture is that the Tailscale network is the security boundary until identity and policy are enforced. What it does not do: a `.ts.net` name is matched by suffix, not resolved, so the check trusts the name and not where it points; and a tailnet listener in `off` serves every peer on the tailnet as the unverified operator. An app that wants loopback only needs its own check on the address (several apps carry one); an app that serves beyond a trusted network uses `enforce`.

```yaml
modules:
  add: [database, auth, identity]
config:
  database: { sqlite: { path: /var/lib/app/app.db } }
  auth:     { mode: enforce }
  identity: { key_file: /var/lib/app/identity-signing.key }   # an empty block is enough
```

With `identity` selected, `enforce` needs no other verifier.

## People

1. First run: `GET /api/setup` says `{"setup_required": true}`. `POST /api/setup` with `{display_name, username, password}` creates the owner (scope `*`), closes setup for good, and signs in.
2. `POST /api/login`, `POST /api/logout`, `PUT /api/password` (ends every other session), `GET /api/identity/profile`, `PUT /api/identity/profile`, `GET /api/identity/sessions`, `DELETE /api/identity/sessions/{id}`.
3. Administrators (`<app>.admin`, which the owner holds through `*`) manage users: `GET|POST /api/identity/users`, `PUT /api/identity/users/{id}` (disable, scopes, password). Nobody can grant a scope they do not hold.

`GET /api/identity/profile` (scope `authenticated`) answers who the caller is: `{id, username, display_name, email, scopes, assurance}`, the same shape sign-in and `PUT /api/identity/profile` return plus `assurance`. `assurance` is `"local"` for every caller today (the module holds the credentials itself); `"verified"` is reserved for an external verifier and is never emitted. Answers: 200 for a signed-in person; 401 `unauthorized` with no or a bad credential (the guard's signed-out answer); 403 `user_required` when the credential is not a person, which is an API key, a token minted from a key, or a disabled or removed account. A machine caller does not get a principal back from this route: it already knows its own key and scopes, and `user_required` is what every other person-only route answers. A token minted from a person's session still names that person and gets the profile. `scopes` is the stored list (a copy); a UI shows `scopes` as the caller's capabilities.

A browser keeps a `HttpOnly`, `SameSite=Strict` cookie. Wrap the app's final handler in `identity.From(app).Middleware(handler)`: it turns the cookie into the bearer the guard verifies, and requires an `X-Requested-With` header and a same-origin `Origin` on cookie-authenticated writes. Five wrong passwords for a username and address lock further attempts for five minutes.

Passwords are bcrypt, 12 to 72 bytes. Passkeys, signed approvals and other strict-mode proofs are not part of this module; they would be a later opt-in layer on the same users and sessions.

## Reading users from the app

An app that needs to show who a user is (an owner column, an assignee list) asks the module, not the table:

```go
svc := identity.From(app)
u, err := svc.User(ctx, id) // identity.PublicUser: ID, Username, DisplayName, Email, Scopes, Disabled
```

An unknown id returns `identity.ErrNotFound`. A disabled account is returned with `Disabled` set; the app decides what that means for it. The view never carries the password hash, sessions or keys, and `Scopes` is a copy. The `identity_*` tables are the module's storage, not an interface: do not read them directly.

## Machines

A tool or another app holds an **API key** or a **short-lived token**.

- An administrator creates a key: `POST /api/identity/keys` `{name, scopes, audiences?, ttl_hours?}`. The key (`key_…`) is shown once; only its hash is stored. `GET /api/identity/keys` lists, `DELETE /api/identity/keys/{id}` revokes. A key authenticates directly as a bearer.
- `POST /api/token` trades any credential the caller holds (a session, a key, a token) for an EdDSA JWT: `{scopes?, audience?, ttl_seconds?}` returns `{token, expires_at, ttl_seconds, scopes, audience}`. Scopes can only narrow. A token for another **audience** is allowed only if the key lists that audience (or the caller is an administrator). Tokens last 15 minutes by default and at most an hour.
- The reply has the shape Tether's mint endpoint has, so a client app configures it as its own token source: `auth.mint_url: https://issuer/api/token`, `auth.mint_credential_file: <file holding the key>`, and `auth.mint_body: {audience: "the-callee"}`. The auth module refreshes the token and attaches it to outbound calls (`auth.HTTPClient`).

## App A calls app B

1. B's administrator creates a **pairing code** for A: `POST /api/identity/pairing-codes` `{name: "app-a", scopes: ["b.read"], audiences: ["b"], ttl_hours: 720, ttl_minutes: 10}`. The code (`pair_…`) is single-use and expires.
2. A redeems it, with no credentials: `POST https://b/api/pair` `{code}` returns `{key, credential, token_url, jwks_url}`. A stores `key` in a file (mode 0600).
3. A configures `auth.mint_url: https://b/api/token`, `auth.mint_credential_file: <that file>`, `auth.mint_body: {audience: "b"}`. Every call A makes with `auth.HTTPClient` carries a fresh token.
4. B verifies the token with its own guard (it signed it). To let a *third* app C accept tokens B issues, point C's `auth.jwks_url` at `https://b/api/identity/jwks`, with `auth.audience` set to C's name, and mint with `audience: "c"`.

Revoking the key at B ends A's access within the token lifetime.

## Keys and storage

The signing key (Ed25519, PEM) is created `0600` at `identity.key_file` on first start, beside a public `<key_file>.jwks.json`. Losing it invalidates minted tokens, not sessions or keys. State lives in `identity_*` tables created at start in the app's database (SQLite or Postgres). See the next section to rotate the key without breaking live tokens.

### One issuer per database

A process creates the signing key only when the database has no identity rows (no users, no API keys): the fresh-instance path. If the database holds identity rows and `key_file` does not exist where this process's config points, start fails with an error naming the `key_file` path, because creating a key would make a second issuer that verifies only its own tokens (a child launched with an explicit database path and a default `key_file` beside it did exactly this). Fix the config so `key_file` points at the service's key, or the process at the right database. When the key is truly lost, or a new issuer is intended, set `identity.allow_new_signing_key: true` for that start; it is the deliberate way to create a key beside existing rows. An existing key file is always used as before. Lazy key creation (no key written by a process that never mints a token) is not built: the JWT verifier is built from the key at start, so it is not a small change.

## Rotating the signing key

An administrator (`<app>.admin`, which the owner holds through `*`) rotates the key on the running app:

```sh
curl -sX POST https://app/api/identity/signing-key/rotate -H "Authorization: Bearer $ADMIN_KEY" -d '{}'
# {"kid":"<new>","retired_kid":"<old>","not_after":"2026-11-12T09:10:00Z"}
curl -s https://app/api/identity/signing-key -H "Authorization: Bearer $ADMIN_KEY"
# {"active":{"kid":"...","created_at":"..."},"retired":[{"kid":"...","retired_at":"...","not_after":"..."}]}
```

- **Overlap.** The old key stays published and keeps verifying until `not_after` (`retired_key_ttl`, default and minimum 70 minutes: the longest token life plus ten minutes of skew). No token dies before its own `exp`. The new key signs from the moment the call returns. Apps that trust this one through `auth.jwks_url` pick the new key up on their next fetch (every 5 minutes, or on the first token with an unknown `kid`); they keep the old key until it leaves the set. Rotate again inside the overlap and both retired keys stay, each with its own `not_after`.
- **Compromised key.** `{"compromised": true}` removes the old key from the set at once: its outstanding tokens stop verifying here immediately and in other apps after their next fetch (up to about 5 minutes). Do this only when you must; live tokens are cut.
- **Files** beside `key_file`: `<key_file>.retired.json` (public keys with `retired_at` and `not_after`, 0600) and the derived `<key_file>.jwks.json`. The new key is renamed over `key_file` as the commit point, after the retired record is on disk; a crash on either side loses no token. At start a retired entry equal to the active key (a crash before the commit) is dropped and the JWKS is rebuilt. A corrupt `.retired.json` refuses to start: delete it only to accept that retired keys stop verifying.
- **One rotation at a time.** A second call while one runs gets `409 rotation_in_progress`.
- **Limits.** One process per `key_file`; several instances sharing the directory would need a reload signal and are not supported. The emergency path that needs no running app: delete `key_file` and restart with `identity.allow_new_signing_key: true` for that one start (the guard below otherwise refuses, because the database holds identity rows), which invalidates every minted token; remove the setting afterwards. Every rotation is logged at warn level with the caller.

Go: `identity.From(app).RotateSigningKey(compromised)` and `.SigningKey()` do the same in process. Boot hooks must not register modules.

## Config

See the package comment for the full list: `key_file`, `allow_new_signing_key` (default false), `issuer`, `audience`, `session_ttl` (720h), `token_ttl` (15m), `min_password` (12), `bcrypt_cost` (12), `secure_cookies` (`auto`), `trusted_proxy`, `allowed_origins`, `retired_key_ttl` (`70m`, also the minimum; a lower value refuses to start).
