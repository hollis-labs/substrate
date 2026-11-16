# Cutover and installed CLI verification

The preset conversion is code and a local CLI replacement, not a live application migration.
Chrispian and the driver review and land the slices. No tag, release publication, service cutover,
Cerberus apply or fleet install is part of this completion assignment.

## Preserve existing projects

Existing `.folio.yaml` breadcrumbs remain untouched. Removed `chat-app`, `go-service-app`, Chimera
and pre-3.0 dashboard presets still fail explicitly with `preset_removed`. Fresh chat generation uses
`app-chat`; there is no implicit migration. `folio sync` updates a project within its own preset, never across presets.

The optional user preset directory is read as before. Folio has no database or daemon to migrate.
Its new own `project.yaml` describes current behavior and does not install a runtime or policy gate.

## Install a reviewed main build

After every completion slice is reviewed and landed, the driver selects the exact clean main SHA
and records it. Before replacing the executable, record the current PATH resolution, checksum and
`go version -m` output; copy the existing executable to a recoverable backup with its mode preserved.
Build `./cmd/folio` from the monorepo's `folio` module into a temporary file alongside the selected
PATH destination, verify it, then atomically replace the destination. Record the new checksum, source
SHA, version output and backup location in the task report. This does not create a release tag.

Use the installed executable to list presets and show `app-chat`, `substrate-cli` and `substrate-service`.
Render a scratch project with current explicit framework/BOM inputs. Verify its descriptor, inspect
result, deployment preview and the explicit monorepo path/workflow behavior. The required-tool test suite
already builds and runs generated apps with identity/auth/isolation checks; do not apply a live service
just to prove the CLI replacement.

For rollback, restore the saved executable atomically and verify PATH resolution and checksum against
the recorded original. Generated projects are separate files and are not rolled back by replacing Folio.

## Release separately

Modules use `folio/vX.Y.Z` tags in `hollis-labs/tools`. A future release updates the changelog, runs the
repository's release dry run, and follows the maintainer's release authorization. Never infer a release
number from an old cutover note or retag an existing release. The driver owns the final installation
report; this document does not claim installation has already happened.

See [conversion-notes.md](./conversion-notes.md) for remaining framework/catalog boundaries.
