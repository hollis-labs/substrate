# Conversion notes

> Legacy app-ade, app-chat, app-dashboard, substrate-frontend and sysop-ui references below are historical. These scaffolds are retired; new UI apps use Surface.

Folio itself remains a local command-line tool in `hollis-labs/substrate/folio`. Its own `project.yaml`
records class 2, the CLI interface and ownership. It currently has no Substrate runtime, authentication
or policy gate; the caller's OS permissions govern its operations. This is a documented gap relative
to the runtime class bar, not a claim that Folio already implements its generated presets' runtime.

## Current generated behavior

- Class 1: `substrate-app`, `app-dashboard`, fresh `app-chat`; agentic, enforced auth and own identity,
  full CI, SemVer releases, runbook, authored descriptor and installed native deployment/supervision.
- Class 2: CLI/service with managed auth rules and optional scoped MCP. See [class2-tools.md](./class2-tools.md).
- Explicit monorepo generation creates the module plus repository-root workflows. Deployment output can
  be regenerated from authored descriptor settings; the descriptor is preserved.
- Existing `chat-app`, `go-service-app`, Chimera and pre-3.0 dashboard breadcrumbs still fail with
  `preset_removed`. Fresh `app-chat` is not an alias or migration for `chat-app`.
- Existing generated projects are untouched. `folio sync` and automatic migration are not implemented.

## Version evidence

The framework-required CI pin is `v0.1.1-0.20261115173848-45b018004dd2`. Its BOM includes
kit-chat 0.8.0, kit-identity 0.1.4, app-shell 0.11.0, design-app-runtime 0.6.0,
design-bindings 0.3.0 and kit-cards 0.3.0. These are validation evidence, not defaults
or a second source of pins: generation requires the selected framework and kit inputs.
The earlier missing-chat-kit and unpublished-app-shell blockers are resolved.

## Remaining boundaries

- `substrate pins sync --folio` mirrors Go/CI pins, not the framework version or kit inputs.
  CI still maintains its explicit framework version; generated integration tests read that BOM.
- Frontend toolchain packages outside the BOM remain explicit literals. A central frontend toolchain
  source is framework work; Folio does not invent one.
- No framework-owned CI template package exists here. Folio's templates and pinned reusable build
  remain its implementation, validated by generated-app tests.
- Shared project/1 JSON Schema/Go validation now comes from Substrate `kinds`. New class 2 output
  requires the version above or a compatible successor. Folio's runtime projection and conformance
  checks remain local; structural parsing is not operational certification. Registry registration
  remains separate, and no registry endpoint or hook is fabricated.
- The nested historical `folio/.github/workflows/ci.yml` is still read by the pins drift checker;
  GitHub executes the repository-root `folio-checks.yml`. Consolidating that drift-check source is separate.
- Chat starts without an assistant provider. The service hook is real, but production provider policy,
  credentials, rate limits and serialized turns are application work.

Install and rollback coordination is described in [cutover-plan.md](./cutover-plan.md).
