# Conversion notes: presets that emit Substrate apps

Folio stays a command-line tool: no service, no port, no database, no keyring entries, no secrets. Its only
state is the optional user preset directory (read) and the `.folio.yaml` breadcrumb written into each rendered
project. There is therefore no data to migrate; "the port" is the presets. Substrate pins: `da9f59b` (BOM go
floor 1.26.11, `security/auth` v0.3.1) while porting; the tests and CI run against `4f82c47f3f4d`.

## What changes for whom

| Who | Change |
|---|---|
| Anyone rendering `app-dashboard` | They now get a Substrate app (`substrate.yaml`, generated wiring, identity, migrations, app-shell frontend, CI, `deploy.yaml`), not a go-webui or Chimera scaffold. `app-dashboard` 3.0.0; its inputs changed (`repo_name`; `substrate_version` and the seven kit versions have no default); `base_path` is gone (httphost has no sub-path support). |
| Anyone rendering `chat-app` | Removed with no replacement until a `kit-chat` release is pinned in the BOM (Gap 3); the owner chose removal and a follow-up adds a chat preset back. |
| Anyone rendering `go-service-app` | Removed (superseded by `substrate-app`; its transport-boundary lint gate lives on in `substrate-baseline`). |
| The Chimera presets (`app-dashboard-chimera-plugin`, `chat-app-chimera-plugin`, `chimera-plugin-fixture`) | Removed; the vendored licensed Chimera material left the tree and stays in history. |
| Projects already rendered | Untouched on disk. Their `.folio.yaml` breadcrumbs keep the old preset id and version. `folio inspect` of one that names a removed preset (or `app-dashboard`/`sysop-ui` below 3.0.0) fails with `preset_removed` naming the preset and `folio/v0.0.1`, the last release that has it; it never re-renders onto another preset. Not migrated. |
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
   BOM is on ^0.2). A Substrate chat preset has no BOM-pinned chat kit to render, so `chat-app` was removed
   rather than ported; a follow-up task adds a chat preset back when a release is pinned.
4. **app-shell is pinned at 0.1.0** (0.11.0 with responsive navigation was unpublished). The template frontend
   uses the BOM's pins. app-shell also nests two `main` landmarks (its own and the surface page frame): a shell
   issue on kit task CW-20261112-0129, not worked around in the template.
5. **The frontend toolchain has no source in the BOM.** The BOM `kits:` block pins only some `@hollis-labs`
   packages. react, react-dom, vite, tailwindcss, `@tailwindcss/vite`, biome, eslint, typescript,
   typescript-eslint, `@base-ui/react`, `lucide-react`, the `@types/*` and `@hollis-labs/eslint-config-design`
   are literals copied from Loom's `frontend/package.json` at `e809f2db451f`; drift against the BOM or the ported
   apps is not detected. Reported to the Framework Agent.
6. **Folio's CI lived where GitHub never runs it.** `folio/.github/workflows/ci.yml` is ignored (only the
   repository root workflows run), so Folio had no framework tests in CI. The root workflow
   `.github/workflows/folio-checks.yml` now runs them with `FOLIO_REQUIRE_TOOLS=1` and a hand-maintained
   `FOLIO_SUBSTRATE_VERSION` (a pseudo-version of Substrate main) until the framework mirrors its version into
   `platform-pins.yaml` (Gap 2). The old file stays because the drift check reads it; a follow-up points the drift
   check at the root workflow and deletes it.
