# Adopting Substrate

How to move an existing Go app onto Substrate. It was written from the first two ports (Hadron, a workflow engine
host; Basalisk, a single-owner personal app) and is meant to be corrected by the third. If a step here is wrong or
missing for your app, fix this file in the same change.

Substrate supplies mechanism: config, logging, health, HTTP host, database and migrations, auth and identity,
secrets, policy, MCP, scheduler, events. Your app keeps its own engine, data, policy and process. Nothing in
Substrate owns your domain, and nothing in your app should copy what a module already does.

## 0. Decide before you write code

Answer these first; most of the surprises in a port were one of them.

| Question | Why it matters |
|---|---|
| Migrated data or fresh start? | The clean break is about code and compatibility (no shims, no fallbacks), not about data. An app with real usage or data migrates all of it; loss is not acceptable. An app nobody uses starts with a fresh schema and the old data moved aside. Decide per app before coding; an in-use app has a cutover plan of its own (section 11). |
| What is the app's binary? | `variant` is `app-host` (serves HTTP and a UI), `service`, `headless`, `cli` or `microservice`. Most apps are `app-host`. |
| Who issues identity? | Own accounts (the `identity` module), trust another issuer (`auth.jwks_url`), fixed service keys, or nothing for local use. Per app; see `docs/identity.md`. |
| Which routes are public, which need a scope, which authenticate themselves? | Every `/api/` route needs a scope. A route that authenticates its own callers (a device protocol, a webhook) is an open mount. |
| Is there background work that must drain on shutdown? | Workers, schedulers, pollers. Decide who owns them (a module or your code) and the stop order. |
| What does the UI need from the framework? | Nav items and surfaces come from module `ui.json` files and the frontend manifest; the app's own nav items and surfaces go in a file named by `frontend.contributions` (section 8). |
| Does the app depend on a module or a lib that is not in the BOM? | Add the pin to `bom/bom.yaml` in Substrate first (a small PR); the app's `pins check` fails until you do. |

Write the answers into the app's `docs/architecture.md` as you go. Reviewers read that file first.

## 1. Set up the branch and the reference

Work on a branch of the same repository. Keep the old tree for reference outside the working copy (a second clone
named `<app>-previous` worked well). Do not copy the old code in wholesale: move code that survives, delete
everything the framework replaces, and port every test that is not about deleted code. The tests are the parity
evidence.

## 2. The manifest and the wiring file

Put `substrate.yaml` beside `main.go` (`cmd/<app>/`):

```yaml
app: myapp
variant: app-host
profiles: [minimal]            # minimal: config, logging, health. agentic adds capabilities, secrets, auth, mcp.
modules:
  add: [httphost, secrets, auth, database, identity, cli]
config:
  logging: { level: info, format: text }
  httphost: { addr: "127.0.0.1:8085" }
  database: { sqlite: { path: myapp.db } }
  auth: { mode: enforce }
```

```sh
go run github.com/hollis-labs/substrate/cmd/substrate modules  -f cmd/myapp/substrate.yaml   # the modules this manifest resolves to
go run github.com/hollis-labs/substrate/cmd/substrate validate -f cmd/myapp/substrate.yaml
go run github.com/hollis-labs/substrate/cmd/substrate generate -f cmd/myapp/substrate.yaml   # writes substrate_wiring.go
```

Commit the generated `substrate_wiring.go`; `go build` must work without the substrate CLI. `config:` accepts blocks for
catalogue modules and one block for the app itself, the key equal to the manifest's `app` (`myapp:`). Put the app's
defaults there; they reach the generated config like any module's, and the app reads them from `app.Config["myapp"]`
(the `config` module overlays the file named by `SUBSTRATE_CONFIG`). Keep a small `internal/settings` package that
decodes the block and refuses a bad value at start with a message that names the key. Any other key is an error.

## 3. The entry point

