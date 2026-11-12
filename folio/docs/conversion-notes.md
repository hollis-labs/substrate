# Conversion notes: presets that emit Substrate apps

Folio stays a command-line tool: no service, no port, no database, no keyring entries, no secrets. Its only
state is the optional user preset directory (read) and the `.folio.yaml` breadcrumb written into each rendered
project. There is therefore no data to migrate; "the port" is the presets. Substrate pin: `da9f59b`
(BOM go floor 1.26.11, `security/auth` v0.3.1).

## What changes for whom

| Who | Change |
|---|---|
| Anyone rendering `app-dashboard` or `chat-app` | They now get a Substrate app (`substrate.yaml`, generated wiring, identity, migrations, app-shell frontend, CI, `deploy.yaml`), not a go-webui or Chimera scaffold. Major version bumps: `app-dashboard` 3.0.0, `chat-app` 2.0.0. |
| Anyone rendering `go-service-app` | Removed (superseded by `substrate-app`). |
| Projects already rendered | Untouched. Their `.folio.yaml` breadcrumbs keep the old preset id and version and stay valid as records; Folio has no sync today, so nothing reads them back. They are not migrated. |
| Unchanged | `base`, `go-baseline`, `go-lib`, `go-package`, `nanite-plugin`, `ts-plugin`. |
| The owner's installed binary | `go install .../folio/cmd/folio@<tag>` after the maintainer tags the release. Nothing else is installed or run. |

## Gaps

1. **No CI template source in the framework.** The task text says "CI from the Substrate templates"; the
   Substrate repository has none (`.github/workflows/{build,ci,pins-folio}.yml` are its own). Ruling: the preset's
   CI mirrors Mentat's and Loom's on their main branches (`ci.yml`, `build.yml` using the tools reusable
   `go-app-build.yml` pinned by commit, `deploy.yaml`). Drift against the ported apps is caught by hand until the
   framework publishes a template.
2. **`substrate pins sync --folio` mirrors only `toolchain.go_floor` and the `ci` block.** Run at `da9f59b` on a
   scratch copy: `already in step with the bom`, no change. It does not write the Substrate version pin, the
   `libs:` pins or the `kits:` pins. A preset that renders `go.mod` and `frontend/package.json` needs those
   values and must not carry a second hand-kept list. Reported to the Framework Agent via the driver.
3. **`kit-chat` is not pinned in the BOM** (its comment: 0.6.0 needs `design-bindings` and `kit-cards` ^0.1, the
   BOM is on ^0.2). The `chat-app` variant has no BOM-pinned chat kit to render until a release follows.
4. **app-shell is pinned at 0.1.0** (0.11.0 with responsive navigation is in review, unpublished). The template
   frontend uses the BOM's current pins.
