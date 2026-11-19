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
- Long-lived handlers (SSE, WebSocket, long poll) must watch `httphost.Stopping(r)`, a channel closed when `Serve`'s
  context ends. `http.Server.Shutdown` cannot end a stream and does not track a hijacked connection, so without this
  a handler holds `Serve` for `GracePeriod` (default 10 s), after which `r.Context()` is cancelled, connections are
  closed and `Serve` returns an error. `Serve` returns nil only after every handler, hijacked ones too, has returned.
  The pattern: `select { case <-httphost.Stopping(r): /* send a close frame or final event, return */ case
  <-r.Context().Done(): /* client left */ case ev := <-events: ... }`. It reaches the handler through the host check,
  identity, the guard and `apierr.Normalize` (it rides the request context). `r.Context()` is deliberately not
  cancelled when a graceful stop begins, so a request doing real work (a database call) finishes; only the grace
  expiry cancels it. Outside `Serve` (httptest) `Stopping` is a nil channel and never fires. An app's own background
  goroutines started from a handler (a worker the request kicked off) should watch the app context, not
  `Stopping(r)`, which belongs to the request's own connection.

## 4. Persistence

- Data policy first (section 0). The notes below on a startup guard apply to fresh-start apps. An app that migrates
  data ships a tested one-way migration instead: backup, rehearsal on a copy of the real data, row counts and
  integrity checks before and after, and a documented rollback.
- Use the `database` module's store (`database.From(app)`), not your own `sql.Open`. SQLite and Postgres are both
  supported by the module; if your SQL uses `?` placeholders and SQLite syntax, say in the architecture note that the
  app is SQLite only. Identity and other modules use `Dialect().Rebind` and work on both.