```go
run, err := boot.Start(ctx, substrateApp, substrateModules, substrateConfigJSON)
if err != nil { fmt.Fprintln(os.Stderr, "myapp:", err); os.Exit(1) }
defer func() { _ = run.Stop(context.Background()) }()
root := cli.Root(run.App)               // cobra root with version, config, health
root.AddCommand(serveCmd(run.App))      // serve is the default command
```

- Install hooks before `boot.Start`: `database.UseMigrationFS`, `policy.UsePDP`, `mcp.UseToolFilter`, and so on.
  A module that needs a hook and does not get one fails to start; it never defaults to allow.
- Work that needs the effective config, or must happen between two modules, uses the boot hooks:
  `boot.StartWith(ctx, app, modules, configJSON, boot.Options{AfterConfig: ..., Before: map[string]boot.Hook{...}})`
  (`boot.Start` is the same call without hooks). `AfterConfig` runs once, with the `*module.App`, right after the
  `config` module has applied the `SUBSTRATE_CONFIG` override and before any later module starts: set run-time
  values there. `Before["database"]` runs immediately before that module's `Start`. A hook that names a module
  that is not in the start list is an error at start (a typo cannot silently never run); a hook error stops the boot
  and stops the modules already started, in reverse order. If the `config` module is not selected there is no
  override to wait for, so `AfterConfig` runs once before the first module starts. Hooks run once per start.
- `serve` builds the handler with `httphost.Handler(app, httphost.Options{Assets, Routes, Mounts})` and runs it with
  `httphost.Serve(ctx, app, handler, ...)`, which also checks the bind address against the auth mode.
- Shut down on `SIGINT` and `SIGTERM` (`signal.NotifyContext`), stop accepting work, drain, then stop modules. Test
  the signal path with a real process; a daemon that ignores `SIGTERM` is killed by its supervisor mid-write.

## 4. Persistence

- Data policy first (section 0). The notes below on a startup guard apply to fresh-start apps. An app that migrates
  data ships a tested one-way migration instead: backup, rehearsal on a copy of the real data, row counts and
  integrity checks before and after, and a documented rollback.
- Use the `database` module's store (`database.From(app)`), not your own `sql.Open`. SQLite and Postgres are both
  supported by the module; if your SQL uses `?` placeholders and SQLite syntax, say in the architecture note that the
  app is SQLite only. Identity and other modules use `Dialect().Rebind` and work on both.
