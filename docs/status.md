# Status

v0.1.0 (2026-11-02). Pre-1.0: APIs and the manifest schema may change in minor releases.

## Built

- Manifest schema + validator, builder (`validate`, `modules`, `generate`, `build`), module registry.
- Modules: config, logging, health, httphost (over libs app-host), capabilities, sqlite, auth, policy,
  secrets, cli, mcp, pluginhost. Every catalogue module is `ready`.
- Profiles: `minimal` (config, logging, health) and `agentic` (adds capabilities, secrets, auth, mcp, with httphost).
- Examples: hello-service, hello-app-host, hello-cli, hello-agentic. Acceptance: flag test (`e2e/`) compares the
  linked modules and size of minimal vs agentic from one source (about 9.4 MB vs 13.7 MB on arm64 macOS).

## Next

- `gui-shell` profile (frontend; repo location to be settled) and Hadron as the first real app.
- Cosign keyless signing of release artifacts (optional; checksums ship today).

## Known limitations

- **Postgres** is not exposed by the `sqlite` module. libs `core/store` supports it and the Hollis Labs
  fleet runs a Postgres, so add a `postgres` module (or a driver option) when the first app needs it, not before.

## Decided

Standards: cobra, stdlib mux, goose via libs core/store, `/healthz` `{status, checks}`, YAML config,
security/auth + security/policy as defaults. `bom/bom.yaml` is the only hand-edited pin source
(`substrate pins sync|check` keeps `go.mod`, the CI workflows and Folio's mirror in step); the builder emits the
wiring file only. Tasks live in Torque project Substrate (PRJ-20261102-0001).
