# Architecture: Substrate presets

Folio renders typed presets (YAML manifest plus text/template tree). Presets whose output is a Substrate app
sit beside the older ones; the engine (`internal/*`, `service/`, `cmd/folio`) changed in one place only: a
breadcrumb or a command line that names a removed preset fails with `preset_removed` (`service/removed.go`).

## Presets

```
go-baseline (layer)                  LICENSE, CHANGELOG, AGENTS.md, lint config, hooks (unchanged)
  substrate-baseline (layer)         the Go shell of a Substrate app-host; composes go-baseline
    substrate-app (preset)           backend only, no UI
    substrate-frontend (layer)       minimal app-shell frontend; composes substrate-baseline with with_frontend=yes
      app-dashboard 3.0.0 (preset)   the app with a UI (kit-dashboard theme, sign-in, one list surface)
```

Unchanged and out of scope: `base`, `go-lib`, `go-package`, `nanite-plugin`, `ts-plugin`. Removed: see
`conversion-notes.md`. There is no chat preset until a `kit-chat` release is pinned in the Substrate BOM.

### What `substrate-baseline` renders (modelled on Loom and Mentat)

- `cmd/<name>/substrate.yaml` (`variant: app-host`, modules `secrets, auth, database, identity, cli`,
  `auth.mode: enforce`, the identity key file, the app's own block) and its committed generated
  `substrate_wiring.go`. The wiring is templated (the app block's key order follows the app name) and the
  integration test proves it byte-identical to `substrate generate` for two app names.
- `cmd/<name>/config.go`: `plan` decides from the command line before anything boots. Only `serve` and the
  offline `identity setup` / `identity key create` start every module and create the data directory; help,
  version, a bare command, an unknown word and a bad flag start nothing that writes. A bare command prints help.
- `cmd/<name>/serve.go`: the handler stack (Host check, identity middleware, the host with guard, routes and SPA).
- `internal/api` (routes with scopes; the owner of a row is the verified principal), `internal/service` (the only
  code that runs SQL), `internal/settings`, `internal/hostguard` (a per-app copy of a check the framework is to own,
  Substrate pull request 23), `internal/webui` (embeds `all:dist`), `internal/storage` (one goose migration).
- `.golangci.transport.yml` and `docs/transport-boundary.md`: handlers may not import the storage package, the
  database module or `database/sql`; a second lint run in `check.yml` enforces it.
- `.github/workflows/check.yml` (vet, pins check, wiring current, race tests, both lint runs, scan-public) and
  `build.yml` (the tools reusable `go-app-build.yml`, pinned by commit), `deploy.yaml` (a host-alias placeholder),
  `docs/identity-bootstrap.md` (interactive owner setup), `.gitignore` with `/dist/`, `scan-public.allow`.
- `go.mod` carries only the Substrate requirement; `go mod tidy`, `substrate pins sync --app .`, `go mod tidy`
  complete it. No `go.sum` is rendered.

### What `substrate-frontend` renders

React 19, Vite, Tailwind, app-shell's `ManifestShell` over the manifest `substrate frontend` generates (`predev`,
`prebuild` and `pretypecheck` run it), an app-owned sign-in screen (identity has no UI kit yet; a copy of Loom's
with its fixes), one list surface over `GET`/`POST /api/items`, `cmd/<name>/ui/app.json`, the lint configs, a
`Makefile`. `npm install` once and commit `package-lock.json`: the rendered `check.yml` fails with that
instruction when the lock is missing. `identity.allowed_origins` for the dev server is documented for a
development override file only; no shipped file sets it.

### Inputs and versions

`repo_name`, `github_owner`, `description`, `go_version` (the portfolio floor, from `platform-pins.yaml`),
`http_port`, `build_workflow_ref`. Required with NO default: `substrate_version` and the seven `kit_*` versions
(a stale default is worse than a failed render); the integration tests read them from the framework's
`bom.yaml`. The Go floor and CI action versions are literals checked by the drift check. The frontend toolchain
literals are copied from Loom's `frontend/package.json` (Gap 5). Layers receive switches through compose `vars`
(`with_frontend`), not through `computed`, which does not reach a composed layer's templates.

## Tests

`integration_substrateapp_test.go`: render trees and refusals (no default for `substrate_version`, bad inputs,
required kits); `TestSubstrateApp_BuildsAndRuns` and `TestAppDashboard_BuildsAndServesUI` build against the real
framework (tidy, pins, validate, generate diff, vet, race tests, npm install, typecheck, lint, build, the
transport-gate controls) and run the binary on a temporary data directory (offline owner, sign-in, write, read,
refused requests, `SIGTERM`, an empty `HOME` stays empty). They need network, Go and Node and set
`FOLIO_SUBSTRATE_VERSION`; without it they skip visibly (`FOLIO_REQUIRE_TOOLS=1` makes that a failure, as in CI).
