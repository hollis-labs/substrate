# Compatibility policy

What a Substrate version promises, what breaks it, and how an app moves between versions.
`substrate upgrade` enforces the mechanical half of this document.

## What is covered

The promise applies to everything an app or a generated binary can depend on:

| Surface | Where |
|---|---|
| Go packages an app imports: `module`, `boot`, `catalog`, `manifest`, `ui`, `apierr`, `frontend`, `bom`, and the exported API of every `modules/<name>` | the module path `github.com/hollis-labs/substrate` |
| The manifest schema | `substrate.yaml`, parsed strictly by `manifest` |
| The contribution manifest | `ui.json`, `ui.SchemaVersion` |
| The generated frontend manifest | `frontend.ManifestVersion` |
| The wire conventions Substrate serves | the flat error body `{code, message, details?}`, `GET /api/me`, `GET /api/capabilities`, the `GET /api/events` SSE frames, `/healthz` |
| Pins | every version in `bom/bom.yaml` |
| Config keys each module reads | the config block in each module's doc comment |

Internal helpers, test files and anything unexported are not covered.

## Versions

Semantic versioning, from the first release.

- **Patch** (`0.1.0` to `0.1.1`): fixes only. Safe to take unattended.
- **Minor**: new modules and options. **Before 1.0 a minor release may break an app**; from 1.0 it may not.
- **Major**: may break.
- A pre-release or pseudo-version (an untagged commit) promises nothing.

A change to a pinned library follows that library's own semver: a **major** bump of a pinned lib is a breaking
change for Substrate, a minor or patch bump is not. Changing a pin never needs an app edit; only the Substrate
requirement in the app's `go.mod` moves.

## What counts as breaking

Removing or renaming anything in the table above, tightening what it accepts, changing what it returns, or
changing the default of a security-relevant setting. Concretely: a changed scope name, a route moved or removed,
a manifest field removed or newly required, a different error code, a config key renamed, a module's `Requires`
growing.

Adding an optional field, a new module, a new route, or a new profile is not breaking.

## How a breaking change ships

1. The release's `CHANGELOG.md` section has a `### Breaking` heading. Each bullet ends with
   `(migration: <id>)` naming a registered mechanical migration, or `(migration: none)` plus the manual step.
2. A mechanical migration is a function in `upgrade.Migrations`, introduced at that release, that rewrites the
   app's files through a transaction that can be rolled back.
3. The conformance suite runs across every module and profile before the tag, and CI runs the app-level bump
   test (an app that requires Substrate only resolves every pin to the BOM).

A release is not tagged while a breaking bullet names nothing.

## Moving an app: `substrate upgrade`

```sh
substrate upgrade -dry-run            # report only: pin changes, changelog, migrations, manual steps
substrate upgrade                     # to the latest release
substrate upgrade -to v0.3.0
```

It refuses when the app replaces Substrate with a local path, when the target is older, when a breaking change
has no mechanical migration, or when a move that may break has no changelog entries to check. `-allow-unmigrated`
overrides the last two once you have read the changelog and made the manual changes.

On a run it applies the migrations, moves the one Substrate requirement (`go get`, `go mod tidy`), validates the
app's manifest with the *target* release's CLI, and builds the app. If any step fails it restores `go.mod`,
`go.sum` and every file a migration wrote, and says so.

## Support

Until 1.0, only the latest minor line receives fixes. After 1.0 the policy for older lines will be written here
before the first 1.x release.
