# folio

Preset-driven project scaffolding for the hollis-labs portfolio.

`folio new <preset> <target-dir>` renders a typed preset (YAML manifest +
text/template tree) into a new project directory, with a `.folio.yaml`
breadcrumb recording exactly what was generated so the project can be
re-rendered later (and, in a future release, synced when the preset evolves).

## Status

Folio ships Go libraries, class 1 apps, class 2 tools and services, a Nanite plugin,
and compose-only layers. Its own runtime remains a local CLI; [project.yaml](./project.yaml)
records the actual interface and ownership. It has no Substrate runtime or auth/policy gate;
local operations use the caller’s OS permissions. The generated apps have separate contracts.
Ask folio rather than this README which presets exist:

```sh
folio preset list           # bundled and user-dir presets, with version and description
folio preset show go-lib    # one preset's metadata, composes: entries and inputs
```

Add `--json` to either for machine-readable output. A preset marked `[layer]`
(`layer_only: true` in its `preset.yaml`) exists to be composed by other presets.

`folio new` / `folio plan` / `folio preset validate` all understand
`composes:`. Sync, post-render Hadron hooks, federated git-URL preset
sources, and Folio’s own MCP / HTTP surfaces are deliberately deferred; see
[`CHANGELOG.md`](./CHANGELOG.md) for the full deferred list.

## Substrate apps

`substrate-app`, `app-dashboard` and `app-chat` render class 1 Substrate `app-host`
apps with the agentic profile, enforced auth, own offline-created identity, SQLite,
scoped routes and authenticated MCP. Dashboard adds an app-shell list UI; chat adds
owner-isolated conversations and messages using the published kit-chat and kit-identity.
Chat starts with no assistant provider connected; its service layer offers a provider hook.
`app-ade` puts a person and an agent over the same surface with the ADE packages: the agent host
at `/api/agent` (owner from the verified principal, model from operator configuration), a
workspace page (surface, guide bar, agent panel, attention layer) and a starter notes board. The
domain, the agent's prompt and the README are the person's and `folio sync` never writes them
(sync policy `skip`); the shell is the preset's and sync keeps it current. See
[`docs/app-ade.md`](./docs/app-ade.md).

Each app includes a project descriptor, fleet deployment and native Cerberus supervision,
a runbook, full checks, build artifacts and a SemVer release workflow. Managed runs work
from installed binaries with XDG configuration/data/state, independent of the checkout.
Release workflows accept only correctly prefixed SemVer tags whose commit is on main.

`substrate_version` and the applicable `kit_*` versions have no defaults: take them from
the selected framework’s `bom/bom.yaml`. Chat additionally requires `kit_chat`, `kit_cards`,
`kit_identity` and `kit_account`; ADE requires `ade_agenthost`, `ade_core`, `ade_agent` and `ade_react`. After rendering, follow the generated README: tidy Go,
sync pins, tidy again; for UI apps install frontend packages and commit the lockfile.
Folio itself never installs, builds or runs a rendered project. See [architecture](./docs/architecture.md).

## Surface apps

`surface-app` (a plain web app: the app frame and views) and `surface-desk` (a Go server on Surface with
windows, the agent panel and cards) scaffold an app whose interface is built from Surface's `@surface`
registry at a pinned release: the theme, the raw-control lint rule, `npx surface-ui verify` in CI, agent
instructions and a gallery. `make setup` once, then `make check`. See [`docs/surface-apps.md`](./docs/surface-apps.md).

## Class 2 tools

`substrate-cli` and `substrate-service` (layer `substrate-tool-baseline`) render a class 2 tool of the
project/1 class model (release candidate 1): `project.yaml` (written once, never overwritten),
`deploy.yaml` (fleet-deploy/v1), for a service `<name>.cerberus.yaml` (cerberus-project/v1; fleet-install
places the binary and Cerberus only supervises it), the class 2 CI (vet, pins check, build, race tests,
scan-public) and a build workflow on the shared reusable workflow. A service's auth mode is `enforce`
(bearer token) or, with explicit `serve --dev`, `loopback_open` on a loopback bind. Managed runs
enforce auth even if the binary was generated with a development default; optional MCP also enforces auth.
`internal/descriptor` validates `project.yaml` and tests that the three descriptor files agree. Needs
`substrate_version` (no default). See [`docs/class2-tools.md`](./docs/class2-tools.md).

