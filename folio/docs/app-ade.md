# app-ade

`app-ade` renders a class 1 Substrate app where a person and an agent work over the same surface,
built on the ADE packages: `@hollis-labs/ade-core` (the hub and the agent's tools),
`@hollis-labs/ade-agent` (the page's side of the conversation), `@hollis-labs/ade-react` (the
workspace's components) and `github.com/hollis-labs/libs/agent/host` (the server's side, in the libs repository).

## What is whose

| Path | Whose | Sync policy |
|---|---|---|
| `frontend/src/domain/**` | the person's: the surface (a starter notes board) | `skip` |
| `cmd/<repo>/prompt.go` | the person's: the agent's prompt | `skip` |
| `README.md` | the person's | `skip` |
| `cmd/<repo>/agent.go` | the preset's: mounts the agent host at `/api/agent` | `prompt` |
| `frontend/src/pages/workspace.tsx` | the preset's: the workspace page | `prompt` |
| `frontend/src/ade/**` | the preset's: hub, session, guide persistence, guide events | `prompt` |

`skip`: `folio sync` never writes the file, whatever has happened to it or to the preset.
`prompt`: sync writes the preset's new version when the file is as generated, and reports a
conflict (never overwrites) when it was edited. Everything else follows the Substrate layers' rules.

The workspace page reaches the domain only through `frontend/src/domain/index.ts`, which exports
`SURFACE`, `labels`, `Surface` and `context`.

## Generate

All versions are inputs with no default. The kit versions below are the `kits:` block of the
Substrate `bom/bom.yaml` at the version folio's CI builds against
(`FOLIO_SUBSTRATE_VERSION` in `.github/workflows/folio-checks.yml`); take the ADE versions from
the ADE release you build on.

```sh
folio new app-ade notes-app --non-interactive \
  --input repo_name=notes-app \
  --input substrate_version=v0.1.1-0.20261119145946-6a4bb696c942 \
  --input ade_agenthost=v0.1.0 \
  --input ade_core=0.1.0 --input ade_agent=0.1.0 --input ade_react=0.1.0 \
  --input kit_app_shell=0.11.0 \
  --input kit_design_app_runtime=0.6.0 \
  --input kit_design_bindings=0.3.0 \
  --input kit_design_components=0.5.1 \
  --input kit_design_tokens=0.5.0 \
  --input kit_dashboard=0.6.2 \
  --input kit_plugin_host_ui=0.3.0
```

## Build and serve

```sh
cd notes-app
go mod tidy && go run github.com/hollis-labs/substrate/cmd/substrate pins sync --app . && go mod tidy
(cd frontend && npm install && npm run build) && go build -o notes-app ./cmd/notes-app
```

```sh
./notes-app identity setup --username owner   # once; reads the password from the terminal
./notes-app serve --managed
```

With no model configured the app runs and the agent panel says so. To connect one, add to the
operator configuration (`SUBSTRATE_CONFIG`):

```yaml
notes-app:
  agent: { model: <model or alias>, base_url: <Anthropic-compatible base URL>, key_file: <path> }
```

## Testing the generated app

`TestAppADE_BuildsAndRuns` generates an app, builds it against the released packages and serves
it. It runs when `FOLIO_SUBSTRATE_VERSION`, `FOLIO_ADE_AGENTHOST` (the `agent/host` version) and
`FOLIO_ADE_PACKAGES` (the `@hollis-labs/ade-*` version) are set and resolve.

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
