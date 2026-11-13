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
at Start with an error that names the key. The `config` module deep-merges the YAML file named by `SUBSTRATE_CONFIG` over it (maps merge recursively; scalars and lists replace; `null` removes a key, so your default applies). Numbers always arrive as `float64`, whichever source set them: the manifest's JSON gives `float64`, and the `config` module converts every integer from the override file (nested maps and lists included) to `float64` as it merges, refusing an integer above 2^53 with an error naming the key. Read a number with `.(float64)` only; an `int` case is dead code.

## Hooks for the app

When the app must supply something (a PDP, plugin specs, a log, a topic authorizer) expose a package-level
`Use...` function called before `boot.Start`, as `policy.UsePDP` and `events.UseLog` do. Starting without a
required hook is an error, not a silent default that allows.

A module that needs the effective config (the `SUBSTRATE_CONFIG` override already applied) from the app, or a
step between two modules, does not need its own hook type: the app passes `boot.Options` to `boot.StartWith`
(`AfterConfig`, and `Before` keyed by module name). A module sees only the resulting `app.Config`.

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
  A module that relays a third-party protocol cannot choose its own error shape from inside: the app declares the
  path raw (`httphost.Options.RawPrefixes`, `Mount.Raw`; `docs/adopting-substrate.md` section 5), so say in your
  docs which prefix the app must list.
- The same file can carry UI `contributions`, `envelopes` and a companion `package`, validated against
  `ui.Default`. The kinds are the design team's vocabulary (SF-2 v0.4.1): `nav.item`, `nav.group`,
  `settings.region`, `surface`, `panel`, `dock.tab`, `widget`, `slot`, `menu.item`, `command`; do not invent others. Each kind takes only
  its own fields (the validator refuses a stray one), and goes in its default region unless it names one
  (`nav.primary`, `nav.group:<key>`, `nav.footer`, `settings.sections`, `surface.page`, `aside.panels`,
  `widgets.grid`; `dock.tab` and `slot` name a host-defined region such as `chat.drawer`; `menu.item` and
  `command` use the menu regions below).
  - Settings are referenced, never defined: a `settings.region` lists `{id, scope_kind}` groups that the admin
    manifest serves.
  - `dock.tab` and `slot` name host-defined regions, which the app declares under `frontend.regions` in its
    manifest (docked regions name the inline `anchor` they sit under). The core regions are not declared. The
    builder fails on an undeclared region, a `dock.tab` outside a docked region, a `slot` inside one, and a
    `slot.opens` that does not name a `dock.tab` in the docked region anchored under the slot's region.
  - `menu.item` and `command` (v0.4) carry an `action`: exactly one of `{navigate: <route>}`, `{openRegion: <region>,
    tab?: <dock.tab key>}` or `{call: <catalog function>, input?: {...}}`. An action holds no code and no URL; the
    host sends a `call` through its action gateway, which decides confirmation, not the plugin. `risk` is `low`
    (default), `medium` or `high`. A `menu.item` goes in a core menu region (`menu:<subject-kind>`, `row.actions`,
    `toolbar.primary`) or a declared non-docked host region, takes `label`, `icon`, `order` (default 100, lower
    first), `shortcut` (a display hint) and `when` (a subject kind, only in a `menu:` region); a `command` goes
    in `command.palette` (its default) and takes `label`, `icon`, `keywords`, `group`. The builder checks that a
    `navigate` names a `surface` route that is not a backend path, an `openRegion` names a declared docked
    region, and its `tab` is a `dock.tab` in that region. A `menu.item` key is unique per region and module.
  - `user.menu` (v0.4.1) is the signed-in user's menu, opened from the shell's identity chip. It is a core,
    non-docked region: implicit in every host, never declared (declaring it under `frontend.regions` is a build
    error) and never listed in the generated manifest's `regions`. Only a `menu.item` may target it (a `command`,
    `nav.item`, `slot` and the rest are refused), and `when` is an error there. Who the user is never appears in
    the manifest: the shell supplies the identity at run time. A `menu.item` in any menu region may also set
    `group` (items sharing a group stay together, a divider separates groups; convention in `user.menu`:
    `account`, then `session` with `order` 900 or more for sign out) and `requires`. A `requires` is a scope
    string, and a capability is a scope: it may contain `{app}`, which the builder expands to the app name (as a
    route's scope is), and the shell hides the item unless the caller holds that scope or `*`. It is a display
    hint; the backend still authorizes. A `call` in `user.menu` is named `<module>.<verb>` with a camelCase verb
    (`identity.signOut`); the builder checks that shape only. Whether anything registered that function is the
    shell's business at run time (an unregistered one is skipped and listed in its diagnostics).
  - A module that names a companion `package` the BOM does not pin in `kits:` fails the build with
    `module <name>: package <pkg> is not pinned in the BOM kits section`.
  - `envelopes.emits` lists `kind@version` strings; the frontend manifest turns them into `bindings`
    (`{kind, version}`, sorted, unique) for the shell's binding table.
  - Docked regions in `frontend.regions` may set dock attributes: `open_state` (`hidden`, `open`), `exclusive`,
    `sizes` (`options` from `sm`, `md`, `lg`, and a `default`), `dismiss_key` (`Esc`), `mobile`
    (`bottom-sheet`), `edge` (`side` or `bottom`) and, for a bottom dock, `height_px` (`min`, `default`, `max`).
    They appear on the region in the frontend manifest in snake_case.
  - A frontend `route` is a path in the SPA and must never equal a backend path.
  - A companion npm package is named in `package.name` and must be pinned in the BOM's `kits:` section; the
    version lives only there.
- `substrate frontend` merges the contributions of exactly the modules a manifest selects, so removing your
  module removes its UI. An app (which is not a module) contributes through the same file shape, named by
  `frontend.contributions` in its manifest with `module` set to the app name; it is merged last under the same
  rules, and may not declare `routes`.

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