## Quickstart

```sh
go install github.com/hollis-labs/tools/folio/cmd/folio@latest

folio new base /tmp/folio-smoke \
  --input project_name=smoke_test \
  --input github_owner=chrispian \
  --input description="folio v0 smoke" \
  --non-interactive

cd /tmp/folio-smoke
go vet ./...
go test ./...
```

Drop `--non-interactive` to be prompted for any inputs you didn't supply on
the command line. Drop the `--input` flags entirely and folio will prompt
for everything required.

## TypeScript plugin authoring

Render `ts-plugin` with `plugin_name`, `description`, and `sdk_spec` pointing to a
protocol-2 SDK release or local tarball. SDK main implements Serve v2; this
is not a release. The default SDK spec remains an explicit placeholder until a
protocol-2 release exists. Build a local main tarball and verify its exported
`PROTOCOL_VERSION` is 2; the generated README gives the commands. Optional `include_ui=true` adds a browser bundle. Generated projects
use schema-first tool/config types, esbuild and the shared SDK artifact helper.
The generated README explains harness tests and the configurable dev reload seam;
live host reload waits on protocol-2 lifecycle adoption.

```sh
folio new ts-plugin ./echo-plugin --non-interactive \
  --input plugin_name=echo-plugin --input description="Echo tools" \
  --input sdk_spec=file:/path/to/plugin-sdk.tgz
```

## Commands

