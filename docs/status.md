# Status

Pre-release; not tagged.

## Built

- Manifest schema + validator, builder (`validate`, `modules`, `generate`, `build`), module registry.
- Modules: config, logging, health, httphost (over libs app-host), capabilities, sqlite, auth, policy,
  secrets, cli, mcp, pluginhost. Every catalogue module is `ready`.
- Examples: hello-service, hello-app-host, hello-cli. Acceptance: flag test (minimal vs agentic, `e2e/`).

## Next

- Grow the `agentic` profile with the new modules and add `hello-agentic` (it is still
  `minimal + capabilities`), then extend the flag test to compare real module sets.
- Reconcile `bom/bom.yaml` with Folio's `platform-pins.yaml` (one source); Go floor is 1.26.10.
- v0.1.0: per-module and per-profile conformance suites, bump test, tag (a maintainer's call).
- `gui-shell` profile in a sibling repo; Hadron as the first real app.

## Decided

Standards: cobra, stdlib mux, goose via libs core/store, `/healthz` `{status, checks}`, YAML config,
security/auth + security/policy as defaults. BOM is the single pin source; the builder emits the
wiring file only. Tasks live in Torque project Substrate (PRJ-20261102-0001).
