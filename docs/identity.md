# Identity: accounts, keys and app-to-app trust

Every app needs people who can set it up and sign in, and other apps and tools that can call it. Substrate's `auth` module only *verifies* credentials. The `identity` module is the other half: an app that selects it is its own identity provider, and stands alone. Nothing here depends on another app, and an app can still trust someone else's issuer instead (or as well).

## Choose how an app trusts

Per app, in the manifest config. These combine; the guard accepts a credential any of them can verify.

| You want | Set |
|---|---|
| The app issues its own identity (people sign in here) | select the `identity` module and add an `identity:` block |
| The app trusts another app's issuer (Tether, another Substrate app) | `auth.jwks_url` (and `auth.issuer`, `auth.audience`) |
| The app is its own JWT issuer with a rotating key ring and an in-process deny-list (Tether) | `auth.UseKeySource` and `auth.UseDenyList` in Go, before `boot.Start` (see below) |
| Fixed service keys from config | `auth.api_keys` |
| Nothing, for local development | `auth.mode: off` (the default); a request with no credential is the unverified operator |

`auth.mode` is the dial for all of them: `off`, `permissive` (log what `enforce` would refuse) or `enforce`.

### An issuer that verifies its own tokens

An app that signs its own JWTs (agent tokens from a rotating Ed25519 key ring) cannot point `auth.jwks_url` at itself: its listener is not up when the auth module starts, and `bootstrap_key_file` cannot follow a rotation. It hands the keys and the deny-list to the auth module in process instead, before `boot.Start`:

```go
auth.UseKeySource(func(ctx context.Context) ([]byte, error) { return ring.JWKS(), nil }) // every key a live token may use
auth.UseDenyList(deny)                                                                  // *libauth.DenyList; the app calls deny.Replace
// after rotating or retiring a key:
_ = auth.RefreshKeys(ctx, app)
```

Verification is libs `security/auth`'s JWT verifier, unchanged: EdDSA, `exp`/`nbf` with leeway, `auth.issuer`, `auth.audience` (default the app name), the deny-list (jti, sub, sid). Keys are read on first use, every 30 seconds, on a token with an unknown `kid` (at most once a second), and on `RefreshKeys`. Call `RefreshKeys` after retiring a key, or tokens it signed keep verifying until the next periodic read. A source error makes a token verification `auth_unavailable` (503), never an accept. The source is a second trust root next to the identity module's: a token either one verifies is accepted (`auth.AddVerifier` chains them). Start refuses `UseKeySource` with `auth.jwks_url`, `UseDenyList` with `auth.denylist_url`, and `UseDenyList` without a JWT verifier.

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