| Command | What it does |
|---|---|
| `folio new <preset> <dir>` | Render a preset into `<dir>`. Prompts for missing inputs unless `--non-interactive`. |
| `folio plan <preset> <dir>` | Dry-run — print resolved inputs + computed values + planned file list. No writes. |
| `folio preset validate <preset-dir>` | Run the v0 validation rule set against `<preset-dir>/preset.yaml`. |
| `folio preset list [--json]` | List every bundled and user-dir preset (`~/.folio/presets/local/<id>@<version>/`) with its version, source and description. |
| `folio preset show <id> [--json]` | Show one preset's metadata, its own declared `composes:` entries (not a resolved chain) and its inputs. Exits 1 if the id is unknown. |
| `folio inspect <dir> [--json]` | Read-only drift report: re-render the preset recorded in `<dir>/.folio.yaml` with the recorded inputs and compare it, per file, with the project (`unchanged`, `preset_updated`, `locally_modified`, `conflict`, `added_upstream`, `removed_upstream`, `missing_locally`). Never writes; exits 0 whenever it can report (1 if `<dir>` has no `.folio.yaml` or the preset no longer accepts the recorded inputs). Replays the generation clock and folio version from the manifest, so a `LICENSE` reading `{{ .now.Year }}` does not drift when the year turns; a file that renders random bytes (`uuid`) is marked `*` and compared for local edits only. |
| `folio sync <dir> [--plan] [--input k=v] [--theirs-to SUFFIX] [--json]` | Apply what `inspect` reports, against the preset version folio has now: write `preset_updated` and `added_upstream` files (never over an untracked file already there), keep `locally_modified`, leave a `conflict` as it is and report it (`--theirs-to .folio-new` writes the preset's new rendering beside it to merge by hand), and leave `removed_upstream` and `missing_locally` files alone. A path under a sync rule with policy `skip` is never written, whatever its state: that is how a preset marks authored files. A conflict under a rule with policy `overwrite` takes the preset's copy. Rules match in order, the root preset's first, then the rendering layer's (`**` spans directories). `--input` sets an input a newer preset version requires. `--plan` writes nothing; otherwise `.folio.yaml` records the new versions, inputs and digests and a `sync_history` entry. There is no three-way merge: `.folio.yaml` stores only digests. Repository workflows are not synced. |

## Composing presets

A preset can layer on top of other presets by declaring `composes:`. Each
entry names a composed preset by id, pins its version via a semver
constraint, and points at its directory. The composing preset's files then
overlay the composed preset's tree in declared order — later layers
overwrite earlier ones on the same relative path.

```yaml
# presets/go-package/preset.yaml
folio_version: "0.1"
id: go-package
version: 1.0.0

composes:
  - id: base
    version: ">=0.1,<1.0"
    source: local
    path: ../base

inputs:
  - name: package_name
    type: string
    required: true
    pattern: "^[a-z][a-z0-9_]*$"
```

The bundled `go-package` preset does exactly this — it layers on `base`
to add `internal/<package_name>/` library code and overwrites the base
`README.md` + `cmd/<project>/main.go` with a library-first stub.

```sh
folio new go-package /tmp/folio-compose \
  --input project_name=smoke_compose \
  --input github_owner=chrispian \
  --input package_name=greeter \
  --non-interactive

cd /tmp/folio-compose
go vet ./...
go test ./...
```

The resulting `.folio.yaml` records both layers in apply order under
`presets:`, and each file's `preset:` field names the layer that produced
it (the last writer on overwrites).

**Constraint syntax.** npm-style operators are supported: `>=`, `<=`, `>`,
`<`, exact, `*`, tilde (`~1.2.3` → `>=1.2.3,<1.3.0`), caret (`^1.2.3` →
`>=1.2.3,<2.0.0`), AND-via-comma (`>=1.0,<2.0`), OR-via-pipe (`^1.0 || ^2.0`).
Partial versions canonicalize (`1.0` → `1.0.0`).

**Var scoping.** A composed preset inherits the caller's `.inputs.*` by
default. To override a per-key value for the composed layer, declare it in
the entry's `vars:` block — the value is a Go template rendered against
the *caller's* render context (so `{{.inputs.foo}}` inside a `vars:` value
resolves to the caller's input named `foo`). Templates inside the composed
preset's own files render against the layer's own resolved inputs +
computed values, with prior layers' values visible.

**Limits.** v0.2 supports `source: local` only (git URL sources land in
v1.1+). Compose DAG depth is capped at 8; direct or transitive cycles
produce a path-bearing error.

## Inputs resolution

For each input declared by a preset, folio resolves a value in this order
(higher beats lower):

1. CLI flag — `--input key=value` (repeatable).
2. `--inputs-file <path>` — YAML or JSON file of `key: value` pairs.
3. Environment variable — `FOLIO_INPUT_<UPPER>` (hyphens → underscores).
4. Preset-declared default.
5. Interactive prompt (charmbracelet/huh), unless `--non-interactive`.
6. Error — exit 2 with the list of missing required inputs.

## Template helpers

folio templates use Go `text/template` with a curated funcmap. Helper names
match Hadron where shared (`basename`, `dirname`, `ext`, `json`, `default`,
`ternary`, `upper`, `lower`, `trim`, `replace`, `split`, `join`) so
templates portable between the two tools evaluate identically.

folio additions cover case conversion (`kebabCase`, `snakeCase`,
`camelCase`, `pascalCase`), quoting (`quote`, `squote`, `shellQuote`,
`jsonEscape`), encoding (`jsonIndent`, `toYAML`, `b64encode`, `b64decode`),
date/time (`date`, `dateISO`), lists/dicts (`list`, `first`, `last`,
`slice`, `dict`, `get`, `hasKey`), random (`uuid`, `randAlphaNum`), and the
folio-specific `licenseHeader`, `gomodPath`, `gitUser`, `spdxId`.

**Excluded from the funcmap**: `env`, `readFile`, `getHostByName`, `httpGet`,
`exec`/`shell`. These exist in Hadron's funcmap but are deliberately not
in folio's — folio's threat model includes third-party presets via git URL
(v1.1+), and template-time access to environment variables / the
filesystem is a secret-leak and reproducibility risk under that model.

## Development

```sh
make test       # go test -race ./...
make vet        # go vet ./...
make lint       # golangci-lint run
make vuln       # govulncheck ./...
make pins       # drift-check every version pin against platform-pins.yaml
make build      # build the folio binary
make install    # go install ./cmd/folio
```

CI runs `go test -race`, `go vet`, `golangci-lint`, `govulncheck` and the pins
drift check on push and pull requests to `main`.

`platform-pins.yaml` mirrors the portfolio's version pins (Go floor,
`actions/checkout`, `actions/setup-go`, golangci-lint action and binary,
govulncheck) from `bom/bom.yaml` in github.com/hollis-labs/substrate, the source.
Do not edit it by hand: change the BOM and run `substrate pins sync --folio <this
dir>` from a Substrate checkout. It does not drive rendering yet: the presets still carry
the literals, and `scripts/check-platform-pins-drift.sh` fails when any of them —
preset `go_version` defaults, rendered workflow templates, folio's own CI, the
conformance script's floor, or (with `--checklist FILE`) the ratified lib
checklist — disagrees with the manifest. `--self-test` breaks one value at a time
in a scratch copy and requires exactly that check to fail.

