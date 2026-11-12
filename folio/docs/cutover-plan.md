# Cutover plan: Folio presets on Substrate

Folio is a one-shot command-line tool: no service, no port, no database, no keyring entry, no secret
(survey in `docs/conversion-notes.md`, #17). The cutover is therefore a release, not a data migration.
Nothing here touches a live system; the owner merges and a maintainer pushes the tag.

Status: **not started** (pull requests #21, #24, #25 and this one are drafts).

## 1. Data

None to migrate. The only state is the optional `~/.folio/presets/local` directory (read by Folio, never
written) and the `.folio.yaml` breadcrumb inside each project Folio rendered. Existing breadcrumbs keep
their old preset ids and versions and stay on disk as records; they are not rewritten and nothing syncs
them (`folio sync` is reserved). What the new `folio` does with them: `folio inspect` of a project whose
breadcrumb names a removed preset (`go-service-app`, `chat-app`, `app-dashboard-chimera-plugin`,
`chat-app-chimera-plugin`, `chimera-plugin-fixture`, or `app-dashboard`/`sysop-ui` below 3.0.0) fails with
`preset_removed` and one sentence naming the preset, its version and `folio/v0.0.1`, the last release that
has it, writes nothing and never re-renders it onto the new `app-dashboard`; `folio new` and `folio plan`
of a removed id say the same. To inspect such a project, install that release. Breadcrumbs from the
unchanged presets (`base`, `go-baseline`, `go-lib`, `go-package`, `nanite-plugin`, `ts-plugin`) inspect
as before. Projects already generated from the go-webui or Chimera presets are not touched and are not
migrated by this change.

## 2. Callers and what changes for each

| Caller | Today | After |
|---|---|---|
| The owner's `folio` binary (installed with `go install`, dated 29 September) | old presets | reinstall: `go install github.com/hollis-labs/tools/folio/cmd/folio@<tag>`; `folio preset list` shows `substrate-app`, `app-dashboard` 3.0.0 and no Chimera presets |
| Anyone rendering `app-dashboard` or `chat-app` | go-webui or Chimera scaffold | `app-dashboard` 3.0.0 renders a Substrate app; the inputs changed (`repo_name`, `substrate_version`, kit versions: no defaults); `chat-app` is gone until a `kit-chat` release is pinned in the Substrate BOM |
| Anyone rendering `go-service-app` | composition root plus transport gate | `substrate-app` (the transport gate is in `substrate-baseline`) |
| Scripts, units, Tether/Cerberus catalog, fleet-install | none found (the primary host has no folio binary, unit or `~/.folio`; no catalog or installer entry calls `folio`) | no change |
| `hollis-labs/chimera` | consumed by the old presets | no longer consumed by Folio: its retirement can proceed once Parallax is also off it (program plan) |

Unchanged presets: `base`, `go-baseline`, `go-lib`, `go-package`, `nanite-plugin`, `ts-plugin`.

## 3. Config and supervision

Nothing to remove: no unit, no descriptor, no environment of the tool itself (only the per-input
`FOLIO_INPUT_<NAME>` overrides). No Cerberus apply, no hub reload, no gateway catalog change.

## 4. Release

1. Merge #17, #21, #24, #25 (and #22) in the owner's order; #24 and #25 are stacked, retarget to `main`.
2. In `folio/CHANGELOG.md` turn `## Unreleased` into the release section (`## v0.1.0 - <date>`;
   pre-1.0, a breaking change takes the minor). `scripts/release` refuses a tag without that heading.
3. On `main`: `scripts/release folio v0.1.0` (dry run, validates), then `scripts/release --apply folio v0.1.0`
   (creates the tag locally; the script never pushes).
4. The maintainer pushes `folio/v0.1.0` and creates the GitHub release. No CI artifact or install step
   is involved (Folio is installed with `go install`).
5. The owner reinstalls (section 2) and renders one app with the real versions, then runs it (below).

Before step 3, the evidence the pull requests carry must hold on `main`: `go vet`, `go test -race` with
`FOLIO_REQUIRE_TOOLS=1` and `FOLIO_SUBSTRATE_VERSION` set, `golangci-lint v2.11.4`, the pins drift check
and its self-test, `substrate pins check --folio`, and `substrate scan-public` (tree: no finding of
Folio's; history: inherited findings only).

## 5. Rollback

Reinstall the previous tag: `go install github.com/hollis-labs/tools/folio/cmd/folio@v0.0.1`. There is
nothing on disk to restore: Folio holds no state, and projects rendered with the new version are
independent of the binary. A bad tag is superseded by a patch tag, not rewritten.

## 6. Verify

With the installed binary and no state on the machine:

1. `folio preset list`: `substrate-app`, `app-dashboard` 3.0.0 present; no `chimera-*`, `go-service-app`,
   `chat-app`.
2. Render `app-dashboard` into a scratch directory with `substrate_version` and the seven `kit_*`
   versions taken from the framework's `bom.yaml`; follow the rendered README's first build
   (`go mod tidy`, `substrate pins sync --app .`, `go mod tidy`, `npm install` in `frontend/`, commit the lock).
3. Create the owner offline (`<name> identity setup`), start with `<name> serve` on a free port and a
   temporary data directory, sign in, add an item, read it back; an unknown `Host` is refused;
   `SIGTERM` stops it. The integration tests (`TestSubstrateApp_BuildsAndRuns`,
   `TestAppDashboard_BuildsAndServesUI`) do this on every run.

## 7. Approvals

The owner merges every pull request in `hollis-labs/tools`. The tag push and the GitHub release are the
maintainer's. The sub-folio agent does not push tags and does not run anything against a live system.

## 8. Known open items at cutover

- The framework version and the `@hollis-labs` kit versions are mirrored nowhere in Folio
  (Substrate `pins sync --folio` writes only the Go floor and the CI versions): the integration tests
  read them from `bom.yaml`, and Folio's own CI carries a hand-maintained `FOLIO_SUBSTRATE_VERSION`.
- The frontend toolchain versions (react, vite, tailwind, biome, eslint, typescript, base-ui, lucide-react,
  the design lint config) have no source in the BOM; they are copied from Loom's `package.json`.
- `kit-chat` is not pinned in the BOM, so there is no chat preset.
- `internal/hostguard` in the rendered app is a per-app copy of a check the framework will own
  (Substrate pull request 23); the preset drops it when that lands.
- `folio/.github/workflows/ci.yml` is not executed by GitHub (only the repository root workflows run);
  the root `modules.yml` runs vet and tests for Folio without the framework environment.
