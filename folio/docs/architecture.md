# Architecture: Substrate presets

Folio renders typed presets (YAML manifest plus text/template tree). The port adds presets whose output is a
Substrate app; the engine (`internal/*`, `service/`, `cmd/folio`) does not change except for what presets need.

## Preset design

```
go-baseline (layer, unchanged)       CI check, lint, hooks, LICENSE, CHANGELOG, AGENTS.md base
  substrate-baseline (layer, new)    backend shell of a Substrate app
    substrate-app (preset, new)      app-host: backend only, no UI
      substrate-frontend (layer)     app-shell frontend, ui/app.json, internal/webui embed
        app-dashboard 3.0 (preset)   substrate-app + frontend + dashboard surface
        chat-app 2.0 (preset)        substrate-app + frontend + chat surface (blocked, Gap 3)
```

### What `substrate-baseline` renders (modelled on Mentat and Loom main)

- `cmd/<name>/substrate.yaml`: `app`, `variant: app-host`, `profiles: [minimal]`, modules `secrets, auth, database,
  identity, cli`; config for logging, `httphost.addr` (default `127.0.0.1:8080`, overridden through
  `SUBSTRATE_CONFIG`), sqlite path, `auth.mode: enforce`, identity key file; the app's own block (key = app name).
- `cmd/<name>/substrate_wiring.go`: committed generator output. A test regenerates it with the pinned
  `substrate generate` and diffs, so it cannot drift.
- `cmd/<name>/main.go`: `signal.NotifyContext`, `boot.Start`, `cli.Root`, `serve`.
- `internal/settings` (decodes the app block, refuses bad values naming the key), `internal/webui` (embeds
  `all:dist`, `dist/.gitkeep`), `internal/storage/migrations/sqlite/00001_init.sql` registered with
  `database.UseMigrationFS`.
- Identity: owner created offline (`<name> identity setup`, `identity key create`); no open setup form
  (`identity.setup: offline`, the default). `docs/identity-bootstrap.md` has the scripted sequence.
- `go.mod` (go floor from `platform-pins.yaml`, Substrate pin from the BOM), `.golangci.yml` (v2.11.4),
  `.gitignore` with `/dist/`, `scan-public.allow`, `.github/workflows/{ci,build}.yml`, `deploy.yaml`,
  `docs/architecture.md`, `docs/conversion-notes.md` stubs, `AGENTS.md`.
- No secret-shaped literals, hosts or user paths in any template; `substrate scan-public --tree` on the rendered
  output is part of the integration test.

### What `substrate-frontend` renders

Minimal only: `frontend/package.json` with BOM-pinned exact versions (no `^`), `index.html`, `src/main.tsx`,
`src/App.tsx` on app-shell, `cmd/<name>/ui/app.json` (the contribution file named by
`frontend.contributions`), vite/tsconfig/biome/eslint configs, and the `manifest` script that runs
`substrate frontend`. A frontend agent reviews it before the pull request goes to the owner.

### Inputs

`project_name`, `github_owner`, `description`, `go_version` (from the pins file), `base_path`
(app-dashboard keeps it only if a Substrate app can serve a sub-path; to be checked, not assumed).
Removed: `gui_host`, `chimera_version`, `chimera_gui_recipe`, `design_kit_version`, `go_webui_version`.

### Versions: where they come from

Go floor and CI tool versions: `platform-pins.yaml` (mirror, written by `substrate pins sync --folio`).
Substrate version and kit versions: not in that mirror today (Gap 2); no second hand-kept list is added.

### Tests

Existing integration tests render, compile and assert. New: render `substrate-app`, `go mod tidy`, `substrate
validate`, `go vet`, `go build`, `go test`, `substrate pins check --app`, `scan-public --tree`, then start the
binary on a temporary data directory (port 18210 to 18219) and check offline identity bootstrap, sign-in, a
refused request, `GET /api/setup` says `setup_required: false`, and `SIGTERM`. Frontend: `npm ci`, typecheck,
lint and build of the rendered project.

## Slices

S0 these notes; S1 this design; S2 `substrate-baseline` + `substrate-app`; S3 `substrate-frontend` + the two
variants; S4 removal (Chimera, go-webui, `go-service-app`, fixtures, scripts, vendored licensed material, which
leaves the tree and stays in history); S5 README, CHANGELOG, AGENTS.md, `docs/cutover-plan.md`.