- The store contract. `database.From(app)` is libs `core/store` (`*store.Store`). On SQLite it has ONE write
  connection (`DB()`, `WriteTx`, `ExecContext`; pool size forced to 1, `BEGIN IMMEDIATE`) and a separate read-only pool
  (`Reader()`, `QueryContext`, `QueryRowContext`, `ReadTx`; opened `mode=ro`, 4 connections by default, set with
  `database.sqlite.read_pool` (1 to 64), a write through it fails with "attempt to write a readonly database"). The
  write side stays one connection; a bigger read pool lets more cursors be open at once, it does not add writers. Rules that follow:
  - A second statement issued on the write handle while a cursor (`*sql.Rows`) is open on it waits for the one
    connection forever, a deadlock. It bites as a hung test, and as a hung request in production. The same happens
    inside a `WriteTx` callback if it uses anything but the `tx` it was given (the store itself, `DB()`, the read
    pool): libs says the callback may use only its `tx`.
  - Reads go through `Reader()`. Writes go through `WriteTx` (use `ExecContext` only for a single statement).
  - Read the rows you need, close the cursor (or finish the loop), then write. Never write from inside a loop over
    rows you are still reading, and give `WriteTx` callbacks no side effects other than the `tx`: they are retried
    from scratch on `SQLITE_BUSY`.
  - SQLite in memory (`:memory:`) uses a single shared connection for reads and writes, so there even a `Reader()`
    query inside an open cursor waits. Tests on `:memory:` hang sooner, not differently.
  - Postgres has one pool (`Reader()` and `DB()` share it), so the deadlock is SQLite's. Keep the same discipline
    anyway: the code has to run on both.

  ```go
  st := database.From(app)

  // Read first, on the read pool, and close the cursor before writing.
  rows, err := st.Reader().QueryContext(ctx, `SELECT id FROM jobs WHERE state = ?`, "stale")
  if err != nil { return err }
  var ids []string
  for rows.Next() {
      var id string
      if err := rows.Scan(&id); err != nil { _ = rows.Close(); return err }
      ids = append(ids, id)
  }
  if err := rows.Close(); err != nil { return err }

  // Then write, in one transaction, using only the tx.
  return st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
      for _, id := range ids {
          if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ? WHERE id = ?`, "retry", id); err != nil {
              return err
          }
      }
      return nil
  })
  ```

  Wrong, and hangs on SQLite: `rows, _ := st.DB().QueryContext(...)` and then `st.DB().ExecContext(...)` or
  `tx.ExecContext(...)` for each row, while `rows` is still open. (Checked against libs `core/store` v0.1.0 and
  `core/util` v0.6.0 `sqlitekit`: the write pool is `MaxOpenConns=1`.)
- One goose migration set, embedded, registered with `database.UseMigrationFS(fsys, root)`. A fresh schema edited in
  place until the first release is fine. Do not carry per-version `ALTER` ladders from the old code in a clean break.
- Libraries that own their tables (for example `libs/security/pairing`) keep creating them; do not duplicate their DDL.
- A clean break must not open an old database silently. Add a startup guard that refuses a data directory with the
  old layout and says what to do ("move the old data directory aside"). It has to run before the database module
  migrates: key it as `boot.Options.Before["database"]` and read the database path from `app.Config`, which already
  carries the override, so the guard does not repeat the config merge rule.
- Data directories: the framework has no data-directory concept yet (a decision after the first wave of ports).
  Resolve the data directory (XDG or an override variable) in the `AfterConfig` hook and write absolute paths into
  `app.Config`; leave a path the operator's override already made absolute alone. Directories `0700`, secrets and
  databases `0600`.

## 5. Auth, identity and routes

1. Pick the trust mode (section 0). To issue identity yourself, add `identity` (and `database`, `auth`) and an empty
   `identity:` block. It provides `/api/setup`, `/api/login`, `/api/logout`, `/api/password`, `/api/identity/*`,
   `/api/token`, `/api/pair` and `/api/identity/jwks`. Do not rebuild setup, login, sessions, passwords, API keys or
   pairing in the app.
2. Wrap the final handler: `identity.From(app).Middleware(handler)`. Browser writes then need an `X-Requested-With`
   header and a same-origin `Origin`.
3. Declare each route's scope. Either list routes in a module-style `ui.json` and bind handlers with
   `module.AddRoutes`, or register them on the auth module's `ScopeTable` (`auth.ScopeTable(app).Route(method,
   pattern, scope)`). A route without a scope is refused in `enforce`. Name scopes `<app>.<verb>`; use `authenticated`
   for any verified caller and `public` for the few routes that must be reachable without credentials. Add a test that
   every registered route has a scope (and break one on purpose to see it fail).
4. Routes that authenticate their own callers (a native device protocol, a webhook, an A2A endpoint) are
   `httphost.Mount`s with `Open: true`. Their handlers must verify the credential themselves; say so in the code.
5. The data owner of a row is the verified principal subject from the request context, never a request field.
6. Keep stored secrets behind the `secrets` module's resolver. Never return a secret, a secret reference, a hash or a
   session value from any API.
7. DNS rebinding and proxies: serve on loopback, check the `Host` header against loopback plus a configured list, and
   set identity's `trusted_proxy` only for a local TLS proxy that overwrites `X-Forwarded-Proto`.
8. Strict-mode proofs (passkeys, signed approvals) are not part of this path. Do not port them; record the follow-up.
9. Local trust for a stdio MCP process (owner decision 14): enforced identity stays on HTTP; a stdio process the
   user launched locally against the app's own data file may run without a token. The app opts in in Go, at the
   call site: `mcp.ServeStdio(ctx, app, mcp.StdioOptions{LocalTrust: true})`. There is no config key and the
   default is off, so a manifest or override file cannot enable it. With no token configured the tools run under
   `local:operator` (kind `operator`, verified by filesystem trust, scopes `*`; narrow with `LocalScopes`, and
   per-tool `mcp.Authorize` then refuses what the list lacks). A token that is given is verified as always and a
   bad one is an error, never a fallback. The trust statement, verbatim: the process runs as a user who can open
   the data file; the module cannot tell who launched it and checks nothing about the file or the parent. A
   child launched by a gateway (Tether) on the same host as the same user counts as local. A gateway that serves
   several callers through one child must authorize per caller, or give the child a token (`mcp.stdio_token_file`),
   because every caller of a local child is the operator. The process writes one line to stderr naming the local
   principal; audit events carry `principal_id` `local:operator`, which no identity-issued id can equal. Apps that
   key a cache on a verified principal see the local principal as verified.
10. One issuer per database. The identity module creates its signing key at start only when the database holds no
    users or API keys; with identity rows and no key file at `identity.key_file` it refuses to start and names the
    path. A child process pointed at the service's database (an explicit `-db`) must point `key_file` at the
    service's key. `identity.allow_new_signing_key: true` is the deliberate override for a lost key.

## 6. Configuration

Defaults live in the manifest and in `internal/settings`; operators override any key in the YAML file named by
`SUBSTRATE_CONFIG`. The merge is deep: maps merge key by key, so an override of only `database: {sqlite: {path: /data/app.db}}` changes the path and keeps the manifest's other `sqlite` keys; scalars and lists replace the manifest value whole; a key only in the override is added; an explicit `null` removes the key so the module's own default applies; a map over a scalar (or the reverse) replaces it. The rules are in the `config` package comment.
Map every old environment variable to a config key (or drop it on purpose) and write the table in
`docs/conversion-notes.md`. Do not read `os.Getenv` for settings in new code, except the data-directory override.

## 7. Background work, MCP, policy

- A long-running worker stops on context cancellation and is joined before `Stop` returns. Prefer the scheduler,
  queue or events modules when the work is a schedule, a job queue or a fan-out; keep plain goroutines for work that
  is intrinsic to the app, and drain them.
- MCP: select the `mcp` module, register typed tools, and filter the visible tools per caller with
  `mcp.UseToolFilter`.
- Policy: if access is a decision, use the `policy` module with your PDP in `shadow` first (audit would-be denials
  and allow), then `enforce` for release.

## 8. The frontend

- The UI is built on app-shell and the BOM-pinned kits (`bom/bom.yaml` `kits:`, exact versions, no `^`). Run
  `substrate frontend -f cmd/myapp/substrate.yaml` to generate the frontend manifest and use the pinned versions.
- Serve the built SPA with `httphost.Options{Assets: fsys}` from an `internal/webui` package that embeds
  `all:dist` (keep `dist/.gitkeep` so an unbuilt tree still compiles).
- The app's own UI goes in a contribution file named in the manifest (`frontend: {contributions: ui/app.json}`, a
  path relative to the manifest). It has the shape of a module `ui.json` (`schema_version`, `module`, `contributions`,
  `package`, `envelopes`) with `module` equal to the manifest's `app`, and `substrate frontend` validates it against
  the same vocabulary and merges it with the modules' items under the same rules (nav groups exist, surface routes
  are unique and not backend paths, regions are declared, actions resolve); its items carry the app name as their
  `module`, and its `package` and the base kits are pinned like a module's. `routes` are refused: an app mounts its
  routes through httphost, so there is nothing for the builder to wire, and only modules declare backend paths. The
  app name must not be a catalogue module name. Do not hand-merge a fragment in the SPA.
- Known limits today, each a framework task: the identity module has no UI, so setup, sign-in and password screens are
  app-owned until a shared kit exists; app-shell has no user-menu region (sign-out is a page); kit-settings still
  depends on the previous design-system version. Check the open Substrate tasks before building around one.
- Frontend work is a separate track from the backend port. Agree the API contract first (paths, the flat
  `{code, message, details?}` error body, the CSRF header) and build against it.

## 9. Pins, CI and hygiene

- Pins are edited only in `bom/bom.yaml` in Substrate. `go run github.com/hollis-labs/substrate/cmd/substrate pins
  check --app .` must pass in the app; `pins sync` rewrites `go.mod` and the CI workflow. A lib the BOM does not
  list is a one-line Substrate PR, not a local `go.mod` exception.
- CI: `go vet`, the pins check, `go test -race`, `golangci-lint`, the frontend typecheck/lint/build (built before
  `go test` so the embed has assets) and `python3 scripts/scan-public --ref HEAD`. Copy the workflow from a ported app.
- The repository is probably public. No secrets, internal hosts or addresses, or personal paths in files, commits or
  CI. Scan history as well as the tree; fixing history later is expensive.

## 10. Docs

- `README.md` short and true. `docs/architecture.md` (layout, auth model, scopes, data, config keys, what was
  dropped and why). `docs/conversion-notes.md` (gaps, old-to-new mapping of env, paths, HTTP, and what the frontend
  and operator must do). Verify every claim against the code; drop what you cannot confirm.
- `AGENTS.md` describes the Substrate shape and names the conventions a future agent must keep.

## 11. Cutover

For an app in use, the port is not done when the tests pass. Write the plan before the work and keep it with the
app's Torque task:

1. **Data**: an app with real data migrates all of it: a tested one-way migration with a backup and a rehearsal on a
   copy of production data. Only an app nobody uses starts clean; then the old data directory is moved aside, never
   deleted, and the owner decides when to delete it.
2. **Clients**: list every caller of the old API and protocol (other apps, a CLI, native clients, MCP clients, the
   UI). Each either keeps working unchanged or has a named change and an owner. Run each against the new build.
3. **Config and supervision**: the old environment variables and unit/descriptor entries that no longer mean anything
   are removed from the supervisor's descriptor. Hand that change to whoever owns the descriptor.
4. **Deploy**: build once in CI, install from GitHub (`fleet-install deploy <app> --commit <sha>` with
   `--dry-run` first), keep the previous version for rollback, health-check `/healthz`.
5. **Rollback**: the previous release is relinked and the moved-aside data is restored. State it before you start.
6. **Verify live**: setup or sign-in, one write, one read through the real proxy URL, and an unknown `Host` refused.

## 12. Evidence before you open the PR

Run and report the actual output of: `go vet ./...`, `go test -race ./...`, `golangci-lint run`, `pins check --app .`,
frontend typecheck/lint/build, `scan-public --ref HEAD`. Then run the real binary on a throwaway data directory and
exercise setup, sign-in, a write, a read, a refused request and `SIGTERM`. For an app with a UI, drive it in a real
browser against the real backend; a stub backend proves the UI only. Say plainly what you did not verify.

## What the first two ports taught

- Ask for the missing framework piece early and land it in Substrate as a small PR; do not work around it in the app
  and call it done.
- Anything that needs a human decision (an open route, a scope name, a dropped feature, a data cutover) goes in the
  app's `docs/conversion-notes.md` under "Gaps" the moment it appears.
- A feature the old app never served (an unmounted handler, a dead flag) is not a port target. Port the code, do not
  invent the route, and file a task.
- Mechanical clean-up (lint findings, import moves) is cheap to delegate; contract decisions are not.