1. First run: the owner is created by someone who can run the binary on the host, either offline (`<app> identity setup`, see [Offline bootstrap](#offline-bootstrap)) or in the browser through a **setup link** (see [Setup links](#setup-links)). `GET /api/setup` says `{"setup_required": true, "setup_over_http": false, "setup_link": true}` until then and `{"setup_required": false, ...}` after. `POST /api/setup` without a setup token is refused (403 `setup_offline`) unless `identity.setup` is `http`, a local-development opt-in under which `{display_name, username, password}` creates the owner (role `owner`), closes setup for good, and signs in.
2. `POST /api/login`, `POST /api/logout`, `PUT /api/password` (ends every other session), `GET /api/identity/profile`, `PUT /api/identity/profile`, `GET /api/identity/sessions`, `DELETE /api/identity/sessions/{id}`.
3. Administrators (`<app>.admin`, which the owner holds through `*`) manage users: `GET|POST /api/identity/users`, `PUT /api/identity/users/{id}` (disable, role, scopes, password). Nobody can grant a scope, or a role whose scopes, they do not hold. An empty `scopes` list clears the user's own scopes.

### Roles and spaces

People belong to a **space** with a **role**. There is one space, `default`, until an app needs more (an org, a team or a workspace later is another space, not a new model). The role grants scopes on top of the user's own stored scopes, and the guard checks the union, so routes and tools keep declaring scopes and need not know about roles:

| role | scopes it grants |
|---|---|
| `owner` | `*` |
| `admin` | `<app>.admin`, `<app>.use`, `<app>.read` |
| `member` | `<app>.use`, `<app>.read` |
| `guest` | `<app>.read` |

The app decides what `<app>.use` and `<app>.read` allow. The first user is the `owner`; `POST /api/identity/users` makes a `member` unless it is given a `role` (or only `scopes`); `PUT` changes the role. The last owner cannot be given another role (409 `last_owner`). A role change applies to the user's live sessions on their next request. On upgrade from a release without roles, existing users join the default space as `owner` (scope `*`), `admin` (a `<app>.admin` scope) or `member`. The users list, the profile and the sign-in answer carry `role`; the profile's `scopes` are the effective ones. Go: `Service.Role`, `Service.SetRole`, `PublicUser.Role`, `identity.RoleScopes`.

`GET /api/identity/profile` (scope `authenticated`) answers who the caller is: `{id, username, display_name, email, scopes, assurance}`, the same shape sign-in and `PUT /api/identity/profile` return plus `assurance`. `assurance` is `"local"` for every caller today (the module holds the credentials itself); `"verified"` is reserved for an external verifier and is never emitted. Answers: 200 for a signed-in person; 401 `unauthorized` with no or a bad credential (the guard's signed-out answer); 403 `user_required` when the credential is not a person, which is an API key, a token minted from a key, or a disabled or removed account. A machine caller does not get a principal back from this route: it already knows its own key and scopes, and `user_required` is what every other person-only route answers. A token minted from a person's session still names that person and gets the profile. `scopes` is the stored list (a copy); a UI shows `scopes` as the caller's capabilities.

`GET /api/setup` answers `{"setup_required": bool, "setup_over_http": bool}`. `setup_over_http` is true only when `identity.setup` is `http` (the local-development opt-in), that is, when `POST /api/setup` can create the owner on an empty instance. It reflects the setting alone: false in the default `offline` setting, true under `http` even after an owner exists (then `POST /api/setup` answers 409 `setup_closed`, and `setup_required` is false). A UI must not show a "create the owner" form when `setup_over_http` is false: with `setup_required` true and `setup_over_http` false it shows instructions instead, "run `<app> identity setup` on the host" (naming the app's binary), and no form, because `POST /api/setup` would answer 403 `setup_offline`. With `setup_required` false it shows the sign-in.

A browser keeps a `HttpOnly`, `SameSite=Strict` cookie. Wrap the app's final handler in `identity.From(app).Middleware(handler)`: it turns the cookie into the bearer the guard verifies, and requires an `X-Requested-With` header and a same-origin `Origin` on cookie-authenticated writes. A cookie-authenticated connection upgrade (a WebSocket handshake, which is a `GET`) needs a same-origin `Origin` too (403 `bad_origin` without one). Five wrong passwords for a username and address lock further attempts for five minutes.

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
- **Revoking and expiry.** `DELETE /api/identity/keys/{id}` calls `identity.From(app).RevokeKey(ctx, id)`, the one path (an app that mints a key per child with `Service.CreateKey` calls it directly when the child stops or the key is renewed; it needs no HTTP request). It is idempotent: revoking a key that is already revoked is nil in Go and 204 over HTTP, and an id that never existed is `identity.ErrNotFound` (404). Credentials are read from the database on every verification, so a revoked key is refused by the very next request (bearer on HTTP) and the very next `mcp.ServeStdio` start (token file) with nothing to wait out. An **expired** key (`ExpiresAt` set by `ttl_hours` or `KeyInput.TTL`) is refused the same way: the expiry is compared on each verification, so it needs no sweep. The module's hourly sweep never touches API keys; an expired key stays stored (and listed) until an administrator revokes it. What a revoke does and does not end is set out under "What a revoke ends" below.
- `POST /api/token` trades any credential the caller holds (a session, a key, a token) for an EdDSA JWT: `{scopes?, audience?, ttl_seconds?}` returns `{token, expires_at, ttl_seconds, scopes, audience}`. Scopes can only narrow. A token for another **audience** is allowed only if the key lists that audience (or the caller is an administrator). Tokens last 15 minutes by default and at most an hour.
- The reply has the shape Tether's mint endpoint has, so a client app configures it as its own token source: `auth.mint_url: https://issuer/api/token`, `auth.mint_credential_file: <file holding the key>`, and `auth.mint_body: {audience: "the-callee"}`. The auth module refreshes the token and attaches it to outbound calls (`auth.HTTPClient`).

### Your own keys

Anyone signed in can make keys for their own tools, such as an agent on their laptop or a script. They need a
signed-in browser session; a key can't make keys.

- `GET /api/identity/me/keys` lists the caller's keys that aren't revoked.
- `POST /api/identity/me/keys` `{name, scopes, ttl_hours}` makes one, with scopes the caller holds. `ttl_hours`
  is at most 366 days, and 0 means no expiry. The secret is returned once.
- `DELETE /api/identity/me/keys/{id}` revokes one of the caller's own keys.

Administrators see and revoke everyone's keys on `GET`/`DELETE /api/identity/keys`, where `created_by` names the
maker.

A key someone made works only while they do. If they are disabled or removed, it stops working (401). It also
never has more than they hold now: after a demotion, scopes they no longer hold are dropped at the next request.
Keys a module or the CLI made are not affected. The key's
principal is `key:<id>`, and its `Parent` is the maker (`user:<id>`), so an app can attribute the key's work to
that person.

### What a revoke ends

A revoke takes effect at once for what is looked up in the database, and not at all for what has already been verified.

Ends immediately:

- Key lookups over HTTP: the key is refused on the very next request.
- New stdio processes: a `mcp.ServeStdio` start with that key's token file is refused.

Does not end:

- Tokens already minted from the key. An EdDSA token keeps verifying until its own `exp`: `token_ttl` (15 minutes by default, at most 1 hour).
- Stdio processes already running. `mcp.ServeStdio` verifies its token once at start and keeps serving under that principal until it exits.

An app that needs to cut a running child short stops the process; revoking its key is not enough. For a child that must stop with its key, revoke the key and stop the child.

## App A calls app B

1. B's administrator creates a **pairing code** for A: `POST /api/identity/pairing-codes` `{name: "app-a", scopes: ["b.read"], audiences: ["b"], ttl_hours: 720, ttl_minutes: 10}`. The code (`pair_…`) is single-use and expires.
2. A redeems it, with no credentials: `POST https://b/api/pair` `{code}` returns `{key, credential, token_url, jwks_url}`. A stores `key` in a file (mode 0600).
3. A configures `auth.mint_url: https://b/api/token`, `auth.mint_credential_file: <that file>`, `auth.mint_body: {audience: "b"}`. Every call A makes with `auth.HTTPClient` carries a fresh token.
4. B verifies the token with its own guard (it signed it). To let a *third* app C accept tokens B issues, point C's `auth.jwks_url` at `https://b/api/identity/jwks`, with `auth.audience` set to C's name, and mint with `audience: "c"`.

Revoking the key at B ends A's access within the token lifetime.

## Keys and storage

The `identity_*` tables are created by numbered migrations (`modules/identity/migrations/{sqlite,postgres}`, goose format through libs `core/store/migrate`) under their own version table, `identity_schema`, apart from the app's migrations. Migration `0001` is the schema as releases before versioning created it (`CREATE TABLE IF NOT EXISTS`), so their databases upgrade in place.


The signing key (Ed25519, PEM) is created `0600` at `identity.key_file` on first start, beside a public `<key_file>.jwks.json`. Several processes starting at once on one new data directory share one key: the create is exclusive (a private temp file hard-linked to `key_file`), and the process that loses reads the winner's key. Losing it invalidates minted tokens, not sessions or keys. State lives in `identity_*` tables created at start in the app's database (SQLite or Postgres). See the next section to rotate the key without breaking live tokens.

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
- **One rotation at a time.** A second call while one runs gets `409 rotation_in_progress`. The hourly prune takes the same lock, so a rotate that arrives while a prune is running also gets `409 rotation_in_progress` (retry; a prune is short), and a prune that arrives during a rotation waits for it.
- **A rotation that fails before the commit point** (the error is returned, the old key stays active) leaves the old key listed in `<key_file>.retired.json` with its real public key, because the retired record is written first; the file is then ahead of the running process. This is transient: the running process still reports no retired key, a retry rewrites the record from memory (the entry is replaced, never duplicated), and the next start drops a retired entry equal to the active key and rewrites the file. No token is lost on either side.
- **Limits.** One process per `key_file`; several instances sharing the directory would need a reload signal and are not supported. The emergency path that needs no running app: delete `key_file` and restart with `identity.allow_new_signing_key: true` for that one start (the guard below otherwise refuses, because the database holds identity rows), which invalidates every minted token; remove the setting afterwards. Every rotation is logged at warn level with the caller.

Go: `identity.From(app).RotateSigningKey(compromised)` and `.SigningKey()` do the same in process. Boot hooks must not register modules.

### Invites

How more people join. An administrator makes an invite for a role, with a label to remember who it is for: `POST /api/identity/invites` `{role, label?, ttl_hours?}` answers `{id, code, role, label, expires_at}`; the code is shown once (only its hash is kept), and the app turns it into a link (`<app>/invite#c=<code>`; the fragment keeps it out of logs). `GET /api/identity/invites` lists the pending ones, `DELETE /api/identity/invites/{id}` revokes one. An invite works once and expires (7 days by default, at most 30). Nobody can invite to a role whose scopes they do not hold.

The invited person's page asks `POST /api/invite/check` `{code}` what the invite is for (`{role, label, app, expires_at}`, or 404 `invite_invalid`) and accepts it with `POST /api/invite/accept` `{code, username, display_name, password}` (with `X-Requested-With` and this app's `Origin`): the account is created with the invite's role, the invite is spent in the same transaction, and they are signed in. Both public routes count failures toward the per-address limit. Go: `Service.CreateInvite`, `PendingInvites`, `RevokeInvite`.

## Passkeys

Passkeys (WebAuthn) are on when `identity.public_url` is a secure context: `https://…`, or `http://localhost…` (`passkeys: false` turns them off). The relying party is that URL's host and the only accepted origin its own; a browser offers passkeys only in a secure context, so a page should also check `window.isSecureContext`.

- **Add one** (signed in): `POST /api/identity/passkeys/register/begin` answers `{challenge_id, options}`; pass `options` to `navigator.credentials.create` and send the result as `POST /api/identity/passkeys/register/finish` `{challenge_id, credential, name}` (201 with the passkey). Resident keys are required, so the passkey can sign in without a username.
- **Sign in**: `POST /api/passkey/begin` (public) answers `{challenge_id, options}` for `navigator.credentials.get`; `POST /api/passkey/finish` `{challenge_id, credential}` signs in (the session cookie, as `/api/login`). Failures answer 401 `invalid_login` and count toward the per-address limit.
- **Manage**: `GET /api/identity/passkeys` (`{passkeys, passkey_only, enabled}`), `PUT /api/identity/passkeys/{id}` `{name}`, `DELETE /api/identity/passkeys/{id}`.
- **Passkey-only**: `PUT /api/identity/passkey-only` `{enabled}` makes the account refuse its password (403 `passkey_required`, given only after the password checked out). It needs a passkey first, and while it is on the last passkey cannot be deleted (409 `last_passkey`).

Each ceremony's server half (the challenge) is stored for 5 minutes and used once. A session records how it was signed in (`password`, `passkey` or `setup`); the profile reports it as `sign_in`.

### Behind a proxy, or on HTTPS

Set `public_url` to the address people use. An https `public_url` makes the session cookie `Secure`. A TLS-terminating proxy (a tunnel, `tailscale serve`, Caddy) is believed only from the peers listed in `trusted_proxies` (CIDRs; `trusted_proxy: true` is loopback): exactly one `X-Forwarded-Proto: https` from one of them makes the cookie `Secure` when `public_url` is not set; anyone else's forwarded headers are ignored.

## Two-step sign-in (TOTP)

Authenticator-app codes (RFC 6238: SHA-1, 30 seconds, 6 digits) as a second step after the password. People move up when they choose to: password, then password and TOTP, then a passkey, then passkey-only.

- **Turn it on** (signed in): `POST /api/identity/totp/begin` answers `{secret, uri, qr_svg}`. The page shows the QR code (an SVG of the `otpauth://` link) and the secret for typing. `POST /api/identity/totp/confirm` `{code}` proves the app has it, turns TOTP on and answers ten `recovery_codes`, shown once. Starting again before confirming replaces the pending secret; once it is on, `begin` answers 409 `totp_enabled`.
- **Sign in**: `POST /api/login` with the right password and no `code` answers 401 `totp_required`. The page asks for the code and sends `{username, password, code}` again. A wrong code answers 401 `invalid_code` and counts toward the sign-in limit. A recovery code works in place of a code, once (case, spaces and dashes don't matter). The session's `sign_in` is `password+totp` or `password+recovery`. A passkey sign-in asks for no code.
- **Manage**: `GET /api/identity/totp` (`{enabled, pending, recovery_codes_left, required}`). `POST /api/identity/totp/recovery-codes` `{code}` replaces the recovery codes with a new set. `DELETE /api/identity/totp` `{code}` turns TOTP off. Both take a current code or a recovery code, and the per-person limit counts wrong ones.
- **Lost the app and the codes**: an administrator resets it with `PUT /api/identity/users/{id}` `{"reset_totp": true}`, but not for themselves.

A code is accepted for the step before and after the current one, and each step only once. The secret is stored as is, because an authenticator secret cannot be hashed; the recovery codes are stored only as hashes. The profile adds `totp`. Migration `0005`.

### Requiring a second factor

`identity.require_second_factor: [owner, admin]` lists roles whose accounts must have TOTP, or a passkey when passkeys are on. Until such an account has one:

- its sessions carry no scopes, so only the signed-in identity routes answer, which is enough to enroll;
- the profile says `second_factor_required: true` (and `scopes: []`);
- the page sends the person to set one up.

The restriction lifts on the next request after enrolling. Such an account can't turn TOTP off without a passkey, or delete its last passkey without TOTP (409 `second_factor_required`). With a passkey and no TOTP, it signs in with the passkey: a password alone answers 403 `passkey_required`.

## Setup links

The friendly first run, with the same rule as the offline bootstrap: only whoever can run the binary against the data can make the owner.

```sh
<app> identity setup-link --url http://hub.example:8470     # prints http://…/setup#t=setup_…
```

The link is single use and expires (`--ttl`, default 30 minutes, at most 7 days); only its hash is stored, and it is refused once an owner exists (no new link either). The token rides in the URL fragment, so it never reaches a server log or a referrer. The page reads it and sends `POST /api/setup` with `{setup_token, display_name, username, password}`, an `X-Requested-With` header and this app's `Origin`; that creates the owner and signs them in under either `identity.setup` setting (`offline` keeps refusing the form without a token). Wrong, used or expired tokens answer 403 `setup_link_invalid` and count toward the per-address limit. Without `--url` the command prints the token alone. Go: `Service.SetupLink(ctx, ttl)` and `identity.SetupURL(base, token)`, for an installer that prints the link itself.

## Recovering a locked-out account

For someone who can no longer sign in (a lost passkey, lost authenticator and recovery codes, a forgotten
password) when no other admin can help, as for a sole owner. Recovery works like setup links: only whoever can run
the binary against the data can do it.

```sh
<app> identity recover --user <name> --url https://app.example.com    # prints …/recover#r=recover_…
```

The link is single use and expires (`--ttl`, default 30 minutes, at most 24 hours). Only its hash is stored. The
page asks `POST /api/recover/check` `{token}` what it is for (`{username, display_name, totp, passkeys,
passkey_only}`). It then sends `POST /api/recover` `{token, new_password?, clear_passkeys, clear_totp}`, with
`X-Requested-With` and this app's `Origin`. That spends the link, applies the changes in one transaction
(clearing passkeys also turns passkey-only off), ends the user's other sessions, and signs them in (`sign_in`:
`recovery`). Wrong, used or expired links answer 403 `recovery_link_invalid` and count toward the per-address
limit. Minting and using a link are logged at warning level, with the user and what changed. Go:
`Service.RecoveryLink(ctx, username, ttl)` and `identity.RecoverURL(base, token)`. Migration `0006`.

A passkey sign-in with a passkey this app doesn't have answers 401 `passkey_unknown` rather than
`invalid_login`, so the page can send the person to their password. That happens when its user or the passkey
was removed, or it was saved for another install at the same name.

## Offline bootstrap

There is never an open first-run setup over HTTP on a host where agents run: loopback is not a boundary (an agent on the same host reached an app's loopback setup first and became its owner). The owner and the first keys are created offline, before the service starts. When an app selects `identity` and `cli`, the cli root gets:

```sh
# the owner: password from the terminal (prompted twice, not echoed) or from the first line of stdin
printf '%s\n' "$OWNER_PASSWORD" | <app> identity setup --username owner --display-name "The Owner"

# an API key: written once to a new file (mode 0600, never overwritten), or with --stdout to stdout, nothing else
<app> identity key create --name station-a --scope station.read --scope station.write \
      [--audience station] [--ttl 720h] --out /run/secrets/station-a.key
```

- `identity setup` makes the first owner (scope `*`) with the same validation and the same atomic check as `POST /api/setup`, so it refuses when any user exists ("setup is already done", exit 1, nothing changed). After it, `GET /api/setup` answers `setup_required: false` and `POST /api/setup` is refused. No session is created.
- The password is never an argument or an environment variable. With no terminal on stdin the first line of stdin is the password; with one, it is prompted twice. Nothing the commands print includes it (also on errors).
- `identity key create` needs an owner (run setup first). Any scope may be granted, including `*`; the key is recorded as created by `local:operator` and shows in `GET /api/identity/keys`. Exactly one of `--out PATH` (the file is claimed with `O_EXCL` at mode 0600 before the key exists, so a taken path costs nothing; stdout then carries only the credential id) or `--stdout` (the key and a newline, the only output; notes go to stderr, never the key). `--ttl` is optional (default: no expiry), `--audience` and `--scope` repeat.
- Scripting: no prompt when stdin is not a terminal; any failure is a non-zero exit and a one-line message on stderr. A repeat `setup` on an instance that already has an owner exits non-zero, so a first-run script that must be idempotent tests `GET /api/setup` (or the message) first.
- Go: the same operations are `Service.SetupOwner(ctx, OwnerInput)` and `Service.CreateKey(ctx, KeyInput)` for an app with its own provisioning flow; `Service.OwnerExists(ctx)`, `Service.ListKeys(ctx)` and `Service.RevokeKey(ctx, id)` are the checks and the key list an offline command needs (the routes call them), so no app reads the identity tables. `cli.AddCommand(app, cmd)` adds a command to the root whichever of the two modules starts first.

**Same boot as the server.** The commands run inside the app's normal boot (`boot.StartWith` with the app's hooks, the `SUBSTRATE_CONFIG` merge), so the database, `key_file` and secrets directory are the ones the service reads. They work on a database just created by the app's migrate step that no server has opened: identity creates its own tables on start. Starting the module also creates the signing key at `key_file` if it is absent (the same file the server will use, so this is harmless on one host; a later change guards against a second issuer).

**Beside a running server.** The store allows one write connection per process, not per file. A command run while the server is up is a second process on the same SQLite file; SQLite serialises the two writers and the store waits for the lock. The server sees the new rows on its next request (credentials are read per call, not cached). Tested with a server writing sessions continuously while eight keys were created beside it. The limit: a write that holds the lock for a long time (a backup, a very large migration) delays the command; it does not corrupt anything. Postgres has no such limit.

**`identity.setup`.** `offline` (the default) or `http`. Under `offline`, `POST /api/setup` is always refused with 403 `setup_offline` and a message that names `<app> identity setup`, on a fresh instance too, so a service started by mistake before the bootstrap cannot be claimed. `GET /api/setup` still answers (`setup_required: true`, `setup_over_http: false`). `http` restores the old behaviour and is for local development only: set `identity: {setup: http}` in the manifest or in a `SUBSTRATE_CONFIG` override.

**Trust statement.** Whoever can run the binary against the database and the key file already controls the instance: they can read the database and the signing key and write rows or mint tokens directly. These commands add no authority; they make the same authority usable without an open setup window. They do not check who the operator is, and a local key's `local:operator` creator is a label, not a verified identity.

## Config

See the package comment for the full list: `key_file`, `allow_new_signing_key` (default false), `issuer`, `audience`, `session_ttl` (720h), `token_ttl` (15m), `min_password` (12), `bcrypt_cost` (12), `secure_cookies` (`auto`), `setup` (`offline`; `http` for development), `trusted_proxy` (a local TLS proxy: one `X-Forwarded-Proto: https` from a loopback peer, package `forwarded`; a remote proxy needs `secure_cookies: always`), `allowed_origins`, `retired_key_ttl` (`70m`, also the minimum; a lower value refuses to start).
