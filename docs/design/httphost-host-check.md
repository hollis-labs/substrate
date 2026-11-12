# Design: the Host check moves into httphost (brief item 7)

Status: proposal, nothing built.

## What exists

Guide rule 7 asks each app to serve on loopback and check `Host` against loopback plus a configured list. Each app carries its own `internal/hostguard`. I could read one copy: `~/dev/hollis-labs/apps/basalisk/internal/hostguard/hostguard.go` (81 lines plus a 90-line test). I did not find the copies in Parallax, Loom, Sigil or Station on this machine, so I cannot confirm they are identical; the brief says about 170 lines each, which matches Basalisk's 171 with its test. Anything below about the other four is unverified, and the first step of the work is to diff them.

Basalisk's behaviour, which I propose to adopt as the framework behaviour:

- Loopback `Host` (localhost, 127.0.0.0/8, ::1, with any port) passes.
- A name in the operator list (exact, case-insensitive, port only if the client sends one) passes only when the TCP peer is loopback and the request carries exactly one `X-Forwarded-Proto: https`; otherwise 403 "Untrusted HTTPS proxy".
- Any other `Host`: 403 "Invalid host".
- Always deletes `Forwarded`, `X-Forwarded-Host`, `X-Forwarded-For`, `X-Real-IP`. Deletes `X-Forwarded-Proto` unless it was just validated. Sets `X-Frame-Options: DENY` and `Referrer-Policy: no-referrer`.

## Where it goes

`modules/httphost/hostguard.go`, applied inside `httphost.Handler` as the outermost wrapper of the returned mux, so it covers `/api/`, `/mcp`, `/healthz`, mounts and the SPA. It is on by default; there is no off switch (a framework-wide secure default; an app that serves a public name lists it). `httphost.Handler` returning the wrapped handler means apps stop calling their own `Handler(allowed, next)`.

## Config (under `httphost`)

```yaml
httphost:
  addr: 127.0.0.1:8080
  allowed_hosts: [app.example.test]   # optional; exact Host values as clients send them
```

- `allowed_hosts`: list of strings, default empty. Loopback is always allowed and cannot be removed. Entries are lower-cased; a bare name does not admit `name:port` (listed explicitly, as in Basalisk). A wildcard, an empty string, an entry with a scheme or a path is a start error (fail closed on typos).
- Default with an empty list: only loopback names are served. On a non-loopback bind with an empty list the server starts but every request is refused; `Serve` logs one warning at start naming the setting, because that combination is almost certainly a mistake. (Tailnet-bound apps list their tailnet name; today's `ValidateBind` admits tailnet ranges, so this is a real case.)
- Optional `httphost.trusted_proxy_peers`: not proposed. Peer rule stays "loopback only", as in Basalisk. A proxy on another host is not supported by this check; widening it is a decision, not a default.

## Interaction with identity

- `identity.trusted_proxy: true` believes a single `X-Forwarded-Proto` (secure cookies, `sameOrigin` scheme). The host check makes that safe: it deletes the header unless the request names a listed host, arrived from a loopback peer and carried exactly one `https` value. The docs state the pairing: set both or neither; `trusted_proxy` without `allowed_hosts` is inert (no listed host can reach the header) but harmless.
- `identity.allowed_origins` is a separate allowlist of Origins for cookie writes. A listed host does not make its origin allowed; `sameOrigin` compares the Origin to the request's own `Host`, which the check has now validated, so a rebinding page cannot satisfy it. Document that an operator publishing `app.example.test` usually needs no `allowed_origins` entry.
- `mcp.allowed_origins` is unchanged (browser Origin check on `/mcp`).
- Stripping `X-Forwarded-For` means `clientAddr` (identity rate limiting) sees the proxy's loopback address for every client behind a TLS proxy, so lockouts are shared. That is Basalisk's current behaviour; I flag it rather than change it. A later `trusted_proxy` could carry the first `X-Forwarded-For` hop; not part of this change.

## Error body

Plain text today. Change to the framework's flat body: `403` with `{"code":"invalid_host","message":"..."}` via `apierr.Write` (and `untrusted_proxy` for the listed-host case), so every API client gets one shape. The text must not echo the offending `Host` (log it at debug instead). Browsers navigating get JSON, acceptable for a refusal.

## Migration note for apps

1. Move `allowed_hosts` from `<app>.allowed_hosts` into `httphost.allowed_hosts` in the manifest/settings; document the old key as removed in the app's conversion notes.
2. Delete `internal/hostguard` and its wrapper call in `main`; the handler from `httphost.Handler` is already wrapped.
3. Keep the app's own test that an unknown Host is refused (end-to-end through `httphost.Handler`), since that is the property the app depends on.
4. Check the response change (text to JSON) does not break a proxy health probe: `/healthz` with a loopback Host still answers; a probe using the public name needs it listed.
5. Apps whose copy differs from Basalisk's (unverified) must say why in the PR; if the difference is a real requirement it becomes an option here, not a fork.

## Files touched

`modules/httphost/hostguard.go` (new), `httphost.go` (wrap, config parse, package comment), `httphost_test.go`, `docs/adopting-substrate.md` (rule 7 now says "set `httphost.allowed_hosts`"), `docs/identity.md` (the pairing), `CHANGELOG.md`.

## Tests

Port Basalisk's test table, then add: loopback variants (`localhost:1`, `[::1]:80`, `127.0.0.2`); unknown host 403 with the JSON body and no handler call; listed host from a non-loopback peer 403; listed host with two `X-Forwarded-Proto` values 403; listed host valid passes and the header survives; loopback with `X-Forwarded-Proto: https` has it stripped; the four forwarding headers are removed on every pass; a name with an explicit port is not admitted by a bare entry; bad config entries fail start; `/healthz`, `/mcp`, a Mount and the SPA are all covered (mutation: wrap only `/api/`, a test must fail); case-insensitive match; `identity` with `trusted_proxy` over the guard gives a Secure cookie only for a validated request.

## Decisions asked of the driver

1. On by default with no off switch?
2. Loopback-only peer rule kept (no remote proxy support)?
3. Flat JSON body in place of plain text?
4. Accept that `X-Forwarded-For` stripping collapses rate-limit buckets behind a proxy for now?
