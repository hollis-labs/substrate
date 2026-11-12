# Design: explicit local trust for stdio MCP (Change 8)

Status: proposal, nothing built. Needs the driver's agreement before code.

## Problem

`mcp.ServeStdio` in enforce mode requires a token verified by the auth module. An identity-issued key can only be made through the HTTP API, so a project-local database used through `<app> mcp` needs a server, a first-run setup and a key first. The owner decided (2026-11-12) that a stdio process started locally against a local file runs under filesystem trust, as the app's CLI commands already do.

## Shape

One new field, set in Go at the call site, never read from config:

```go
type StdioOptions struct {
    In        io.ReadCloser
    Out       io.WriteCloser
    TokenFile string
    // LocalTrust runs tool calls under the fixed local operator principal when no
    // token is configured. Chosen by the app's code; there is no config key for it.
    LocalTrust bool
}
```

`identify` changes in one branch only. Today: guard nil or off returns ctx unchanged; no token in enforce returns an error; no token in permissive returns ctx unchanged. New: if `o.LocalTrust` and the token file is empty (neither `TokenFile` nor `mcp.stdio_token_file`), stamp `LocalPrincipal` and return. Everything else is untouched. With the option unset the code path is today's. With a token present it is read and verified as today whatever the option says. A failing token is an error and does not fall back to local trust, so a misconfigured gateway key fails loudly instead of silently gaining operator rights.

`LocalTrust` with a nil guard or `off` mode changes nothing (nothing to stamp), so an app can pass it unconditionally from its stdio command. In permissive mode with no token the principal is also stamped (today the call runs unidentified), so permissive audit shows `local:operator` instead of "missing".

## The local principal

```go
libauth.Principal{
    Verified: true,
    ID:       "local:operator",
    Kind:     libauth.KindOperator, // the libs constant exists ("operator")
    Display:  "local operator (filesystem trust)",
    Scopes:   []string{"*"},
    Audience: app.Name,
}
```

Scopes `*`: `Principal.HasScope` (libs auth v0.3.0) treats `*` as every scope, which is what the owner holds. `Authorize`, the mcp scope and every per-tool scope pass with no special case in tools, and it stays correct when the app adds scopes. Enumerating the scope table instead would need a read after the app registers its tools and would go stale; I do not recommend it. If the driver wants less than owner, the cheap variant is `StdioOptions.LocalScopes []string` (default `*`); I would not add it until an app asks.

Distinguishable: the id prefix `local:` cannot collide with `user:<id>` or `key:<id>` (the two ids identity mints), and `Kind` `operator` is never issued by identity. The module also writes one line to stderr at start (stdout is the protocol): `mcp: stdio under local filesystem trust (no token)`.

Not verified: whether the libs guard `Event` passed to the audit sink carries the principal id or kind. I read `AuthorizeTool`, which emits an `Event`, but did not trace its fields. If it does not carry the id, the audit test below fails and the fix is a libs change, not a Substrate one.

## What "local" means

The module cannot tell who started it, where stdin comes from, or whether the file is on a network share. The trust statement, to go verbatim in the docs: **the process runs as a user who can open the data file.** Nothing more is checked or claimed. Whoever can do that can already read the database and the signing key and write rows directly, so demanding a token from them adds ceremony and no protection. The module does not check file mode, owner or database kind; checking would suggest a guarantee it cannot give.

Gateway as parent (Tether launching `loom mcp` as a child): I agree it is local by this definition. The gateway is the authenticated surface and the child runs as the gateway's user, who can open the file. Two caveats for the docs. (1) A gateway that serves several callers through one child gives every caller operator scope inside the app, so the gateway must authorize per caller, or launch the child with a token (`mcp.stdio_token_file`) so identity is real and per-tool scopes bite. (2) The app decides, not config: a gateway child is local only if the app's command passes `LocalTrust`.

## Not changed

`/mcp` over HTTP, `mount.MCPHandler`, `Authorize` and the auth module. No config key; an override file cannot enable it. The default is off.

## Question: a stdio-only process creating a key file

Today `identity.Start` runs `loadKey`, which creates `identity-signing.key` (0600) and `.jwks.json`, and migrates the `identity_*` tables, in every command, because boot starts every manifest module. Options:

1. **Lazy key creation.** `loadKey` moves behind a `sync.Once`, called by whatever needs the key (token mint, JWKS handler, pairing). `Verify` of `ses_` and `key_` tokens needs no signing key. The JWT verifier is built from `<key_file>.jwks.json` at Start, so it must also become lazy or tolerate a missing file. Table migration stays at start (idempotent). Smallest change; fixes every CLI and stdio command with nothing for the app to do, and removes the surprise that `<app> version` writes a secret. Risk: a key-write failure surfaces on first use (HTTP 500) instead of failing start.
2. **Per-command module set.** Right in principle, a large change to boot and the manifest (see the opinions doc).
3. **A flag**, such as `identity.eager_key: false` or a `boot.Options.Skip["identity"]` for the stdio command. Simple, but the app must remember it, and Skip leaves routes and dependants unregistered, which needs a rule.

Recommendation: **option 1**, shipped as its own PR ahead of the rest, because it is right independent of local trust (a read-only run should not write a secret). An app that wants no identity tables at all for local use needs option 2; I would not build that for this.

## Files touched

`modules/mcp/mcp.go` (option, `identify`, package comment), `modules/mcp/stdio_test.go`, `docs/adopting-substrate.md` (section 5 rule and the trust statement), `CHANGELOG.md`. Lazy key PR: `modules/identity/identity.go`, `identity_test.go`.

## Tests

- Enforce, no token, option unset: error (existing test stays).
- Enforce, no token, `LocalTrust`: a tool call succeeds, `Authorize` passes for a scoped tool, the handler sees id `local:operator`, kind `operator`.
- Token given plus `LocalTrust`: principal is the token's; a bad token errors with no fallback; a token without the mcp scope is refused.
- `mcp: {local_trust: true}` in manifest or override changes nothing (mutation: make `identify` read it, the test must fail).
- HTTP `/mcp` in enforce with no credentials is still 401 in the same app.
- Audit event carries the local id (subject to the libs question above).
- Lazy key: a `version` run with identity selected leaves no key file; the first mint creates it 0600; a restart reuses it.

## Decisions asked of the driver

1. Principal `local:operator`, kind `operator`, scopes `*`: agreed, or a scoped variant?
2. A gateway child counts as local, with caveat (1)?
3. Ship the lazy identity key as its own PR first?
4. A supplied token that fails never falls back to local trust: agreed?
