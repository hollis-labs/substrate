# Writing a module

A module is one optional capability. It wraps and pins code from `libs`, never copies it, and never owns
identity, policy, data or listeners: the app supplies those. Read `modules/capabilities` (smallest, routes only)
and `modules/events` (routes, config, an app hook) before starting.

## The shape

```
modules/<name>/
  <name>.go        package doc, init() registration, Start/Stop
  ui.json          optional: routes and UI contributions (embedded)
  <name>_test.go
```

```go
func init() { module.Register(&mod{}) }

type mod struct{}

func (*mod) Name() string { return "<name>" }
func (*mod) Start(ctx context.Context, app *module.App) error { ... }
func (*mod) Stop(ctx context.Context) error { ... }
```

- **Registration is from `init()`.** The builder's generated wiring file imports only the modules a manifest
  selects, so an unselected module is never linked.
- **Start in order, stop in reverse.** Stop must release everything Start acquired and be safe to call once
  after a failed start. The conformance suite starts, stops and restarts every module.
- **Never import another module's package from non-test code** unless the catalogue lists it under `Requires`.
  Share through the app instead: `app.Set(key, v)` and `app.Value(key)`, with keys documented as constants
  (`"events.hub"`, `"auth.guard"`). Read by key when you do not want the import.

## Config

Config arrives as `app.Config["<name>"]`, a `map[string]any` from the manifest's `config` block (JSON types:
numbers are `float64`). Document every key in the package comment, give each a default, and refuse a bad value
at Start with an error that names the key. The `config` module can override a key from `SUBSTRATE_CONFIG`.

## Hooks for the app

When the app must supply something (a PDP, plugin specs, a log, a topic authorizer) expose a package-level
`Use...` function called before `boot.Start`, as `policy.UsePDP` and `events.UseLog` do. Starting without a
required hook is an error, not a silent default that allows.

## Routes and UI

Declare every HTTP route in `ui.json` and bind handlers with `module.AddRoutes`; do not mount handlers by an
ad-hoc key. That is what guarantees a route cannot exist without an auth scope.

```json
{
  "schema_version": 0,
  "module": "<name>",
  "routes": [ { "method": "GET", "path": "/api/<name>", "scope": "{app}.read" } ]
}
```

```go
//go:embed ui.json
var uiJSON []byte

func (*mod) UI() ui.Contribution { return ui.MustParse(uiJSON) }

// in Start:
return module.AddRoutes(app, m.UI(), map[string]http.Handler{"GET /api/<name>": handler})
```

- `scope` is required. Use `"{app}.<verb>"`, or `"authenticated"` for any verified caller. `"{app}"` expands to
  the app name. An app can replace every scope of your module with the string config `<name>.scope`.
- `method` may be `*`; a path ending in `/` is a subtree (`/api/plugins/`). Paths must be under `/api/` or
  `/admin/`.
- Every declared route needs a handler and every handler a declared route; `AddRoutes` fails otherwise.
- Errors use `apierr.Write(w, status, code, message, details)`, the flat `{code, message, details?}` body the
  frontend reads. Streaming handlers must not buffer; the wrapper in httphost passes `Flush` through.
- The same file can carry UI `contributions`, `envelopes` and a companion `package`, validated against
  `ui.Default`. The kinds are the design team's approved vocabulary (SF-2 v0.3): `nav.item`, `nav.group`,
  `settings.region`, `surface`, `panel`, `dock.tab`, `widget`, `slot`; do not invent others. Each kind takes only
  its own fields (the validator refuses a stray one), and goes in its default region unless it names one
  (`nav.primary`, `nav.group:<key>`, `nav.footer`, `settings.sections`, `surface.page`, `aside.panels`,
  `widgets.grid`; `dock.tab` and `slot` name a host-defined region such as `chat.drawer`).
  - Settings are referenced, never defined: a `settings.region` lists `{id, scope_kind}` groups that the admin
    manifest serves.
  - `dock.tab` and `slot` name host-defined regions, which the app declares under `frontend.regions` in its
    manifest (docked regions name the inline `anchor` they sit under). The core regions are not declared. The
    builder fails on an undeclared region, a `dock.tab` outside a docked region, a `slot` inside one, and a
    `slot.opens` that does not name a `dock.tab` in the docked region anchored under the slot's region.
  - A frontend `route` is a path in the SPA and must never equal a backend path.
  - A companion npm package is named in `package.name` and must be pinned in the BOM's `kits:` section; the
    version lives only there.
  - `plugin-host-ui` 0.2.0 still exports `dock.tab` as `drawer.tab`; hosts map the name until the kit renames it.
- `substrate frontend` merges the contributions of exactly the modules a manifest selects, so removing your
  module removes its UI.

## Health

If the module can be unhealthy, add a check: `health.From(app).Add("<name>", func(ctx) error {...})`. The
catalogue entry then lists `health` under `Requires`.

## Register it

1. `catalog/catalog.go`: add `{Name, Import, Requires, Status: "ready"}`. `Import` must be
   `github.com/hollis-labs/substrate/modules/<name>`.
2. `conformance/conformance_test.go`: add the blank import so the registry matches the catalogue.
3. Libraries you wrap: pin them in `bom/bom.yaml` (`libs:` or `third_party:`), run
   `go run ./cmd/substrate pins sync`, then `go mod tidy`. Pins live nowhere else.
4. `CHANGELOG.md` under `[Unreleased]`, and `README.md` / `docs/status.md` if the module changes what an app
   can do.

## What the suite already checks for you

Catalogue consistency (imports, `Requires`, names, directories); start, stop and restart under every profile and
variant it is valid for; `ui.json` validity; and, in enforce mode, that every declared route is mapped and served
(unauthenticated callers get 401, never 403 `route_not_mapped`). Add tests for what is yours: config errors,
the hook, and the behaviour of your handlers, including the failure paths. Prove a new check can fail by
breaking the thing it guards once.

## Before you push

```sh
go vet ./... && go test ./... && golangci-lint run
python3 scripts/scan-public --ref HEAD
go run ./cmd/substrate pins check
```

The repository is public: no secrets, internal hosts, internal addresses or personal paths in files or commits.
