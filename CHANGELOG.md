# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Semver from the first release; pre-1.0
minor releases may break APIs.

## [Unreleased]

### Breaking

- The `sqlite` module is now `database`, on libs `core/store` with a `driver` of `sqlite` (default) or `postgres`. Config moves from `sqlite.*` to `database.sqlite.*`; `sqlite.UseMigrations` and `sqlite.From` become `database.UseMigrations` and `database.From`. Postgres connection strings are secrets: `dsn_file`/`dsn_env` (and `migrate_dsn_*`), never written in config. (migration: none; pre-release, no consumers)

### Changed

- The `config` module deep-merges the `SUBSTRATE_CONFIG` override: maps merge recursively (an override of only `database.sqlite.path` keeps the other `sqlite` keys); scalars and lists replace; an explicit `null` removes the key so the module default applies; a key only in the override is added; on a type clash the override wins. Before, the merge was one level deep and a nested block replaced the whole manifest block. A module block in the override that is not a map is now an error naming the module. A behaviour change under the compat policy's pre-1.0 rule: an override that relied on a nested block replacing the manifest block must now `null` the keys it wants gone.

### Added

- `GET /api/identity/profile` (scope `authenticated`, declared in the identity `ui.json`): the signed-in person as `{id, username, display_name, email, scopes, assurance}`; `assurance` is `"local"` for every caller (`"verified"` is reserved and not emitted). 401 when signed out; 403 `user_required` for an API key, a token minted from a key, or a disabled account. Read through `Service.User`. Additive; the `PUT` shape is unchanged.
- `identity.Service.User(ctx, id)`: a public view of an account (`identity.PublicUser`: id, username, display name, email, scopes, disabled; no hashes) so apps stop reading `identity_users` directly. Unknown id gives `identity.ErrNotFound`; a disabled account is returned with `Disabled` set.
- `frontend.contributions` in `substrate.yaml`: a path (relative to the manifest, inside its directory) to the app's own UI contribution file, in the shape of a module `ui.json` with `module` equal to the app name. `substrate frontend` validates it against the same vocabulary and merges its items under the same cross-contribution rules, tagged with the app name; its `package` and the base kits are pinned like a module's. `routes` in the file are refused. The app name must not be a catalogue module. Optional field, additive: not breaking. `frontend.Build` takes an optional `frontend.WithAppFile` reader.
- `config.<app>` in `substrate.yaml`: the key equal to the manifest's `app` is accepted next to catalogue modules, so an app's defaults live in the manifest. Any other unknown key is still an error.
- Boot hooks: `boot.StartWith(ctx, name, modules, configJSON, boot.Options{...})` (`boot.Start` is unchanged). `AfterConfig` runs once, with the `*module.App`, right after the `config` module applied the `SUBSTRATE_CONFIG` override and before any later module starts (with no `config` module selected it runs once before the first module). `Before` maps a module name to a hook run immediately before that module's `Start`. A `Before` name not in the start list is an error before anything starts; a hook error stops the boot and stops the started modules in reverse order. Additive, not a breaking change. It replaces the `main`-side config rewrite and pre-migration guard workaround (`docs/adopting-substrate.md` sections 3 and 4). `module.Runtime.StartWith` and `module.Hooks` underlie it.
- `identity` module: an app that selects it is its own identity provider. First-run owner setup, users, password sign-in and sign-out, password change, profile, session list and revoke (session cookie with a same-origin and header check on writes); API keys with scopes and audiences; pairing codes another app redeems for a key; `POST /api/token` (any credential for a short-lived EdDSA JWT, Tether-mint-shaped, so a client sets `auth.mint_url` at it); a published key set at `/api/identity/jwks` so another app can trust this one. Accepted by the app's guard in any mode through `auth.AddVerifier`; `enforce` needs no other verifier. Docs: `docs/identity.md`. Strict-mode proofs (passkeys, signed approvals) are not included. Needs `golang.org/x/crypto` (BOM third_party).
- `ui.Public` / scope `"public"`: a route that skips authentication (setup, sign-in, the key set, pairing). The auth module opens exactly the declared public paths.

