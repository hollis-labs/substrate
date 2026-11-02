# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Semver from the first release; pre-1.0
minor releases may break APIs.

## [Unreleased]

### Added

- `auth` module over libs `security/auth` (mode dial, JWT and API-key verifiers, scope table, bind validation); httphost wraps `/api/` with the guard when started. Unmapped routes fail closed in enforce mode, including `/api/capabilities` until the app maps it.
- `policy` module over libs `security/policy` (shadow by default, fail closed, slog audit). The app registers its PDP with `policy.UsePDP`; starting without one is an error. `policy.AllowAll` is an explicit dev PDP.

- `sqlite` module over libs `core/store` (goose migrations via `sqlite.UseMigrations`, health check, `schema_mode`).
- Lint cleanup: errcheck findings from the first CI run.

- Manifest schema (`substrate.yaml`), validator and module resolver.
- Builder CLI: `validate`, `modules`, `generate`, `build`.
- Module registry and the config, logging, health, httphost (over libs app-host) and capabilities modules.
- `hello-service` and `hello-app-host` examples; end-to-end flag test (minimal vs agentic).
- `bom/bom.yaml` pin scaffold, CI workflow, `scripts/scan-public`.