## Dashboard apps and the former sysop-ui preset

```sh
folio new app-dashboard /tmp/my-dashboard \
  --input repo_name=my-dashboard \
  --input substrate_version=<framework version> \
  --input kit_app_shell=<from bom.yaml> ... \
  --non-interactive
```

`app-dashboard` 3.0 renders a Substrate app (see above) with a React frontend on app-shell and the
kit-dashboard theme: an app-owned sign-in screen and one list surface. It replaces the go-webui and
Chimera dashboard of 2.x (clean break: `project_name`, `gui_host`, `chimera_*`, `design_kit_version`,
`go_webui_version` and `base_path` are gone; a Substrate app serves at the root).

`sysop-ui` remains a deprecated alias: `new`, `plan`, `preset show` and `inspect` resolve it to
`app-dashboard`, and new breadcrumbs record `app-dashboard`. Supported breadcrumbs remain readable by `inspect`, which reports drift without updating
the app or its breadcrumb. Removed presets and pre-3.0 dashboard breadcrumbs fail explicitly. `folio sync` applies a preset's updates file by file but
nothing migrates an already generated app to a different preset. Removed `chat-app` breadcrumbs still fail with `preset_removed`; fresh chat projects use
`app-chat`. There is no automatic migration or alias between the two.

## License

MIT — see [LICENSE](../LICENSE).

## Generate a module in an existing repository

Use an explicit repository boundary; the module's parent directory must already exist:

```sh
folio plan substrate-service ./tools/example --repository-root ./tools \
  --input repo_name=example --input repository_name=tools --input substrate_version=<current-version> --non-interactive
folio new substrate-service ./tools/example --repository-root ./tools \
  --input repo_name=example --input repository_name=tools --input substrate_version=<current-version> --non-interactive --yes
folio inspect ./tools/example --repository-root ./tools
```

`repo_name` names the project/binary. `repository_name` names the physical GitHub repository
(required with `--repository-root`); the target's relative path supplies `repository_location`.
Checkout folder names and worktree labels are never used as GitHub repository identity.
For this example the module is `github.com/hollis-labs/tools/example`, and the descriptor records
repository `hollis-labs/tools` and location `example`. Libraries use the same repository option
without adopting Substrate.

Checks and reusable builds are written to the selected root's `.github/workflows/example-*.yml`,
with the module working directory and dependency-cache path. An existing workflow or module path
is refused; other modules and authored workflows are preserved. The manifest records each workflow's
repository destination. Inspection reads those files only when you explicitly supply the root.
Plan output uses paths relative to that root. This is new-project generation; `folio sync` does not sync repository workflows.
