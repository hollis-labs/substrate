# /mcp behind a local TLS proxy: one Host decision, made once

Status: design, not built. Depends on PR #23 (host check in `httphost`), which is open at the time of writing.

## Problem

An app on a loopback listener reached by a public name through a local TLS proxy (`allowed_hosts` plus identity
`trusted_proxy`: Tangent, Loom, Parallax, Station's units) passes the app's Host check, then `/mcp` answers 403
`Forbidden: invalid Host header "<public name>"`. It works on `127.0.0.1` and fails through the proxy name, so the Tether
gateway cannot reach Tangent by its public name. Tangent works around it by showing `/mcp` the loopback name for a
request its own check validated; every app would copy that.

## Where the refusal lives (read, not inferred)

- `modules/mcp/mcp.go`: `MCPHandler()` returns `mcphttp.NewHandler(m.srv, HandlerOptions{AllowedOrigins})`, then
  wraps it with the auth guard when auth is started.
- libs `mcp/plugin-mcp@v0.6.0/go-mcp/transport/http/handler.go`: `NewHandler` builds
  `mcpsdk.NewStreamableHTTPHandler(..., &StreamableHTTPOptions{Stateless: true})`. `DisableLocalhostProtection` is not set
  and `HandlerOptions` has only `AllowedOrigins`, so there is no way to set it from outside.
- go-sdk `v1.8.0` `mcp/streamable.go` `ServeHTTP`, first statement: if `!DisableLocalhostProtection` and the connection's
  local address (`req.Context().Value(http.LocalAddrContextKey)`) is loopback and `req.Host` is not loopback
  (`internal/util.IsLoopback`: `localhost`, or an IP that is loopback), answer 403. It reads `req.Host` and the
  context only; no header, no forwarded value.
- The check cannot be addressed from a wrapper except by changing `req.Host` or the local-address context value, or by
  building the SDK handler with the option on. The last needs a libs change.

## Options

| | Change | Per-request | Cost |
|---|---|---|---|
| 1. Rewrite `r.Host` in the mcp wrapper, for validated requests only | Substrate only | yes | Downstream of the wrapper sees a loopback Host; the SDK and libs handler read nothing else from it (the libs origin guard reads `Origin`, the SDK's cross-origin check is nil here). |
| 2. libs option `HandlerOptions.DisableLocalhostProtection`; mcp module builds two handlers (SDK protection on, off) and picks per request | libs patch plus BOM bump, then Substrate | yes | Two SDK handlers over one stateless server; a libs release owned by the maintainer. |
| 3. Global disable | libs | no | Rejected: removes DNS-rebinding protection for every request. |

**Recommendation: option 1 now; option 2 only if the libs owner wants it.** Option 1 needs no libs PR, is the same
mechanism Tangent already runs (so it is proven), and keeps the SDK's protection on for every request the host check did
not accept. If libs later adds the option, the Substrate side swaps the rewrite for the handler choice with no app change.

## Design (option 1)

The decision comes from the same place the Host check does. The check records what it accepted in the request context;
the mcp module reads that and nothing else.

```go
// new leaf package hostcheck (sibling of clientaddr; imports nothing from modules)
func Validated(ctx context.Context) (host string, ok bool) // the public name the check accepted
func With(ctx context.Context, host string) context.Context // called only by the check
```

- `httphost` host guard (PR #23 `hostGuard`): in the `proxied` branch (listed host, loopback peer, exactly one
  `X-Forwarded-Proto: https`) add `r = r.WithContext(hostcheck.With(r.Context(), r.Host))`. Loopback hosts need no
  marker: the SDK already accepts them. An app with its own check (until it deletes it) calls `hostcheck.With` itself;
  that is the documented seam.
- `mcp.MCPHandler()`: add a wrapper inside the auth guard:

```go
func localHostFor(h http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if pub, ok := hostcheck.Validated(r.Context()); ok {
            if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && la != nil {
                r = r.Clone(r.Context())
                r.Host = la.String()            // the loopback address the connection arrived on
                r = r.WithContext(hostcheck.With(r.Context(), pub)) // the public name stays readable
            }
        }
        h.ServeHTTP(w, r)
    })
}
```

`r.Host` becomes the loopback address the request really arrived on, so the SDK condition is satisfied for this request
only. Anything not validated keeps its Host, hits the SDK check, and is refused as today (defence in depth if the host
check is ever absent or bypassed). Auth runs outside the wrapper and sees the true Host.

## Failure modes

- A forged marker: context values are not client-settable; validation also needs a loopback peer and one `https` proto.
- Listener not on loopback (tailnet bind): the SDK check does not fire; the wrapper is a no-op in effect.
- No `LocalAddrContextKey` (httptest without a server): wrapper leaves Host alone.
- App does not select `httphost`'s check (it is on for every app after PR #23, no off switch): nothing marked, unchanged.

## Tests I would write (`modules/mcp`)

1. Real `httptest` server on loopback with the host guard in front, `allowed_hosts: [app.example.test]`: `POST /mcp`
   with `Host: app.example.test`, proto https, peer loopback, answers 200 (it is 403 today: write this first and watch it fail).
2. Same request without the proto header: 403 `untrusted_proxy` from the guard, never reaching the SDK.
3. A non-loopback Host that the guard did not validate, with the guard bypassed (mcp handler served directly): the SDK
   still answers 403. This is the "stays on for everything else" test.
4. `Host: 127.0.0.1:port` and `localhost`: 200, unchanged.
5. `hostcheck.Validated` is readable in a tool handler's context (public name preserved).
6. Mutation: remove the `Validated` condition (rewrite always) and test 3 must fail.

## What apps change

Tangent, Loom, Parallax, Station: delete the loopback-name workaround and any `/mcp` special case; nothing else. Apps
with their own Host check call `hostcheck.With` on acceptance until they move to `httphost.allowed_hosts`.
`docs/adopting-substrate.md` and the mcp module doc get one sentence: behind a proxy, `/mcp` works through every name
the host check serves.

## Not verified

I did not run Tangent's workaround or a real proxy. The claim that nothing in the SDK or libs handler reads `r.Host`
after the check is from reading the v1.8.0 `ServeHTTP` head and the libs handler only; the rest of the SDK's request path
(session, SSE writer) was not read for Host use. Test 1 against a real listener answers it.

## Decisions for the driver

1. Option 1 (no libs change) now, or wait for a libs `HandlerOptions.DisableLocalhostProtection` (option 2)?
2. A public leaf package `hostcheck` (so apps with their own check can mark requests), or keep the marker internal to
   `httphost` and require apps to adopt `allowed_hosts` first?
3. Sequence: land after PR #23 merges (the marker is added in `hostGuard`), as a follow-up PR.
