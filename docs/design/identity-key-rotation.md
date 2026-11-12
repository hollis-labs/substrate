# Design: identity signing-key rotation

Status: proposal, not built. Part of CW-20261112-0080 (Go half). For driver approval before any code.

## Today

One Ed25519 key at `identity.key_file` (PEM, 0600). Its `kid` is the first 8 bytes of the public key, base64url. Tokens carry that `kid`. `<key_file>.jwks.json` and `GET /api/identity/jwks` publish exactly one key. The module's own verifier is `libauth.NewJWTVerifier` with `BootstrapKeyFile` set to the `.jwks.json`; that file is read once at start and never again. Losing or replacing the key kills every minted token and every third-party app that cached the old key.

Facts from `libs/security/auth` v0.3.0 that shape this: the verifier picks the key by `kid` from a multi-key set; a remote verifier (`jwks_url`) refetches the set every 5 minutes and on a `kid` miss (at most once per 10 s); a refetch replaces the set, so a key that disappears from the JWKS stops verifying at the next fetch. Tokens live at most 1 h (`maxTokenTTL`).

## Overlap: how old tokens keep verifying

Rotation never invalidates a token before its own `exp`. The old key stays in the published set as a **retired** key until `not_after = retired_at + maxTokenTTL + 10 min` (default 70 min; config `retired_key_ttl`, floor 70 min). After that no unexpired token can have been signed with it.

- Signing always uses the active key; verification accepts active plus every retired key whose `not_after` is in the future.
- This module's own verifier stops using the read-once `BootstrapKeyFile`. `Service` holds the key set (kid to public key) behind an `atomic.Pointer`; `Verify` for a JWT looks the `kid` up there (small in-module EdDSA check reusing the claims rules of the libs verifier, or a `NewJWTVerifier` rebuilt from the new JWKS bytes and swapped atomically; decide at build time, the rebuild is the smaller change).
- Other apps with `auth.jwks_url` see the new key on their next periodic fetch or on the first token with the new `kid` (at most about 10 s of `ErrUnavailable` if they fetched within the last 10 s). Their cached old key remains valid because the JWKS keeps listing it until `not_after`.

## Published key set

JWKS and `<key_file>.jwks.json` list: the active key, then retired keys within `not_after`. `Cache-Control: public, max-age=300` stays; the 70 min floor covers it. Each entry is the same JWK as today; a retired entry carries no extra members (standard JWK has no expiry field; `not_after` lives only in our state file).

## State and layout

```
<key_file>                  active private key, PEM, 0600     (unchanged path and format)
<key_file>.retired.json     [{kid, x, retired_at, not_after}]  public keys only, 0600
<key_file>.jwks.json        derived: active + retired within not_after, 0644
```

Retired private keys are not kept: they never sign again, so there is nothing to protect or leak. The JWKS file is derived and rewritten (temp file then rename) at every start and every rotation; it is never the source of truth. A fresh app, or one that never rotates, has no `.retired.json` and behaves exactly as today (no migration).

Single process per `key_file` is assumed, as today. Several instances sharing one directory would need a reload signal; out of scope, noted as a limitation.

## What the operator runs

An endpoint, because rotation must swap the key inside the running process (a CLI that edits files cannot reach a running server's memory):

- `POST /api/identity/signing-key/rotate`, body `{compromised?: bool}`. Scope `<app>.admin` (the owner holds it through `*`). Reply `{kid, retired_kid, not_after}`. Not a public route; ordinary auth applies.
- `GET /api/identity/signing-key`: `{active: {kid, created_at}, retired: [{kid, retired_at, not_after}]}`. Same scope.
- `compromised: true` sets the retired key's `not_after` to now: the old key is removed from the set at once, which invalidates its outstanding tokens locally and, after remote caches refetch, elsewhere. Without it the overlap above applies.

Both declared in `ui.json` like the other identity routes. A client app or a one-line `curl` with an admin API key is the operator surface; no new CLI is added to the module (an app's own `cli` can wrap the call). Offline fallback: deleting `key_file` and restarting still works and is the old hard break; documented as the emergency path.

## Rotation steps (in order)

1. Generate the new key in memory.
2. Write `.retired.json` with the old public key added (`retired_at` = now, `not_after` = now + `retired_key_ttl`, or now if compromised), via temp file and rename.
3. Write the new key to `<key_file>.new` (0600, fsync) and `rename` it over `key_file`. This rename is the commit point.
4. Rewrite `.jwks.json` (temp and rename).
5. Swap the in-memory set and signing key atomically; a mutex serialises rotations and a second call during one returns 409.

Sweep (existing hourly loop) drops retired entries past `not_after` from the file and the set.

## Failure modes

- Crash before step 3: `key_file` is unchanged; `.retired.json` may list the still-active kid. Start removes any retired entry whose kid equals the active kid. No effect on tokens.
- Crash between 3 and 4 or 5: `key_file` is the new key; `.retired.json` already has the old one; start regenerates `.jwks.json`. Tokens signed by the old key still verify.
- Disk full or write error at steps 2 to 4: rotation returns an error, memory is untouched (step 5 is last), the old key stays active. A failed step 3 after a successful step 2 leaves the benign step-1-crash state above.
- `.retired.json` unreadable or corrupt at start: start fails with a clear error rather than silently dropping old keys (that would invalidate live tokens). The operator deletes it to accept the break.
- Clock skew: `not_after` includes 10 minutes beyond the longest token life; a verifier whose clock is far ahead may refuse a retired key early, same as it would refuse the token.
- Two rotations in quick succession: both retired keys are kept, each with its own `not_after`.

## Tests to write

- Token minted before rotation verifies after it (module verifier and a second app using `jwks_url`); a token minted after verifies with the new `kid`; the JWKS lists both and the `kid`s differ.
- After `not_after` (injected clock) the old key leaves the set and an old token fails; sweep prunes the file.
- `compromised: true` makes the old token fail at once.
- Restart after rotation: state survives, old token still verifies, JWKS unchanged. Restart after each simulated crash point (a fault hook between steps 2 to 4) keeps old and new tokens valid.
- Corrupt `.retired.json` refuses to start; stale retired entry equal to the active kid is dropped.
- Non-admin gets 403; unauthenticated 401; concurrent rotations give exactly one success and one 409; concurrent mint and verify during rotation under `-race`.
- Mutation checks: drop the retired key from the set, make not_after immediate, swap step 3 before step 2: each must fail a named test.

## Decisions for the driver

1. Endpoint rather than module CLI (above).
2. Default overlap 70 min, floor 70 min.
3. In-module verifier swap by rebuilding the libs verifier (small) versus a custom check.
4. Whether `rotate` should require `*` rather than `<app>.admin`.
