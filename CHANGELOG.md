# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Semver from the first release; pre-1.0
minor releases may break APIs.

## [Unreleased]

### Added

- Manifest schema (`substrate.yaml`), validator and module resolver.
- Builder CLI: `validate`, `modules`, `generate`, `build`.
- Module registry and the config, logging, health, httphost (over libs app-host) and capabilities modules.
- `hello-service` and `hello-app-host` examples; end-to-end flag test (minimal vs agentic).
- `bom/bom.yaml` pin scaffold, CI workflow, `scripts/scan-public`.