- SF-2 vocabulary v0.4 (additive; contribution schema_version stays 0): kinds `menu.item` and `command`; core regions `command.palette`, `menu:<subject-kind>`, `row.actions`, `toolbar.primary`; a shared `action` reference (`navigate`, `openRegion` with optional `tab`, or `call` with optional `input`); `risk`, `order`, `when`, `keywords` and `group` fields. The builder refuses a navigate that names no surface route or a backend path, an openRegion that is undeclared or not docked, a tab that is not in that region, a `when` outside a menu region, a command outside the palette, a menu.item in the palette or in a docked region, and a duplicate menu.item key in one region. Menu items sort by `order` (default 100), then module, then key.
- Dock attributes in the region catalog (`frontend.regions`): `open_state`, `exclusive`, `sizes`, `dismiss_key`, `mobile`, `edge`, `height_px`, validated, on docked regions only.
- The frontend manifest carries `bindings` (`{kind, version}`) derived from modules' `envelopes.emits` (`kind@version`).

### Fixed

- The generated frontend manifest wrote each region as `{"Name","Docked","Anchor"}`; it is now `{"name","docked","anchor"}` as the shell reads it, and a test fixes the key names.

- `mcp.UseToolFilter`: a per-caller visibility filter for `tools/list`: one call per request with every tool name, returning the names to list (the caller's principal is on the context for HTTP and stdio). Visibility only; a hidden tool is still callable, so handlers authorize with `mcp.Authorize`. For apps whose tool set differs per caller or changes while serving.

- BOM `app_libs`: `libs/mesh/core v0.6.0` (the Tether client, for apps that launch agent sessions).

- `auth` plumbing from libs `go-auth`: `denylist_url` polls the issuer's revocation list so a revoked jti, principal or session stops working within the poll interval; `token_file` (a non-expiring service token, re-read on use) or `mint_url` (a refreshed short-lived token) give an app, or its CLI, an outbound credential via `auth.Token`, `auth.Transport` and `auth.HTTPClient`, with an optional host allowlist so the token cannot leak to other hosts. Nothing is attached when neither is set (the dev default).
- Host region catalog (`frontend.regions` in `substrate.yaml`, from the design team): host-defined regions (`chat.composer`, `chat.drawer`, ...) are declared once per app, docked regions name their `anchor`, and the generated frontend manifest carries the catalog as `regions`. The builder fails on an undeclared region for a `dock.tab` or `slot`, a `dock.tab` outside a docked region, a `slot` inside one, and a `slot.opens` that names a tab outside the drawer anchored under the slot's region.
- `pins check --npm` also verifies the kits' own dependency and peer ranges against the BOM's pins: two installed versions of a binding contract or token set is the failure it prevents. Known incoherences the repo does not control are listed in the BOM's `kit_exceptions` and reported as accepted, not hidden. The first run found that the previous kit pins were not coherent (kit-admin needed an unpinned kit-observe; kit-settings and plugin-host-ui 0.2.0 depended on design tokens 0.4).
- BOM kits moved to a coherent set for `@hollis-labs/app-shell` 0.1.0: plugin-host-ui 0.3.0 (the `dock.tab` rename), design-bindings 0.2.0, kit-cards 0.2.0, kit-observe 0.1.2, app-shell 0.1.0. `kit-chat` is unpinned until a release that follows bindings and cards 0.2. kit-settings 0.2.0 still depends on tokens and components 0.4 and is an accepted exception.
- BOM `app_libs`: libs an app may import directly that Substrate does not itself require (the workflow engine first). `substrate pins check|sync --app <dir>` holds an app's `go.mod` to the BOM of the Substrate release it requires: a pinned module at another version is drift, and a libs module the app requires directly that the BOM does not pin is an error (add it to `app_libs` or drop the import); libs that arrive only as indirect requirements follow from the pinned ones. This is what keeps an app from drifting onto older libs, as the old Hadron did.
- `database.UseMigrationFS(fsys, root)`: register embedded migrations without importing libs.
- `scheduler.UseStoreFunc` and `scheduler.UseOptionsFunc`: an app that keeps schedules in its own tables supplies its libs `Store` lazily, built over the database module's connection after it started, plus engine options (an observer, a claim lease) that win over the config defaults. Settled the Hadron activation spike: its activation tables already implement the libs scheduler `Store`, so the framework module runs its engine with no adapter code.
- `queue` and `scheduler` take `autostart: false` and expose `queue.Run(app)` and `scheduler.Run(app)`. An app whose one binary is both a CLI and a daemon sets it, and only its `serve` command starts the worker and the engine, so a short-lived command (`version`, `health`, `mcp`) never runs jobs or fires schedules.
- `httphost.Serve`: the one standard way to listen. Address from config `httphost.addr` (default `127.0.0.1:8080`), bind validated against the auth mode (off and permissive refuse a public address), header and idle timeouts, no write timeout so SSE is not cut off, and graceful shutdown when the context ends so in-flight requests finish. The examples use it; no app writes its own listener code, and no `ADDR` environment variable.
- Fixed: a `SUBSTRATE_CONFIG` override with a manifest that has no `config:` block panicked (the generated config text is `null`, which decoded to a nil map). `boot.Start` now always has a map.
- `httphost.Options.Mounts`: app handlers outside `/api/` for paths a protocol fixes (an A2A endpoint, `/.well-known/...`). Guarded by the auth module and given the flat error body unless `Open` is set for public discovery; a pattern that would shadow `/api/`, `/healthz`, `/mcp` or the SPA, or conflict with another, is an error.
- `mcp.ServeStdio`: the same MCP server and tools over stdio, for an agent that launches the app as a subprocess. The caller is identified once from a token file (`mcp.stdio_token_file` or `StdioOptions.TokenFile`), verified by the auth module, and stamped on every tool call, so per-tool scopes behave exactly as over HTTP; enforce mode requires a token holding the `/mcp` scope, and the token and its path never appear in errors. `auth` publishes its verifier (`auth.verifier`, `auth.Verify`) for this.
- `queue` module over libs `core/util/queue`: handlers registered with `queue.Handle`, a worker started and stopped with the app, durable SQLite driver on the database module (or `memory`, or `queue.UseDriver`, which Postgres needs because libs ships no Postgres driver), retries up to `max_tries`, then the failed store.
- `scheduler` module over libs `core/util/scheduler`: durable schedules (cron, interval, one-time) with leased claims, retries and overlap policy, on libs' reference SQLite store (or `scheduler.UseStore`). A fired job is pushed to the `queue` module by default, so scheduler, queue and handler compose without glue; `scheduler.UseRunner` replaces the dispatch.
- `router` module over libs `core/util/router`: a rule set from `rules_file` or `router.UseRules`, compiled at start; `router.Route` returns a ledger-shaped decision with a real confidence gate. Policy, escalation and snapshot hooks go in `router.UseConfig`.
- CI runs a Postgres service (`ci.postgres_image` in the BOM) so the database module's Postgres path is tested for real.

- UI contribution manifest (`ui/`, v0): a module that implements `module.UIContributor` returns an embedded `ui.json` (contributions, envelopes, routes), validated against the design team's approved vocabulary v0.3 (`nav.item`, `nav.group`, `settings.region`, `surface`, `panel`, `dock.tab`, `widget`, `slot`). `settings.region` references admin-manifest groups and never defines them; frontend routes may not equal a backend path; the generated manifest checks nav groups, slot targets and route clashes across modules. Package versions are never written there; `package.version_ref` points into the BOM.
- Declared routes: `module.AddRoutes` binds each route in `ui.json` to a handler and a scope (`{app}`, `authenticated`, or the module's `scope` config), and httphost mounts and scope-maps them, so a route cannot be left unmapped. Paths outside `/api/` (such as `/admin/...`) are mounted exactly under the same guard. `capabilities` now uses it.
- `GET /api/me` (auth): the caller's principal and the guard mode, for any verified caller.
- `apierr`: one flat error body `{code, message, details?}` for everything Substrate serves; httphost rewrites the auth guard's nested body to it, because the app-runtime client reads `message` at the top level.
- `events` module: replayable SSE at `GET /api/events?topic=` over libs `streamhub` and `ssekit`, with Last-Event-ID resume, an explicit `gap` frame, heartbeats, a per-app topic authorizer and `events.UseLog` for a durable log. Scope `<app>.events`.
- BOM `kits:` pins the npm packages the frontend may name (public registry, `@hollis-labs`, exact versions). `substrate pins check --npm` verifies each is published (nightly).
- `substrate frontend -f substrate.yaml [-o dir]` writes `frontend.manifest.json`: the merged contributions of exactly the modules the manifest selects (nav with group checks, settings, routes) and the BOM-pinned kits, with the base design kits added when anything contributes UI. Removing a module removes its UI. Envelope TS types and the binding-table input follow the S0 consolidation.
- `pluginhost` routes are declared in `ui.json` (any method, subtree `/api/plugins/`) with scope `<app>.plugins`; an app maps finer per-plugin scopes on the scope table and those win. `pluginhost.RoutesKey` is removed. Declared routes may now use method `*` and subtree paths.
- `substrate upgrade [-dir] [-to] [-dry-run] [-allow-unmigrated]` (`upgrade/`): reports the BOM pin changes and changelog between two releases, applies registered mechanical migrations, moves the one Substrate requirement, validates the manifest with the target release's CLI, builds, and restores `go.mod`, `go.sum` and migrated files on any failure. Refuses downgrades, local `replace`, unmigrated breaking changes and unreleased targets without `-allow-unmigrated`. `docs/compat-policy.md` defines what is covered, what breaks, and the `### Breaking` / `(migration: id|none)` changelog convention.
- App-level bump test (`e2e/`): an app that requires Substrate only resolves every BOM pin exactly, at v0.1.0 and at this tree. An end-to-end upgrade test moves a real app from v0.1.0 to the proxy's main.
- `docs/writing-a-module.md`.
- Conformance: every contribution validates, and every declared route is mapped and served in enforce mode.

## [0.1.0] - 2026-11-02

First tagged release.

### Added

- `agentic` profile is now `minimal` + capabilities, secrets, auth, mcp (httphost comes via mcp). `policy`, `sqlite` and `pluginhost` stay opt-in under `modules.add`. `examples/hello-agentic` registers one MCP tool; e2e tests assert the linked module sets of minimal vs agentic and call the tool over MCP.

- `pluginhost` module over libs app-host `plugins.Inventory`: the app registers its reviewed Specs with `pluginhost.UseSpecs`; plugins start all-or-nothing, stop in reverse, report a `plugins` health check, and httphost mounts `/api/plugins/{id}/...` under the auth guard (unmapped, so denied in enforce mode until the app maps scopes). Tested against a real subprocess plugin.

- `mcp` module over libs `mcp/plugin-mcp` go-mcp (v0.6.0): server created from config, Streamable HTTP mounted at `/mcp` by httphost, guarded by the auth module (scope `<app>.mcp`) regardless of module order; `mcp.Authorize` for per-tool scopes. Apps register tools on `mcp.Server(app)`. Requires httphost.

- `cli` module (cobra skeleton: `version`, `config` (redacted when secrets is started), `health`); the `cli` variant now starts it. `examples/hello-cli`. `health.Registry.Report` is shared by `/healthz` and the CLI.

- `secrets` module over libs `security/secrets` and `security/redact`: references resolved on demand, resolved values fed to a redaction scope, and the logging module's logger wrapped so they never reach log output. `env://` is off unless `secrets.env: true`. Requires `logging`.

- `auth` module over libs `security/auth` (mode dial, JWT and API-key verifiers, scope table, bind validation); httphost wraps `/api/` with the guard when started. Unmapped routes fail closed in enforce mode.
- `policy` module over libs `security/policy` (shadow by default, fail closed, slog audit). The app registers its PDP with `policy.UsePDP`; starting without one is an error. `policy.AllowAll` is an explicit dev PDP.

- `sqlite` module over libs `core/store` (goose migrations via `sqlite.UseMigrations`, health check, `schema_mode`).
- Lint cleanup: errcheck findings from the first CI run.

- Manifest schema (`substrate.yaml`), validator and module resolver.
- Builder CLI: `validate`, `modules`, `generate`, `build`.
- Module registry and the config, logging, health, httphost (over libs app-host) and capabilities modules.
- `hello-service` and `hello-app-host` examples; end-to-end flag test (minimal vs agentic).
- `bom/bom.yaml` pin scaffold, CI workflow, `scripts/scan-public`.
- `build.yml`: pushes to main cross-build the `substrate` CLI (linux/amd64, darwin/arm64, CGO off) on build-1 after vet and test, and attach tarballs with a sha256 file to the run. No tag or release is created.
- `GET /api/capabilities` is mapped to a scope when auth is started (config `capabilities.scope`, default `<app>.read`), so enforce mode no longer fails it closed as unmapped.
- Conformance suite (`conformance/`): catalogue consistency, every module and every profile under every variant start and stop cleanly (including restart), plus runtime ordering tests in `module/`. Runs in CI with `go test ./...`.
- BOM is the source of every pin: `toolchain.go` becomes `go_floor` (portfolio floor) plus `go_min` (Substrate's minimum); Folio's `platform-pins.yaml` mirrors `go_floor` and `ci`; `substrate pins sync|check` writes and verifies go.mod, the CI workflow and the Folio mirror from it (replaces the earlier bump tests and drift scripts).
