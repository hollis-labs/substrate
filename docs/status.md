# Status

Pre-release; not tagged.

## Built

- Manifest schema + validator, builder (`validate`, `modules`, `generate`, `build`), module registry.
- Modules: config, logging, health, httphost (over libs app-host), capabilities, sqlite, auth, policy,
  secrets, cli, mcp, pluginhost. Every catalogue module is `ready`.
- Profiles: `minimal` (config, logging, health) and `agentic` (adds capabilities, secrets, auth, mcp, with httphost).
- Examples: hello-service, hello-app-host, hello-cli, hello-agentic. Acceptance: flag test (`e2e/`) compares the
  linked modules and size of minimal vs agentic from one source (about 9.4 MB vs 13.7 MB on arm64 macOS).

## Next

- v0.1.0: tag (a maintainer's call). Conformance suites and the BOM bump test are in `conformance/`.
- `gui-shell` profile in a sibling repo; Hadron as the first real app.

## Decided

Standards: cobra, stdlib mux, goose via libs core/store, `/healthz` `{status, checks}`, YAML config,
security/auth + security/policy as defaults. BOM is the single pin source; the builder emits the
wiring file only. Tasks live in Torque project Substrate (PRJ-20261102-0001).
