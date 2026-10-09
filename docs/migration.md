# Moving from the standalone modules

> **Harness library releases.** `harness/v0.1.0`, `harness/v0.2.0` and `harness/v0.3.0` are published; each release's notes are its section of `harness/CHANGELOG.md`. Launch planting adapters now live in `agentlaunch/planting`; the three interim workspace planting packages are removed. Native installed apply and launch-readiness evidence remain adoption requirements, not capabilities supplied by these library releases.

This page lists the Hollis Labs modules that moved into the `substrate` and `libs` repositories, what each one became, and what to expect when a consumer adopts the new module paths. It states what exists; it sets no schedule and promises nothing to any consumer. Import destinations updated 2026-10-07. Old repository tag observations and application dry-run results below retain their original 2026-10-03 scope.

The first sections are the version table. [Adoption notes](#adoption-notes) follow it.

## Version table

One row per old module. **Last old tag** is the highest semantic-version tag of the old repository. **First new version** is the first tag of the new module, listed only if the tag exists. **Migration note** is the per-unit note that lists every old and new import path of that module. Old release tags were not carried into the new repositories, so a new module starts at its own first version, not at the old number.

### llm-core (7 old modules)

New module: `github.com/hollis-labs/substrate/llm-core`.

| Old repository | Last old tag | New import prefix | First new version | Migration note |
|---|---|---|---|---|
| [`agent-contracts-leaf`](https://github.com/hollis-labs/agent-contracts-leaf) | `v0.3.0` | `github.com/hollis-labs/substrate/llm-core/contracts` | `llm-core/v0.1.0` | [note](../llm-core/contracts/MIGRATION.md) |
| [`go-modelsdev-catalog-helpers`](https://github.com/hollis-labs/go-modelsdev-catalog-helpers) | `v0.1.0` | `github.com/hollis-labs/substrate/llm-core/costcalc` | `llm-core/v0.1.0` | [note](../llm-core/costcalc/MIGRATION.md) |
| [`go-embed-contracts`](https://github.com/hollis-labs/go-embed-contracts) | `v0.1.1` | `github.com/hollis-labs/substrate/llm-core/embedcontracts` | `llm-core/v0.1.0` | [note](../llm-core/embedcontracts/MIGRATION.md) |
| [`go-llm-contracts`](https://github.com/hollis-labs/go-llm-contracts) | `v0.4.0` | `github.com/hollis-labs/substrate/llm-core/llmcontracts` | `llm-core/v0.1.0` | [note](../llm-core/llmcontracts/MIGRATION.md) |
| [`go-llm-types`](https://github.com/hollis-labs/go-llm-types) | `v0.5.1` | `github.com/hollis-labs/substrate/llm-core/llmtypes` | `llm-core/v0.1.0` | [note](../llm-core/llmtypes/MIGRATION.md) |
| [`go-modelsdev`](https://github.com/hollis-labs/go-modelsdev) | `v0.3.0` | `github.com/hollis-labs/substrate/llm-core/modelsdev` | `llm-core/v0.1.0` | [note](../llm-core/modelsdev/MIGRATION.md) |
| [`go-usage-ledger`](https://github.com/hollis-labs/go-usage-ledger) | `v0.1.0` | `github.com/hollis-labs/substrate/llm-core/usageledger` | `llm-core/v0.1.0` | [note](../llm-core/usageledger/MIGRATION.md) |

### mesh (4 old modules)

New module: `github.com/hollis-labs/substrate/mesh`.

| Old repository | Last old tag | New import prefix | First new version | Migration note |
|---|---|---|---|---|
| [`go-federation`](https://github.com/hollis-labs/go-federation) | `v0.1.0` | `github.com/hollis-labs/substrate/mesh/federation` | `mesh/v0.1.0` | [note](../mesh/federation/MIGRATION.md) |
| [`go-hitl`](https://github.com/hollis-labs/go-hitl) | `v0.1.0` | `github.com/hollis-labs/substrate/mesh/hitl` | `mesh/v0.1.0` | [note](../mesh/hitl/MIGRATION.md) |
| [`go-messaging`](https://github.com/hollis-labs/go-messaging) | `v0.7.0` | `github.com/hollis-labs/substrate/mesh/messaging` | `mesh/v0.1.0` | [note](../mesh/messaging/MIGRATION.md) |
| [`go-tether-client`](https://github.com/hollis-labs/go-tether-client) | `v0.10.0` | `github.com/hollis-labs/substrate/mesh/tetherclient` | `mesh/v0.1.0` | [note](../mesh/tetherclient/MIGRATION.md) |

### harness (11 old modules)

New module: `github.com/hollis-labs/substrate/harness`. Each package of an old module has its own new home; the note of a module with several homes lists all of them. The packages under `harness/internal/` are not importable from outside the module and have no public import path.

| Old repository | Last old tag | New import prefix | First new version | Migration note |
|---|---|---|---|---|
| [`agentkit`](https://github.com/hollis-labs/agentkit) | `v0.26.1` | several homes under `github.com/hollis-labs/substrate/harness/`: `adapters`, `agentcomposition`, `agentlaunch`, `broker`, `workspace`; `agentcontext` is `github.com/hollis-labs/substrate/agent/agentcontext` (from `harness/v0.3.0` and `agent/v0.1.0`) | `harness/v0.1.0` | [note](../harness/docs/units/agentkit/MIGRATION.md) |
| [`go-agent-wrapper`](https://github.com/hollis-labs/go-agent-wrapper) | `v0.28.0` | several homes under `github.com/hollis-labs/substrate/harness/`: `adapters`, `interception`, `sandbox`, `workspace` | `harness/v0.1.0` | [note](../harness/docs/units/go-agent-wrapper/MIGRATION.md) |
| [`go-egress-proxy`](https://github.com/hollis-labs/go-egress-proxy) | `v0.2.4` | `github.com/hollis-labs/substrate/harness/interception` | `harness/v0.1.0` | [note](../harness/docs/units/go-egress-proxy/MIGRATION.md) |
| [`go-harness-filters`](https://github.com/hollis-labs/go-harness-filters) | `v0.1.1` | `github.com/hollis-labs/substrate/harness/interception` | `harness/v0.1.0` | [note](../harness/docs/units/go-harness-filters/MIGRATION.md) |
| [`go-materialize`](https://github.com/hollis-labs/go-materialize) | `v0.1.0` | `github.com/hollis-labs/substrate/harness/workspace` | `harness/v0.1.0` | [note](../harness/docs/units/go-materialize/MIGRATION.md) |
| [`go-permission`](https://github.com/hollis-labs/go-permission) | `v0.1.0` | `github.com/hollis-labs/substrate/harness/interception` | `harness/v0.1.0` | [note](../harness/docs/units/go-permission/MIGRATION.md) |
| [`go-providers`](https://github.com/hollis-labs/go-providers) | `v0.46.0` | `github.com/hollis-labs/substrate/harness/adapters` | `harness/v0.1.0` | [note](../harness/docs/units/go-providers/MIGRATION.md) |
| [`go-runner`](https://github.com/hollis-labs/go-runner) | `v0.8.2` | `github.com/hollis-labs/substrate/harness/runner` | `harness/v0.1.0` | [note](../harness/docs/units/go-runner/MIGRATION.md) |
| [`go-runtime-events`](https://github.com/hollis-labs/go-runtime-events) | `v0.2.2` | `github.com/hollis-labs/substrate/harness/adapters` | `harness/v0.1.0` | [note](../harness/docs/units/go-runtime-events/MIGRATION.md) |
| [`go-safefs`](https://github.com/hollis-labs/go-safefs) | `v0.1.0` | `github.com/hollis-labs/substrate/harness/sandbox` | `harness/v0.1.0` | [note](../harness/docs/units/go-safefs/MIGRATION.md) |
| [`go-sandbox`](https://github.com/hollis-labs/go-sandbox) | `v0.6.0` | `github.com/hollis-labs/substrate/harness/sandbox` | `harness/v0.1.0` | [note](../harness/docs/units/go-sandbox/MIGRATION.md) |

### libs: util (12 old modules)

New module: `github.com/hollis-labs/libs/util`, in the [libs repository](https://github.com/hollis-labs/libs).

| Old repository | Last old tag | New import prefix | First new version | Migration note |
|---|---|---|---|---|
| [`go-apppaths`](https://github.com/hollis-labs/go-apppaths) | `v0.3.0` | `github.com/hollis-labs/libs/util/apppaths` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/apppaths/MIGRATION.md) |
| [`go-localdaemon`](https://github.com/hollis-labs/go-localdaemon) | `v0.1.0` | `github.com/hollis-labs/libs/util/localdaemon` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/localdaemon/MIGRATION.md) |
| [`go-otel`](https://github.com/hollis-labs/go-otel) | `v0.10.0` | `github.com/hollis-labs/libs/util/otel` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/otel/MIGRATION.md) |
| [`go-queue`](https://github.com/hollis-labs/go-queue) | `v0.2.1` | `github.com/hollis-labs/libs/util/queue` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/queue/MIGRATION.md) |
| [`go-scheduler`](https://github.com/hollis-labs/go-scheduler) | `v0.3.0` | `github.com/hollis-labs/libs/util/scheduler` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/scheduler/MIGRATION.md) |
| [`go-sftpsync`](https://github.com/hollis-labs/go-sftpsync) | `v0.1.1` | `github.com/hollis-labs/libs/util/sftpsync` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/sftpsync/MIGRATION.md) |
| [`go-sqlite`](https://github.com/hollis-labs/go-sqlite) | `v0.1.0` | `github.com/hollis-labs/libs/util/sqlite` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/sqlite/MIGRATION.md) |
| [`go-sqlite-backup`](https://github.com/hollis-labs/go-sqlite-backup) | `v0.1.0` | `github.com/hollis-labs/libs/util/sqlitebackup` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/sqlitebackup/MIGRATION.md) |
| [`go-strutil`](https://github.com/hollis-labs/go-strutil) | `v0.1.0` | `github.com/hollis-labs/libs/util/strutil` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/strutil/MIGRATION.md) |
| [`go-svcerr`](https://github.com/hollis-labs/go-svcerr) | `v0.1.0` | `github.com/hollis-labs/libs/util/svcerr` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/svcerr/MIGRATION.md) |
| [`go-transportparity`](https://github.com/hollis-labs/go-transportparity) | `v0.1.0` | `github.com/hollis-labs/libs/util/transportparity` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/transportparity/MIGRATION.md) |
| [`go-worktree`](https://github.com/hollis-labs/go-worktree) | `v0.1.0` | `github.com/hollis-labs/libs/util/worktree` | `util/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/util/worktree/MIGRATION.md) |

### libs: ui-go (6 old modules)

New module: `github.com/hollis-labs/libs/ui-go`, in the [libs repository](https://github.com/hollis-labs/libs).

| Old repository | Last old tag | New import prefix | First new version | Migration note |
|---|---|---|---|---|
| [`go-chatstream`](https://github.com/hollis-labs/go-chatstream) | `v0.1.0` | `github.com/hollis-labs/libs/ui-go/chatstream` | `ui-go/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/ui-go/chatstream/MIGRATION.md) |
| [`go-directives`](https://github.com/hollis-labs/go-directives) | `v0.1.0` | `github.com/hollis-labs/libs/ui-go/directives` | `ui-go/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/ui-go/directives/MIGRATION.md) |
| [`go-envelopes`](https://github.com/hollis-labs/go-envelopes) | `v0.5.0` | `github.com/hollis-labs/libs/ui-go/envelopes` | `ui-go/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/ui-go/envelopes/MIGRATION.md) |
| [`go-ssekit`](https://github.com/hollis-labs/go-ssekit) | `v0.2.0` | `github.com/hollis-labs/libs/ui-go/ssekit` | `ui-go/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/ui-go/ssekit/MIGRATION.md) |
| [`go-streamhub`](https://github.com/hollis-labs/go-streamhub) | `v0.1.0` | `github.com/hollis-labs/libs/ui-go/streamhub` | `ui-go/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/ui-go/streamhub/MIGRATION.md) |
| [`go-webui`](https://github.com/hollis-labs/go-webui) | `v0.2.0` | `github.com/hollis-labs/libs/ui-go/webui` | `ui-go/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/ui-go/webui/MIGRATION.md) |

### libs: workflow (2 old modules)

New module: `github.com/hollis-labs/libs/workflow`, in the [libs repository](https://github.com/hollis-labs/libs). `go-workflow-host` is the `host/` directory of the same module.

| Old repository | Last old tag | New import prefix | First new version | Migration note |
|---|---|---|---|---|
| [`go-workflow`](https://github.com/hollis-labs/go-workflow) | `v0.1.0` | `github.com/hollis-labs/libs/workflow` and its subpackages | `workflow/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/workflow/MIGRATION.md) |
| [`go-workflow-host`](https://github.com/hollis-labs/go-workflow-host) | `v0.1.0` | `github.com/hollis-labs/libs/workflow/host` | `workflow/v0.1.0` | [note](https://github.com/hollis-labs/libs/blob/main/workflow/host/MIGRATION.md) |

`agent` is not part of this table: it holds no code from an old module.

## Adoption notes

**Go version.** Every module in `substrate` and `libs` declares `go 1.26.6`. A consumer that builds with an older toolchain needs the newer one (or the toolchain download Go performs from the `go` directive) before it can use them.

**The old repositories stay.** The move did not change or delete anything in the old repositories. Each one still exists with its tags (checked on the code host when this page was written) and still resolves under its old module path, so a consumer can stay on an old version for as long as it wants. The moved code is the old repository's main branch at the time of the move, which can be ahead of its last tag; each migration note names the source commit and the number of commits whose history moved with it.

**The import rewrite is mechanical.** An import path changes by prefix; package names, symbols and APIs are not renamed, and every package keeps the package clause it had (except where a module's CHANGELOG says otherwise, for example the root of `llm-core/contracts`). The migration notes list old and new import paths. Harness consolidation also removes the bootdir writer and moves planting contracts; its current changes below and CHANGELOG take precedence over the original mechanical import. `go get github.com/hollis-labs/<repository>/<module>@<version>` replaces `go get` of the old module; the new module path is in each group heading above.

**What a dry-run rewrite of existing applications found.** The `llm-core` and `harness` rows were applied, without committing or pushing anything, to six Hollis Labs applications against the merged `substrate` tree, with their build, `go vet` and test compilation compared before and after. The `harness` rows were rehearsed at the 2026-10-03 imported layout, before workspace consolidation and authority API changes. These historical results do not establish current application adoption:

- `torque`, `tether`, `cairn` and `folio`: no edit beyond the import paths. Build, vet and test compilation pass.
- `hadron`: 12 compile errors, all in one file, `internal/agentsubstrate/launcher.go`. The application pins `agentkit` v0.6.1 and was written against that API; the rewrite moves it to the current one in one step. `agentkit` v0.12.0 removed `agentlaunch.RuntimeKind` and its constants and replaced them with `runtimes.Mode` of `github.com/hollis-labs/substrate/llm-core/contracts/runtimes`: `RuntimeKind` becomes `runtimes.Mode`, `RuntimeSubprocess` becomes `runtimes.ModeSubprocessPerTurn` (the spelling `subprocess` is now `subprocess-per-turn`), `RuntimeServeHTTP` becomes `runtimes.ModeHTTPSSE` (`serve-http` is now `http-sse`), and `RuntimeStreamingStdio`, `RuntimePTY` and `RuntimeJsonRpcStdio` become `runtimes.ModeStreamingStdio`, `runtimes.ModePTY` and `runtimes.ModeJSONRPCStdio`. The package `agentruntime/runtimekind` was removed in the same release and has no rewrite rule; its `PTYDebug` has no replacement (`pty-debug` is `pty` plus the debug posture). The edit is a code change in that one file. Packages that depend on it were not type-checked, so further differences can appear after it.
- `nanite`: one error site, `internal/service/container.go`, where a value of the new `llm-core/embedcontracts.Embedder` type is passed to `tesseract.WithEmbedder`. `nanite` pins `tesseract` v0.10.0, whose API still takes the old `go-embed-contracts` type. `nanite` can adopt the new paths once `tesseract` has moved onto `llm-core` and has a release that `nanite` can pin.

**What the dry run left alone.** It covered only the `llm-core` and `harness` rows. Imports of the `mesh` and `libs` modules in the table (for example `go-messaging`, `go-otel`, `go-workflow`) were left as they are, and so were imports of old modules that are not in the table at all (for example `go-hooks`).

**Known issues in `harness`.** The `TestSandbox_OutsideWorkspaceReadBlocked` test in `harness/sandbox` is skipped unconditionally on Linux: the skip says the test needs additional bind configuration, and it has said so since the sandbox was first extracted, before the move. So the tests do not verify that a sandboxed process on Linux cannot read a denied path outside its workspace. That is not known to be broken; it is not verified. On macOS the same test runs only when `~/.ssh` exists. The other Linux sandbox isolation tests run and pass in CI. A second caveat concerns the Codex turn lifecycle: the wrapper in `harness/adapters/wrapper` takes the end of a Codex JSON-RPC stdio turn from the session's typed terminal event, which restored the native turn-order test that was skipped earlier in this move, but only when the CLI adapter is named `codex`. The session layer emits that typed terminal only for an adapter with that name, so a compatible custom adapter that speaks the same protocol under another name does not get it, and the fix does not cover it. Both statements are in `harness/CHANGELOG.md`.

**Behaviour changes already recorded in the module CHANGELOGs.** The `v0.1.0` section of each module's `CHANGELOG.md`, `harness` included, lists what a consumer can notice. The ones that are not import paths:

- `libs/util`: the OpenTelemetry instrumentation scope names of `otel` and `otel/genai` are now the new import paths, so exported traces and metrics carry a new `otel.scope.name`; dashboards or alerts keyed on the old name need updating. One `go.mod` can require only one version of a dependency, so the highest version any imported library asked for won; the raised versions (for example `modernc.org/sqlite` from v1.48.1 to v1.60.1) are listed in the CHANGELOG.
- `libs/ui-go`: `envelopes.ModulePath` is now `github.com/hollis-labs/libs/ui-go`, the module that owns the package, instead of the old repository path; the catalog's source identity and the module line of generated TypeScript report the new value, so regenerating output changes those lines. `go-chatstream` required `go-ssekit` and `go-streamhub` as separate modules; they are now ordinary imports of `ui-go/ssekit` and `ui-go/streamhub`.
- `libs/workflow`: the module now also requires `modernc.org/sqlite`, because `host/` is part of it; the core imports none of it, but dependants of the module have the requirement in their module graph.
- `substrate/llm-core`: the package clause of the root of `contracts` changed from `agentcontracts` to `contracts`; `status.go` (`InstanceStatus` and related types) was removed from `contracts` because the mesh contracts supersede it.
- `substrate/mesh`: `federation` and `tetherclient` import `messaging` as a package of the same module instead of requiring the old `go-messaging` module.
- `substrate/harness`: code that pinned a sibling module at a tag now builds against the sibling's packages in the same module, at the source of its old repository's main branch (for example the wrapper, which pinned `agentkit` v0.21.0, now builds against `agentkit` v0.26.1); the `harness/CHANGELOG.md` entries under Changed list each pin that moved. The Linux sandbox loopback helper no longer fails with `operation not permitted` when `bwrap` has already brought `lo` up.

## Current harness consolidation

The module pins `llm-core` and `mesh` v0.1.0 and, from `harness/v0.3.0`,
`agent` v0.1.0. Its releases, starting with `harness/v0.1.0`, use the module
path `github.com/hollis-labs/substrate/harness`.

| Removed interim import | Current caller boundary |
|---|---|
| `harness/workspace/plant` | `harness/agentlaunch/planting`: `Planter`, `PlantSpec`, `PlantResult`, `PlantHook`, `SharedPlanter`, `NoOpPlanter` |
| `harness/workspace/providerplant` | `harness/agentlaunch/planting`: provider projection/preparation and `PlantContextFor` |
| `harness/workspace/bootdir` | No public writer replacement; submit explicit artifacts through `agentlaunch.MaterializeArtifacts` with artifact authority |

All paths in this table are relative to `github.com/hollis-labs/substrate/`.
Update the package qualifier to `planting`; wrapper `Config.Planter` and
`Config.PlantSpec` retain their roles with the relocated types. Provider
projection is pure; materializing preparation requires an explicit
`ArtifactAuthorizer`. Engine overrides and the `AtomicWrite` side-writer
are removed without a compatibility authority adapter. Five session start
paths similarly require explicit artifact root and authority. See the complete
removed-field list in [harness/CHANGELOG.md](../harness/CHANGELOG.md).

Real native installed mutation is Unsupported; the private metadata consumer
has no production issuer. Publication/readiness needs trusted host evidence
that the library does not manufacture. Existing operator directories are not
adopted or chmodded, and uncertain roots/receipts remain recovery obligations.
These support limits, runtime validation and consumer migration are adoption
work; a library tag does not establish them. See
[harness/README.md](../harness/README.md).