- Local store plus a data store. This is not a step of every port, and no port needs it to cut over. Whether a port uses a
  second store (its class B and C data on Postgres) is its director's ruling; it comes after adoption, with the Postgres
  switch. When an app does move its growing data to Postgres but must still start and be repairable without that server
  (settings, definitions and identity local; events, logs, domain tables on Postgres), keep the default store local and add
  a named one: `database.stores.data` with `driver: postgres`, `required: false`, `schema_mode: verify`,
  `postgres: {dsn_file: ..., migrate_dsn_file: ...}`, and `database.UseNamedMigrationFS("data", fsys, root)`. In code take
  `h := database.Named(app, "data")`; `h.Get()` returns the store or an error wrapping `database.ErrUnavailable`. A handler
  maps that, and any connection-level error (`database.IsUnavailable(err)`, which it also passes to `h.Report(err)` so the
  state flips at once), to `database.WriteUnavailable(w, h)` (503 `database_unavailable`), or wraps a route group in
  `database.Gate(h)` (`GateWith(h, deny)` keeps a protocol-fixed API's own error envelope). Both panic at construction on a
  nil handle: `database.Named` returns nil for a name that is not configured, and that typo should not surface as a route that
  is quietly always 503. `h.OnChange(fn)` re-arms work that depends on the store after an outage; `h.Wait(ctx)` waits for
  the first time it is ready.
  While an optional store is down the app is up and `/healthz` says `degraded` with HTTP 200 (naming the store and its host
  and database, never credentials; a 503 would make a deploy gate roll the app back or a supervisor restart it), writes to it
  are refused (nothing is queued or spooled), and the module reconnects by itself with backoff. A missing secret file or an
  unset variable is an outage; a secret file that cannot be read (permission, a directory, empty) fails `Start` and names the
  path. A store whose error will not fix itself (a rejected login, a missing database, a schema that is behind) shows as
  `failed` (an optional store; a required one fails `Start`). Producers that must not block (events, telemetry) batch in memory with a bound and drop the oldest, counting
  drops; they do not persist locally. Each named store has its own version table (`goose_db_version_<name>`), so two stores
  can share one database.
  Migrations: a Postgres named store defaults to `schema_mode: verify`, which never reads `migrate_dsn_*`, so the running app
  needs no schema-owner secret and does not migrate a shared server. The deploy step migrates with the schema-owner role:
  `<app> db migrate --store data` (the database module adds the `db` verb when the app has named stores and the cli module is
  selected; it is `database.MigrateNamed(ctx, app, name)` underneath) with `migrate_dsn_file` or `migrate_dsn_env` set. If an
  optional store's schema is behind at start, the store is `failed` and recovers by itself once the deploy step has migrated
  it. A required store with the schema behind fails `Start` (it is required), which is why `db migrate` does not open the
  named stores at boot: run it before the app starts, including on a store's first deploy.
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
  - Starting the same app twice at once on one new SQLite file is safe: the `database` module opens and migrates
    under an exclusive advisory lock (`flock`) on `<db path>.open.lock`, so the second process waits, then finds the
    schema current. The kernel drops the lock when its holder exits, so a crash leaves nothing stale (the leftover
    file is harmless and is not removed). A wait longer than `database.sqlite.open_lock_timeout` (default `30s`) fails
    the start with an error naming the lock file. Limits: the lock covers only processes that start through the
    `database` module (a tool that opens the file itself, such as the `sqlite3` shell, is not serialised); it is not
    taken for `:memory:` or Postgres; on a non-unix build it is a no-op; and flock on a network file system is only as
    good as that file system.
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
   **First-run rule: never open setup over HTTP on a host where agents run; loopback is not a boundary.** The module
   closes it by default (`identity.setup: offline`: `POST /api/setup` is always refused). Create the owner and the first
   keys offline with the commands the cli module adds (select `cli` next to `identity`; see `docs/identity.md`, Offline
   bootstrap). The sequence is: migrate-data (the app's migration, which creates the database), then bootstrap
   (`<app> identity setup --username ...` with the password on stdin, then `<app> identity key create --name ... --scope ...
   --out FILE` for each machine caller), then start the service, and check `GET /api/setup` answers
   `setup_required: false`. Script all of it; the commands never prompt when stdin is not a terminal. To keep the old
   HTTP first-run for local development only, set `identity: {setup: http}`.
2. Wrap the final handler: `identity.From(app).Middleware(handler)`. Browser writes then need an `X-Requested-With`
   header and a same-origin `Origin`.
3. Declare each route's scope. Either list routes in a module-style `ui.json` and bind handlers with
   `module.AddRoutes`, or register them on the auth module's `ScopeTable` (`auth.ScopeTable(app).Route(method,
   pattern, scope)`). A route without a scope is refused in `enforce`. Name scopes `<app>.<verb>`; use `authenticated`
   for any verified caller and `public` for the few routes that must be reachable without credentials. Add a test that
   every registered route has a scope (and break one on purpose to see it fail).
4. Routes that authenticate their own callers (a native device protocol, a webhook, an A2A endpoint) are
   `httphost.Mount`s with `Open: true`. Their handlers must verify the credential themselves; say so in the code.
   **Relaying a third-party protocol.** Every route and mount is wrapped in `apierr.Normalize`, which rewrites a
   4xx/5xx body `{"error":{"code","message"}}` into the flat `{code, message}`. That is right for the app's own API
   and wrong for a handler that relays an upstream whose error shape clients depend on (an OpenAI-dialect gateway:
   `{"error":{"message","type","param","code"}}`, read as `error.message` by the OpenAI SDKs). Declare that path once:
   `httphost.Options{RawPrefixes: []string{"/api/llm-gateway/v1/"}}` for routes under `/api/` (each prefix starts with
   `/api/`, ends with `/`, is not `/api/` itself, appears once and must not overlap a module-declared route; a bad
   one is an error from `Handler` naming it), or `Mount{Raw: true}` for a mount outside `/api/`. The handler's
   response (status, headers, body, streams) then passes untouched. The guard's and the scope table's refusals,
   `route_not_mapped` included, still answer flat, so a client sees the upstream dialect for upstream errors and the
   framework shape for framework refusals. Do not use it for the app's own API: the frontend reads the flat shape.
   **Unknown paths without a UI.** In an app-host a path no route or mount matches loads the SPA (the placeholder page
   when there are no assets), 200 HTML. A service (`variant: service`, config `httphost.fallback: not_found`, which the
   builder writes) answers it 404 and a wrong method 405, flat. A service whose clients expect its own error envelope
   sets `httphost.Options{NotFound: h}`: it then answers every unmatched path outside `/api/`, raw and unguarded (so
   it must never serve data), and a wrong method on a method-specific mount with `Allow` set (answer 405 when it is). A wrong
   method other than GET or HEAD on a method-specific path is 405, never the SPA, in either case.
5. The data owner of a row is the verified principal subject from the request context, never a request field.
6. Keep stored secrets behind the `secrets` module's resolver. Never return a secret, a secret reference, a hash or a
   session value from any API.
7. DNS rebinding and proxies: serve on loopback, check the `Host` header against loopback plus a configured list, and
   set identity's `trusted_proxy` only for a local TLS proxy that overwrites `X-Forwarded-Proto`. `httphost.Harden`
   does the Host check (`httphost: {allowed_hosts: [...]}`, names served to any peer, or `check_host: true` for
   loopback only; never on a unix-socket listener, whose caller check is peer credentials), an Origin check on
   writes, a body limit, panic recovery and a credential-free access log (query parameter names only). `Handler`
   applies it when one of those keys is configured or `Options.Harden` is set; a custom handler wraps itself with
   `httphost.Harden(app, h, opts)`. Under Harden, `X-Forwarded-Proto` survives only under identity's `trusted_proxy`
   and from a loopback peer (read it with `httphost.Scheme(r)`), and the other forwarding headers are removed.
8. Strict-mode proofs (passkeys, signed approvals) are not part of this path. Do not port them; record the follow-up.
9. Local trust for a stdio MCP process (owner decision 14): enforced identity stays on HTTP; a stdio process the
   user launched locally against the app's own data file may run without a token. The app opts in in Go, at the
   call site: `mcp.ServeStdio(ctx, app, mcp.StdioOptions{LocalTrust: true})`. There is no config key and the
   default is off, so a manifest or override file cannot enable it. With no token configured the tools run under
   `local:operator` (kind `operator`, verified by filesystem trust, scopes `*`; narrow with `LocalScopes`, and
   per-tool `mcp.Authorize` then refuses what the list lacks; in enforce mode the list must cover `mcp.scope`, as a
   token must, or `ServeStdio` refuses to start). A token that is given is verified as always and a
   bad one is an error, never a fallback. The trust statement, verbatim: the process runs as a user who can open
   the data file; the module cannot tell who launched it and checks nothing about the file or the parent. A
   child launched by a gateway (Tether) on the same host as the same user counts as local. A gateway that serves
   several callers through one child must authorize per caller, or give the child a token (`mcp.stdio_token_file`),
   because every caller of a local child is the operator. The process writes one line to stderr naming the local
   principal; audit events carry `principal_id` `local:operator`, which no identity-issued id can equal. Apps that
   key a cache on a verified principal see the local principal as verified.

   **An app with a scope that must never be held by an agent on the same host (Tangent has one: approving a human-in-the-loop item) MUST pass `LocalScopes` that leave it out: under `LocalTrust` the default scopes are `*`, and local trust is the trust of anyone who can run the binary as that user, which on our hosts includes agents.**
10. An app that issues its own JWTs from a rotating key ring (agent tokens) verifies them through the auth module,
    not a second private verifier: `auth.UseKeySource` (the ring's public keys as JWKS, read in process) and
    `auth.UseDenyList` (its in-process `*libauth.DenyList`) before `boot.Start`, and `auth.RefreshKeys(ctx, app)`
    after a rotation. Do not point `auth.jwks_url` at the app itself. They combine with the identity module's tokens;
    see `docs/identity.md`, An issuer that verifies its own tokens.
11. One issuer per database. The identity module creates its signing key at start only when the database holds no
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

The frontend reads the flat `{code, message}` error body, which is why `httphost` flattens every error by default.
A path that relays a third-party protocol opts out with `RawPrefixes` or `Mount.Raw` (section 5, step 4); the UI
must not call such a path expecting the flat shape.

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
  `go test` so the embed has assets) and `go run github.com/hollis-labs/substrate/cmd/substrate scan-public --ref HEAD`.
  Copy the workflow from a ported app.
- Do not copy `scripts/scan-public` into the app: `substrate scan-public` carries it (one source, pinned with the
  Substrate version in `go.mod`). It runs the embedded script with `python3 -I` (Python 3 must be on `PATH`; GitHub
  runners have it) and takes the script's flags: `--ref REV` (repeatable), `--repo DIR` (default the current
  directory), `--tree`, `--history`, `--no-gitleaks`, `--allowlist FILE`, `--no-allowlist`, `--source`. Exit 0 clean,
  1 findings, 2 the scanner could not run. The app keeps only its own allowlists at its repository root:
  `scan-public.allow` and `scan-public.allow.d/*.allow`, lines `RULE-ID PATH-GLOB REASON`; allowlisted findings are
  still printed and counted. gitleaks runs too when it is on `PATH`. To move an app over: change the CI line, delete
  `scripts/scan-public`, keep the allow files, run both once on the same ref and compare. (`pins sync` does not
  template this line: it rewrites version pins inside the workflow, not steps, so the line is edited by hand.)
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
6. **Verify live**: sign-in (the owner already exists from the offline bootstrap; `GET /api/setup` says `setup_required: false`), one write, one read through the real proxy URL, and an unknown `Host` refused.

## 12. Evidence before you open the PR

Run and report the actual output of: `go vet ./...`, `go test -race ./...`, `golangci-lint run`, `pins check --app .`,
frontend typecheck/lint/build, `substrate scan-public --ref HEAD`. Then run the real binary on a throwaway data directory and
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
