# Revalidating embedded web assets: ETag and 304

Status: design, not built. Tangent reports that framework webui serving sends no `ETag` and no `Last-Modified`, so a
browser cannot revalidate with 304; the pre-Substrate Tangent did. (I did not check Tangent's old code.)

## Is it already possible? No.

The path: `httphost.Options.Assets` (an `fs.FS`) to `apphost.New(apphost.Config{Name, Assets, Routes})` in
`modules/httphost/httphost.go`, to libs `app-host@v0.1.0` `host.New`, to `webui.Handler(webui.Config{FS, BasePath})` in
`ui-go` (v0.1.0 in the BOM; v0.4.0 in the module cache has the same shape; neither mentions an ETag).

- `webui.Handler` sets `Cache-Control` itself (`CachePolicyFor`: `immutable` for a year under `assets/`, `no-cache` for the
  document and every unhashed file), then hands off to `http.FileServer(http.FS(fsys))`.
- `net/http` adds `Last-Modified` only for a non-zero `ModTime`, and `embed.FS` reports zero. It never invents an `ETag`;
  it only honours one the handler set before `ServeContent` (Go 1.26 `fs.go`: `w.Header().get("Etag")` in the precondition
  checks).
- `no-cache` means "revalidate", but with no validator every load of `index.html` and every unhashed file is a full 200.
  Hashed files under `assets/` are already free (immutable); the loss is the document and unhashed files.
- Options that exist: none reaches the response. `apphost.Config` exposes only `Name`, `Assets`, `BasePath`, `Routes`,
  `Plugins`; it does not even pass through `webui.Config.ImmutableDir`. An `fs.FS` wrapper can supply a `ModTime`
  (`Last-Modified`, `If-Modified-Since`), but a build time on every file is a weak validator and no content hash.

## Proposal: smallest change, in Substrate

A content-hash `ETag` middleware around the asset handler in `httphost.Handler`, where `outer.Handle("/", h.Handler())` is
today. No libs change, no BOM bump, and the libs `Cache-Control` stays exactly as it is.

```go
// modules/httphost/etag.go (new)
func withETag(assets fs.FS, next http.Handler) http.Handler // GET and HEAD only
```

For a GET or HEAD it resolves the file the way `webui.Handler` does (empty or `/` is `index.html`; a missing path with an
extension is a 404 and passes through untouched; a missing path without one falls back to `index.html`; a directory
passes through), looks up that file's strong validator, sets `ETag: "<base64url(sha256)[:22]>"` and calls `next`.
`http.FileServer` then answers `If-None-Match` with 304 itself; Range and HEAD keep working. The validator is computed once
per path and memoised, keyed by size and a non-zero `ModTime`, so an `os.DirFS` in development re-hashes when a file
changes while an embedded FS hashes once. `Cache-Control` is whatever `webui.Handler` set.

The resolution rules duplicate about ten lines of `webui.Handler`. The divergence risk is covered by a test that walks the
real FS rather than by sharing code. If libs later adds the option (a `webui.Config` field plus an `apphost.Config`
pass-through, patch releases of both, then a BOM bump) the middleware is deleted and nothing else changes. File that libs
item, but do not block on it.

## Failure modes

- Compressed responses: none in the chain today. A future compression middleware must emit weak validators.
- `Range`: `FileServer` already evaluates `If-Range` against the `ETag` we set.
- Memory: one hash per served file; the first request per file reads it once (streamed, not buffered whole).
- Auth: assets are public and the guard does not wrap `/`, so a validator leaks nothing.

## Tests I would write (`modules/httphost`)

1. Every file in a fixture FS: first GET has an `ETag`; the repeat with `If-None-Match` answers 304 with no body.
2. A client route (`/settings`) falls back to `index.html` and carries its ETag; 304 on the repeat.
3. A missing asset with an extension: 404, no `ETag`. A missing path without one: the fallback.
4. `Cache-Control` unchanged: `immutable` under `assets/`, `no-cache` for the document.
5. A changed body (`fstest.MapFS`) gives a new `ETag`; with `ModTime` set, the memo refreshes.
6. HEAD, and Range with `If-Range`; a POST gets no `ETag`.
7. Drift: for each file, the path our middleware resolved equals the file webui served (the body hash matches the `ETag`),
   so a libs resolution change fails loudly.

## What apps change

Nothing. The first deploy turns revalidation on for every app that passes `Assets`.

## Not verified

I did not run it and did not check what Tangent's earlier handler sent. I read `webui.go` for v0.1.0 and v0.4.0 and the Go
1.26 `net/http/fs.go` precondition code. I did not test that a middleware-set `ETag` survives the request clone
`webui.Handler` makes before `FileServer`; it sets headers on `w`, so it should.

## Decisions for the driver

1. Substrate middleware now (recommended), or only a libs change (`webui.Config` plus `apphost.Config`)?
2. On by default with no switch, or behind `httphost.asset_etag: false` as an escape hatch?
3. While a libs change is open, also pass `ImmutableDir` through `apphost.Config` (apps cannot set it today)?
