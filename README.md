# Substrate

Go modules for the agentic side of Hollis Labs: the agent harness, the LLM core,
the agent mesh and the agent runtime core. Anything that is about agentic work
or the fabric agents run on lives here. General-purpose libraries live in
[`libs`](https://github.com/hollis-labs/libs).

## Status

`harness`, `llm-core` and `mesh` hold code that was imported, with its git
history, from earlier standalone repositories; `harness` and `mesh` also hold
code written in this repository since. `agent` provides embeddable native runtime mechanisms with host-owned policy
and persistence, described in its [README](agent/README.md). Releases are per module and tagged
`<module>/vX.Y.Z`; the versions of a module are listed in its own
`CHANGELOG.md`.
`harness` provides the consolidated library foundation described in its
[README](harness/README.md), including explicit unsupported operations and
adoption requirements. Published versions are listed below; release tags remain
per module, with release notes in each module's `CHANGELOG.md`.

## Modules

Each module has its own `go.mod`, its own version and its own tags.

| Module | Import path | Latest published tag | Scope |
|---|---|---|---|
| `harness` | `github.com/hollis-labs/substrate/harness` | `harness/v0.3.0` | The agent harness (Cairn): launch, workspace and session plumbing for agent CLIs. |
| `llm-core` | `github.com/hollis-labs/substrate/llm-core` | `llm-core/v0.1.0` | The LLM core: shared model, provider and routing contracts and types. |
| `mesh` | `github.com/hollis-labs/substrate/mesh` | `mesh/v0.1.0` | The agent mesh: messaging, federation, the tether client, human-in-the-loop, agent teams, the broker and agent definitions. |
| `agent` | `github.com/hollis-labs/substrate/agent` | `agent/v0.3.0` | Embeddable native agent mechanisms with host-owned policy and persistence. |

Use a published module the usual way:

```sh
go get github.com/hollis-labs/substrate/mesh@v0.1.0
```

Coming from a standalone module such as `go-sandbox` or `go-messaging`? See
[docs/migration.md](docs/migration.md) for the old-to-new table and adoption notes.

## Adopt the consolidated packages

Apps can fail to build until they adopt the new imports, released module pins
and required API changes together. Consolidating the libraries did not update
application source or deploy anything. The earlier import-map/codemod rehearsal
was a dry run; its scratch replacements and old package homes are not a current
consumer recipe.

The version table above names published module tags as of 2026-10-09. For a
consumer, use the module path with `@vX.Y.Z`, without the directory in the version:

```sh
# Run inside the consumer's Go module; select only the modules it uses.
GOWORK=off go get github.com/hollis-labs/substrate/harness@v0.3.0 \
  github.com/hollis-labs/substrate/llm-core@v0.1.0 \
  github.com/hollis-labs/substrate/mesh@v0.1.0 \
  github.com/hollis-labs/substrate/agent@v0.3.0
```

Use a supported toolchain satisfying the selected modules' Go directives.
The current app adoptions used Go 1.26.9. Read each module's CHANGELOG and the
actual public API at the selected version before adapting older callers.

Agent v0.3.0 core turn, approval and service types now use the consolidated
llm-core, harness permission and ui-go packages. Migrate matching consumer
imports and mocks together; old and new named types are not interchangeable.

### Old-to-new import map

All paths in these tables start with `github.com/hollis-labs/`. A suffix follows
its new prefix only where that package still exists. The
[per-unit migration notes](docs/migration.md#version-table) contain the full path
lists and imported-source provenance; their historical release observations and
dry-run results retain their stated dates.

| Old prefix | Current prefix |
|---|---|
| `agent-contracts-leaf` | `substrate/llm-core/contracts` (root package `contracts`) |
| `go-embed-contracts` | `substrate/llm-core/embedcontracts` |
| `go-llm-contracts` | `substrate/llm-core/llmcontracts` |
| `go-llm-types` | `substrate/llm-core/llmtypes` |
| `go-modelsdev/modelsdev` | `substrate/llm-core/modelsdev` |
| `go-modelsdev-catalog-helpers` | `substrate/llm-core/costcalc` |
| `go-usage-ledger` | `substrate/llm-core/usageledger` |
| `go-federation` | `substrate/mesh/federation` |
| `go-hitl` | `substrate/mesh/hitl` |
| `go-messaging` | `substrate/mesh/messaging` |
| `go-tether-client` | `substrate/mesh/tetherclient` |
| `go-agent-broker` | `substrate/harness/broker` (mesh placement remains separate) |
| `go-context-window` | `substrate/agent/contextwindow` |
| `go-loopdetect` | `substrate/agent/loopdetect` |
| `go-reflexes` | `substrate/agent/reflexes` |
| `go-toolbroker` | `substrate/agent/toolbroker` |
| `go-toolresult` | `substrate/agent/toolresult` |
| `go-toolselect` | `substrate/agent/toolselect` |
| `go-agent-context` | `substrate/agent/agentcontext` |

`agentkit`, `go-agent-wrapper` and `go-providers` split across several packages.
Do **not** replace their module roots with one new prefix:

| Old path | Current path / required action |
|---|---|
| `agentkit/agentcontext` | `substrate/agent/agentcontext`; artifact-bearing composition uses `substrate/harness/agentcomposition` separately |
| `agentkit/agentlaunch` | `substrate/harness/agentlaunch` |
| `agentkit/agentlaunch/providerplant` | `substrate/harness/agentlaunch/planting` (package `planting`) |
| `agentkit/agentsessions` | `substrate/harness/adapters/agentsessions` |
| `agentkit/agentruntime/checkpoint`, `/loopback`, `/runtimebind`, `/sessionkit`, `/turn` | corresponding `substrate/harness/adapters/<package>` |
| `agentkit/broker` | `substrate/harness/broker` |
| `agentkit/agentruntime/bootdir` | writer removed; use explicit artifact materialization; relative-path validation is `agentlaunch.ValidateBootDirRelPath` |
| `agentkit/agentruntime/runtimekind` | removed; adapt to `llm-core/contracts/runtimes` and `harness/adapters/runtimebind` |
| `go-agent-wrapper/adapters` | `substrate/harness/adapters` |
| `go-agent-wrapper/acp`, `/activity`, `/launch`, `/turnoutput`, `/wrapper` | corresponding `substrate/harness/adapters/<package>` |
| `go-agent-wrapper/plant` | `substrate/harness/agentlaunch/planting` |
| `go-agent-wrapper/policy`, `/filters`, `/classifybridge` | corresponding `substrate/harness/interception/<package>` |
| `go-agent-wrapper/sandbox`, `/snapshot` | `substrate/harness/sandbox/wrapper`, `/snapshot` |
| `go-providers/provider`, `/registry`, `/layout`, `/providertest` | corresponding `substrate/harness/adapters/<package>` |
| `go-providers/provider/events` | `substrate/harness/adapters/provider/events`; match before its parent rule |
| `go-runtime-events/runtimeevents` | `substrate/harness/adapters/runtimeevents` |
| `go-egress-proxy/egress` | `substrate/harness/interception/egress` |
| `go-harness-filters/classify`, `/directive`, `/event`, `/normalize`, `/repair` | corresponding `substrate/harness/interception/filters/<package>` |
| `go-permission` | `substrate/harness/interception/permission` |
| `go-sandbox/sandbox` | `substrate/harness/sandbox`; see its [package map](harness/docs/units/go-sandbox/MIGRATION.md) |
| `go-safefs/pathsafe`, `/atomicfile` | corresponding `substrate/harness/sandbox/<package>` |
| `go-materialize/materialize` | `substrate/harness/workspace/materialize` |
| `go-materialize/artifact` | `substrate/harness/workspace/materialize/artifact` |
| `go-runner/runner` | `substrate/harness/runner` |

The `agentkit` root and `agentruntime` facade are not public replacement APIs.
Go's `internal/` visibility remains in force. Clean-break agent-definition and
instance contracts need schema/API adoption; a similarly named mesh package is
not evidence that every old `go-agentdef` or `go-agentmux-client` caller can move
mechanically. For general utilities, UI, workflow and plugin/MCP packages use the
[libs import table](https://github.com/hollis-labs/libs/blob/main/README.md#old-to-new-import-map).

### Codemod and verification steps

1. Fetch the consumer's current main and create an isolated branch. Inventory
   every `go.mod` (including nested application modules), source import, generator,
   public API snapshot and architecture guard that refers to an old module.
2. Use the [executable import-only codemod](https://github.com/hollis-labs/libs/blob/main/README.md#executable-import-only-codemod)
   with reviewed rows from these tables and the per-unit maps. Preview first;
   match complete path segments, longest first, and preserve explicit aliases.
   The contracts root changed its package clause from `agentcontracts` to
   `contracts`; keep the old qualifier with an explicit alias or update callers.
   `plant`/`providerplant` now use `planting` and require the same qualifier review.
3. Review API calls separately. Do not rewrite fixture strings, frozen consumer
   recipes, generators or architecture assertions blindly. Old seven-state leaf
   status code and deleted runtimekind symbols have no mechanical destination.
4. Add only the published modules used by each consumer module, as above. Run
   `GOWORK=off go mod tidy`, inspect `go.mod`/`go.sum` and `git diff --check`.
   An old transitive requirement can still come from an unadopted dependency;
   do not delete it to manufacture a clean graph. No committed local replace or
   `go.work` should be needed for a completed adoption.
5. Run focused affected checks during development and the application's own
   required build/vet/tests and CI before merging. Exercise permission and custody
   refusals as well as successful launch preparation. A library release or source
   adoption does not establish running-version, native installed support, live
   catalog/provider behavior or deployment readiness.

### API changes that need application work

**Hadron / older agentkit callers.** Hadron's old `agentkit v0.6.1` launcher was
not compatible with a path-only rewrite. Its
[completed adoption PR #27](https://github.com/hollis-labs/hadron/pull/27) provides
an application example, not an authority implementation to copy blindly:

- Translate application runtime settings to canonical `runtimes.Mode` values
  through `runtimebind.Resolve`; debug posture is separate from runtime mode.
- Separate pure projection/rendering from materialization. Use
  `workspace/render` and the authored `adapters/layout` resolutions for managed
  native content, then `agentlaunch.MaterializeArtifacts` for the single
  authority-checked writer. The removed bootdir side writer is not a fallback.
- Supply genuine artifact-only authorization derived from the application's
  already accepted launch operation. `ArtifactAuthorizer` must bind the exact
  inactive private root, operation/request/plan identity, fresh observations,
  host validation, canonical locks and an external durable receipt store.
  Revalidate under locks and refuse substitutions, released custody or an
  already-started session. Paths and `AutoPlantBootDir` flags do not grant custody.
  Hadron's accepted operation is bounded and process-local: it claims neither
  durable admission across restart nor credential authority.
- Preserve provider permission behavior explicitly. Hadron sets
  `permission.ModeDefault` for Claude and renders the provider's restrictive
  native policy. Codex retains its existing `never` / `workspace-write` native
  defaults. In the library an empty `Provider.Permission` adds no posture flags;
  it is not a universally restrictive policy. Do not substitute bypass flags,
  blanket allow, fake grants or fixture authorizers to make a launcher compile.

Materializing planting/session start paths need explicit authority and a
lossless `PreparedExecution` handoff. Read
[harness scope](harness/README.md#supported-scope-and-adoption-requirements),
[workspace authority/recovery](harness/workspace/README.md), and
[planting entry points](harness/agentlaunch/planting/doc.go).
Artifact completion is not launch readiness. Credential effects require their
separate typed inputs and authorization; no renderer or artifact grant discovers
an ambient provider home or authorizes installed writes.

**Tesseract / embedding and queue identities.**
[Tesseract v0.11.0](https://github.com/hollis-labs/tesseract/releases/tag/v0.11.0)
changed public `WithEmbedder` types to this module's `llm-core/embedcontracts` and
`WithQueue` to `libs/util/queue`. Adopt that released upstream before passing
new contracts through those APIs. Identical-looking structs/interfaces from the
old `go-embed-contracts` package do not give an app one coherent Go type graph;
this was the Nanite dependency-order blocker in the original dry run. Migrate
all relevant callers and mocks together; do not retain an old contract alias as
a type bridge. The release is source adoption, not a live Tesseract upgrade.

The earlier dry-run findings are retained in [docs/migration.md](docs/migration.md#adoption-notes)
with their historical dates. Current API contracts and released application
adoptions take precedence over assumptions from that rehearsal; apps that have
not adopted may still fail. This guide makes no fleet-wide build or runtime claim.

## Rules

- **One-way dependencies.** Substrate may depend on `libs`. `libs` never depends
  on substrate. The `libs` repository fails CI on any reference to this one.
- **Independent modules.** No `go.work`, no `replace` directive in a committed
  `go.mod`, no module nested inside another. `scripts/check-layout` enforces it.
- **Independent releases.** A module is released alone, with a tag that carries
  the module's directory as a prefix: `mesh/v0.1.0`, `harness/v0.3.2`. There are
  no repository-wide versions and no lockstep releases.
- **Public from the first commit.** No secrets, tokens, internal host names,
  internal addresses or personal paths in files, history, CI or commit messages.
  `scripts/scan-public --allowlist scan-public.allowlist` checks all of them
  with the same reviewed exceptions as CI.

## Developing across modules (there is no `go.work`)

A `go.work` makes code build on one machine and nowhere else, and it hides the
version a module really requires. This repository has none: it is git-ignored,
CI fails if one is committed, and every script and workflow runs with
`GOWORK=off`.

Work on one module at a time:

```sh
scripts/check mesh          # gofmt, go vet, go build, go test
scripts/check -race mesh    # the same, with the race detector
scripts/check               # every module
```

When a change spans two modules, land it in dependency order, one module at a
time. For example, when `mesh` needs something new in `harness`:

1. Change `harness`, merge it, and release it (`harness/vX.Y.Z`, see below).
2. In `mesh`, run `go get github.com/hollis-labs/substrate/harness@vX.Y.Z`, then
   make the change that uses it.

To try an unreleased change before releasing it, point the consumer at your
local copy with a `replace`, and drop it again before you commit:

```sh
cd mesh
go mod edit -replace github.com/hollis-labs/substrate/harness=../harness
# ... work, run scripts/check mesh ...
go mod edit -dropreplace github.com/hollis-labs/substrate/harness
```

A module from `libs` works the same way, pointing at your clone of that
repository: `-replace github.com/hollis-labs/libs/util=<path to libs>/util`.
`scripts/check-layout` fails on any committed `replace`, so a forgotten one is
caught before it reaches `main`.

## Releasing a module

```sh
scripts/release mesh v0.1.0           # dry run: validate, print the tag mesh/v0.1.0
scripts/release --apply mesh v0.1.0   # also create that annotated tag in your clone
```

The script checks that the version is valid semver, that it is newer than the
module's latest tag, that `mesh/CHANGELOG.md` has a section for it, that the
working tree is clean and that `main` is checked out. It never pushes. A
maintainer pushes the tag (`git push origin refs/tags/mesh/v0.1.0`), and a
pushed tag is permanent: the Go module proxy caches it.

## Tooling

| Script | Purpose |
|---|---|
| `scripts/check [-race] [module...]` | gofmt, vet, build and test, per module. |
| `scripts/check-layout` | No `go.work`, no `replace`, one `go.mod` per module at `<module>/go.mod`, module paths that match their directory. |
| `scripts/check-one-way` | No reference to a forbidden module prefix (see `scripts/repo.conf`). |
| `scripts/release` | Validate a release and print its module-prefixed tag. |
| `scripts/import-repo` | Import an existing repository into a module subdirectory with its history. Needs [`git-filter-repo`](https://github.com/newren/git-filter-repo). |
| `scripts/scan-public --allowlist scan-public.allowlist` | Scan the working tree and the full history for secrets, internal hosts and addresses, and private paths. Uses `gitleaks` too when installed. |

CI runs one workflow per module, filtered to that module's paths, and a
`guards` workflow for the layout, the one-way rule and the public-hygiene scan.

## License

MIT. See [LICENSE](LICENSE). To contribute, read [CONTRIBUTING.md](CONTRIBUTING.md);
to report a vulnerability, read [SECURITY.md](SECURITY.md).
