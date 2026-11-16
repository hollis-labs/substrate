# Architecture: Substrate presets

Folio renders typed presets, records a `.folio.yaml` breadcrumb, and leaves the application
independent of Folio. `project.yaml` is authored project metadata; Folio never rewrites it.
Existing removed-preset breadcrumbs fail explicitly, rather than silently selecting a new preset.

## Composition

```text
go-baseline
  substrate-baseline
    substrate-app
    substrate-frontend
      app-dashboard
      app-chat (also selects the backend chat extension)
  substrate-tool-baseline
    substrate-cli
    substrate-service
```

Class 1 apps use `app-host` with the agentic profile plus database, identity and CLI modules.
The shared backend owns boot planning, configuration discovery, the Host/identity/guard handler
stack, scoped routes, storage, migration wiring and deployment/release templates. Print-only
commands do not start write-capable modules. Managed serving enforces auth, including on loopback.
The owner is created offline; there is no open HTTP owner-setup route.

The frontend layer supplies React/Vite/Tailwind, app-shell, the shared cookie/CSRF request adapter,
session handling and a list example. Dashboard uses that example. Chat supplies the conversation UI,
kit-chat components and kit-identity sign-in/offline-setup screens while retaining the shared host
and session adapter. Published kit props are presentation contracts, not invented identity endpoints.

Chat adds owner-filtered conversations/messages and scoped read/write routes. Request bodies cannot
choose a principal or message role. Other owners' conversation IDs return 404. An optional service
`Responder` receives only the selected owner's history. No provider is configured by default, and the
UI says so. A failed reply does not undo a saved user message. Concurrent sends may interleave replies;
applications needing serialized turns must add a queue. The inherited items example remains available.

Class 2 CLI/service presets use minimal profiles by default; `with_mcp=true` selects agentic.
See [class2-tools.md](./class2-tools.md) for HTTP and explicit local-stdio trust contracts.
Libraries do not acquire Substrate merely by sharing the Go baseline.

## Boundaries and generated operations

Transports call `internal/service`; only that layer runs SQL. The generated transport lint forbids
handlers importing `database/sql`, storage or the database module. The authenticated principal owns
rows. Identity, scope guards and CSRF checks remain outside the business service.

Class 1 output includes a full check workflow, pinned reusable build, SemVer release workflow,
checksum artifacts, runbook, `project.yaml`, fleet deployment and native Cerberus supervision.
Fleet-install places the binary; Cerberus supervises the installed link with `serve --managed --addr`.
Configuration, data and secret state use XDG paths; explicit operator paths remain supported.

`settings.runtime` in the descriptor supplies the Folio deployment projection. `folio deploy render`
previews output by default; `--write` stages and replaces generated files, preserving descriptor bytes
and refusing authored output conflicts unless explicitly accepted. It never deploys or restarts anything.
Changing deployment target architecture does not rewrite the CI build target. See class 2 docs for keys.

For an existing monorepo, `--repository-root` and explicit `repository_name` select the boundary and
repository identity. Folio derives module location/path and creates named workflows at the repository
root. It refuses collisions and symlink escapes; standalone generation remains supported. `inspect`
requires that explicit root to read recorded repository workflow files. `folio sync` leaves repository workflows alone.

## Versions and validation

The selected Substrate version and direct Hollis kit versions are required inputs. Framework integration
tests read the kits from that version's BOM; frontend toolchain versions remain separately sourced literals.
Compose vars bind to the first encounter of a layer. App-chat explicitly selects the backend before the
frontend so both layers see the chat switch without duplicating their files.

Required-tool integration tests render and build real apps, check pins/wiring, run frontend types/lint/build,
Go vet/race tests and transport lint, and run temporary binaries. Tests exercise offline identity, scoped
MCP, managed auth, no-checkout operation and UI serving. App-chat adds real two-user API isolation and
provider success/failure persistence checks. Missing tools are visible skips locally and failures with
`FOLIO_REQUIRE_TOOLS=1` in the root Folio workflow.

Project/1 structure is parsed through Substrate's shared `kinds` package in Folio's deployment
renderer and generated class 2 descriptor adapters. The shared typed/schema contract is separate from
Folio's runtime projection and static/runtime conformance checks, which remain intact. This pure package
import does not boot a Substrate runtime inside Folio. Registry registration is separate.
