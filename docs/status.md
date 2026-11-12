# Status

v0.1.0 (2026-11-02). Pre-1.0: APIs and the manifest schema may change in minor releases.

## Built

- Manifest schema + validator, builder (`validate`, `modules`, `generate`, `build`), module registry.
- Modules: config, logging, health, httphost (over libs app-host), capabilities, database (SQLite or Postgres), auth, policy,
  secrets, cli, mcp, pluginhost. Every catalogue module is `ready`.
- Profiles: `minimal` (config, logging, health) and `agentic` (adds capabilities, secrets, auth, mcp, with httphost).
- Boot hooks: `boot.StartWith` with `AfterConfig` (once, after the config override) and `Before[<module>]` (just before that module starts); `boot.Start` is unchanged. No first-class data directory yet.
- Examples: hello-service, hello-app-host, hello-cli, hello-agentic. Acceptance: flag test (`e2e/`) compares the
  linked modules and size of minimal vs agentic from one source (about 9.4 MB vs 13.7 MB on arm64 macOS).
- Identity: `Service.User` lookup accessor (public view, no secrets); `GET /api/identity/profile` (signed-in person plus `assurance: local`). Signing-key rotation: `POST /api/identity/signing-key/rotate` (`<app>.admin`), overlap with a retired key set (`retired_key_ttl`, min 70m), `compromised` cut-off; single process per `key_file`. Second-issuer guard: no signing key is created when the database holds users or API keys and `key_file` is missing (`allow_new_signing_key` overrides); lazy key creation for stdio-only processes is not built.
- MCP stdio local trust: `StdioOptions.LocalTrust` / `LocalScopes` (Go option, never config) runs a locally launched stdio process as `local:operator` (verified, scopes `*` by default) when no token is given; a given token is always verified. HTTP `/mcp` unchanged.

- UI vocabulary v0.4.1: the `user.menu` core region, `group` and `requires` on `menu.item` (builder side). The identity
  module's own `user.menu` items are pending (they wait on the identity UI kit being pinned in the BOM).

- App UI contributions (`frontend.contributions`: the app's own file, validated and merged like a module's) and an app
  config block (`config.<app>`) in the manifest.

## Next

- `gui-shell` profile (frontend; repo location to be settled) and Hadron as the first real app.
- Cosign keyless signing of release artifacts (optional; checksums ship today).

## Known limitations

- The Postgres path needs a live server to test; CI provides one (`SUBSTRATE_TEST_POSTGRES_DSN`), a plain local run skips it.

## Decided

Standards: cobra, stdlib mux, goose via libs core/store, `/healthz` `{status, checks}`, YAML config,
security/auth + security/policy as defaults. `bom/bom.yaml` is the only hand-edited pin source
(`substrate pins sync|check` keeps `go.mod`, the CI workflows and Folio's mirror in step); the builder emits the
wiring file only. Tasks live in Torque project Substrate (PRJ-20261102-0001).
