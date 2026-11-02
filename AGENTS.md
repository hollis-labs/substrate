# substrate

Opinionated, versioned Go framework for Hollis Labs apps: an app is `substrate.yaml` plus its own engine.
Not: a place for app domain code, a template to copy from, or an owner of identity, policy, data or listeners.

## Start here

- `README.md` — what it is and how to build a hello app.
- `catalog/` — the module, profile and variant catalogue the builder validates against.
- `manifest/` — `substrate.yaml` parsing, validation and module resolution.
- `cmd/substrate/` — builder CLI (`validate`, `modules`, `generate`, `build`).
- `module/` — the module contract and registry. `modules/<name>/` — capability modules.
- `boot/` — starts the modules a generated wiring file selected.
- `examples/` — hello apps; `e2e/` — acceptance tests that build real binaries (flag test).
- `bom/bom.yaml` — the single place version pins live. `docs/status.md` — what is built and what is pending.

## Commands

```sh
go vet ./... && go test ./...
go run ./cmd/substrate build -f examples/hello-app-host/substrate.yaml
python3 scripts/scan-public --ref HEAD     # secrets / hosts / private paths, tree and history
```

## Boundaries

- Layering: Substrate sits above `github.com/hollis-labs/libs` and may import any of it; nothing in libs imports Substrate. Wrap and pin libs code; never copy it.
- Every module is optional. Selected modules are linked by the generated wiring file only; `go build` of generated output must work without the substrate CLI.
- Pins are hand-edited only in `bom/bom.yaml`. `substrate pins sync` writes `go.mod` and the CI workflow from it (and, with `--folio <dir>`, the generated mirror `folio/platform-pins.yaml` in the hollis-labs/tools monorepo); `substrate pins check` verifies. Never edit those copies by hand.
- Scope is Go backend plus TypeScript frontend. Other stacks are a seam, not built.
- The repo is public. No secrets, internal hosts, internal addresses or personal paths in files, commits or CI. Tags are a maintainer's decision.
- This repo reuses the name of an older libs-era `substrate` repo that was deleted in the 2026-10-19 reorg; stale `github.com/hollis-labs/substrate/<mesh|agent|harness|llm-core>` paths elsewhere are that old code and are not this module.
