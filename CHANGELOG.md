# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Semver from the first release; pre-1.0
minor releases may break APIs.

## [Unreleased]

### Added

- `agentic` profile is now `minimal` + capabilities, secrets, auth, mcp (httphost comes via mcp). `policy`, `sqlite` and `pluginhost` stay opt-in under `modules.add`. `examples/hello-agentic` registers one MCP tool; e2e tests assert the linked module sets of minimal vs agentic and call the tool over MCP.

- `pluginhost` module over libs app-host `plugins.Inventory`: the app registers its reviewed Specs with `pluginhost.UseSpecs`; plugins start all-or-nothing, stop in reverse, report a `plugins` health check, and httphost mounts `/api/plugins/{id}/...` under the auth guard (unmapped, so denied in enforce mode until the app maps scopes). Tested against a real subprocess plugin.

- `mcp` module over libs `mcp/plugin-mcp` go-mcp (v0.6.0): server created from config, Streamable HTTP mounted at `/mcp` by httphost, guarded by the auth module (scope `<app>.mcp`) regardless of module order; `mcp.Authorize` for per-tool scopes. Apps register tools on `mcp.Server(app)`. Requires httphost.

- `cli` module (cobra skeleton: `version`, `config` (redacted when secrets is started), `health`); the `cli` variant now starts it. `examples/hello-cli`. `health.Registry.Report` is shared by `/healthz` and the CLI.

- `secrets` module over libs `security/secrets` and `security/redact`: references resolved on demand, resolved values fed to a redaction scope, and the logging module's logger wrapped so they never reach log output. `env://` is off unless `secrets.env: true`. Requires `logging`.

- `auth` module over libs `security/auth` (mode dial, JWT and API-key verifiers, scope table, bind validation); httphost wraps `/api/` with the guard when started. Unmapped routes fail closed in enforce mode, including `/api/capabilities` until the app maps it.
- `policy` module over libs `security/policy` (shadow by default, fail closed, slog audit). The app registers its PDP with `policy.UsePDP`; starting without one is an error. `policy.AllowAll` is an explicit dev PDP.

- `sqlite` module over libs `core/store` (goose migrations via `sqlite.UseMigrations`, health check, `schema_mode`).
- Lint cleanup: errcheck findings from the first CI run.

- Manifest schema (`substrate.yaml`), validator and module resolver.
- Builder CLI: `validate`, `modules`, `generate`, `build`.
- Module registry and the config, logging, health, httphost (over libs app-host) and capabilities modules.
- `hello-service` and `hello-app-host` examples; end-to-end flag test (minimal vs agentic).
- `bom/bom.yaml` pin scaffold, CI workflow, `scripts/scan-public`.
- `GET /api/capabilities` is mapped to a scope when auth is started (config `capabilities.scope`, default `<app>.read`), so enforce mode no longer fails it closed as unmapped.
- Conformance suite (`conformance/`): catalogue consistency, every module and every profile under every variant start and stop cleanly (including restart), plus runtime ordering tests in `module/`. Bump tests assert the BOM agrees with `go.mod` and `ci.yml`. Runs in CI with `go test ./...`.
- BOM is the source of every pin: `toolchain.go` becomes `go_floor` (portfolio floor) plus `go_min` (Substrate's minimum); Folio's `platform-pins.yaml` mirrors `go_floor` and `ci`; `scripts/check-bom-drift` verifies the mirror.
