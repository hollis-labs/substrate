# Surface apps: surface-app and surface-desk

Two presets scaffold an app whose interface is built the way Surface's `docs/gui.md` sets out
(github.com/hollis-labs/surface): anything that draws comes from the `@surface` registry, a look is changed
in the registry or the theme and never in the app, and the tooling enforces it.

| Preset | What it renders |
|---|---|
| `surface-app` | A plain web app: Vite, React and Tailwind, the app frame (`app-frame`), a starter view and a gallery page at `#/gallery`. No server. |
| `surface-desk` | A desk app: a Go server on `github.com/hollis-labs/surface/app` with a starter module (`modules/start`: one window, one agent tool), and its page on `@hollis-labs/surface-page` (windows that tile, tab and float, the palette, the agent panel, cards). The gallery is the Components window in the apps menu. |
| `surface-ui` | The layer both compose (`layer_only`). |

## Generate

```sh
folio new surface-app notes-web --non-interactive --input repo_name=notes-web
folio new surface-desk notes-desk --non-interactive --input repo_name=notes-desk --input port=8300

cd notes-web
make setup     # npm install, then npx surface-ui add <the app's items>
make check     # npx surface-ui verify, lint, typecheck, build (desk: go vet, go test, go build)
```

Then commit what `make setup` wrote: `package-lock.json`, `ui.lock.json`, `src/components/` (desk: `go.sum`).

| Input | Default | |
|---|---|---|
| `repo_name` | required | package name; for a desk app also the binary, the app name and `<APP>_DATA_DIR` |
| `title` | from `repo_name` | |
| `surface_version` | `0.1.0` | the Surface release (see below) |
| `github_owner` | `hollis-labs` | desk: the Go module path `github.com/<owner>/<repo_name>` |
| `port` | `8300` | desk: the server's loopback port and the dev server's proxy target |

## One release pins everything

Surface has one version line: the tag `vX.Y.Z` is the registry's ref, the version of every
`@hollis-labs/surface-*` npm package and of the Go module. From `surface_version`:

| Where | What |
|---|---|
| `ui.lock.json` | `"registry": "https://raw.githubusercontent.com/hollis-labs/surface/{ref}/registry/r/{name}.json"`, `"ref": "v<surface_version>"`, `"uiDir": "src/components/ui"` |
| `package.json` | `@hollis-labs/surface-ui` (dev); desk: `@hollis-labs/surface-page`, `-chat`, `-cards`, `-genui`. All exact. |
| `go.mod` (desk) | `require github.com/hollis-labs/surface v<surface_version>` |

The other packages (React, Vite, Tailwind, Biome, Base UI, `cn`, `flexlayout-react`, ...) are exact literals
from Surface's `package-lock.json` at `v0.1.0`, the versions that release was built and tested with. Moving to a
new Surface release moves them with it: update the preset, and in an app, set `ref` and the package versions
together, then `npx surface-ui sync` and `make check` (the generated `AGENTS.md` says so).

## What enforces the rules

- **Lint.** `biome.json` has `noRestrictedElements` for `button`, `input`, `select` and `textarea` over
  `src/**`, excluding `src/components/ui` (the installed copies may use them). A raw `<button>` in a view fails
  `npm run lint`.
- **Verify.** `npx surface-ui verify` (in `make check` and CI) fails when an installed file differs from the
  registry at the lock's ref: an edit to `src/components/ui/button.tsx` fails it.
- **Instructions.** `AGENTS.md` carries the short form of `docs/gui.md` and the commands, and points at the
  Surface repository for the full guide.

## Whose files

| Path | Whose | `folio sync` |
|---|---|---|
| `ui.lock.json`, `src/components/**` | surface-ui's (`make setup`, `npx surface-ui add/sync`) | never writes (`skip`) |
| `src/index.css`, `README.md` | the person's once generated | `skip` |
| `src/views/**`, `src/gallery/**`, `src/App.tsx` (plain) | the person's | `skip` |
| `modules/**`, `src/modules/**`, `go.mod`, `go.sum` (desk) | the person's | `skip` |
| everything else (`package.json`, `vite.config.ts`, `biome.json`, CI, `Makefile`, `AGENTS.md`, desk `main.go`) | the preset's | `prompt`: written when unedited, a conflict when edited |

## Which components

The plain app installs `theme app-frame button input dialog card` (and what they depend on). The desk app
installs what `@hollis-labs/surface-page` draws with (`--for @hollis-labs/surface-page`) and the rest of the
registry, because the Components window draws every item. Any other component: `npx surface-ui add <name>`.

## Testing

`TestSurfacePresets_PinOneRelease` and `TestSurfacePresets_LintAndCI` render both presets and check the pins,
the lint rule, CI and the instructions. `TestSurfacePresets_Acceptance` installs and builds rendered apps
against the published release and checks that a raw `<button>` fails lint and an edited component fails
verify. It needs npm and the network: `FOLIO_SURFACE_ACCEPTANCE=1` (plain app) or `=all` (and the desk app).

## Starter agent directory

`app-ade` and `surface-desk` generate `agents.dir: agents/` in app config and
`agents/assistant/agent.md` (agentdef v2, durable continuity) and `host.yaml`
(embedded, application lifetime, the app's name). `folio sync` never writes `agents/**`.
No Nanite built-in tool group is enabled. `app-chat`, `surface-app` and the other
presets generate neither the directory nor the config key.

The generated app uses Substrate's `agents.Resolve` after operator config merges,
publishing `agent-directory.layout` on the Substrate app. Relative `agents.dir`
paths are beside `SUBSTRATE_CONFIG` when set; with embedded config, they are beside
`substrate.yaml` in the working directory, so a generated checkout uses `./agents/`.
For an installed app, set an absolute path or install the directory beside the
operator config. Runtime paths resolve separately under
`$XDG_STATE_HOME/<app>/agents/<name>` (default `~/.local/state/<app>/agents/<name>`).
Resolution creates no files and opens no agents.

**Nothing in the app opens these files yet.** They become live when chat wiring
lands; app-ade continues to use libs/agent/host and surface-desk uses Surface's Engine.
The Substrate helper requires `v0.1.1-0.20261119145946-6a4bb696c942` or newer;
surface-desk pins it directly, and app-ade's explicit `substrate_version` input must
include the helper.
