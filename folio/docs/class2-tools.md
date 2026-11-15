# Class 2 tool presets

`substrate-cli` and `substrate-service` render a class 2 tool (spec: project/1 class bar, RC1). `substrate-app` and
`app-dashboard` and `app-chat` are the class 1 presets. The two class 2 presets compose `substrate-tool-baseline`, which composes `go-baseline`.

| | `substrate-cli` | `substrate-service` |
|---|---|---|
| Substrate variant, profile | `cli`, minimal | `microservice`, minimal (+ `httphost`, `cli`) |
| `project.yaml` interface | `cli`, auth `none` | `daemon`, auth `enforce` or `loopback_open` |
| `deploy.yaml` | install only | install, `restart: {via: cerberus, resource: <name>}`, health URL |
| `<name>.cerberus.yaml` | no | yes (Option B) |
| auth guard | n/a | `internal/authmode` |

Inputs: `repo_name`, `substrate_version` (no default; from the Substrate BOM), `description`; service also `http_port`,
`auth`; deploy target inputs `target_os` (linux/darwin) and `target_arch` (amd64/arm64); both `owner` (default the placeholder `user:owner`; set your own), `deploy_host`, `build_workflow_ref`. `go_version` comes from
`platform-pins.yaml` through go-baseline.

## Single source

`project.yaml` is authored once. Its Folio-owned `settings.runtime` projection generates `deploy.yaml`
and the daemon's Cerberus manifest, both initially and through `folio deploy render <dir> --plan`.
Use `--write` after review. Authored output conflicts are refused unless explicitly accepted with
`--overwrite`; the descriptor, comments and unrelated settings are never rewritten. This operation
only writes local files and never calls fleet-install or Cerberus.

Runtime keys: `target_os` (linux/darwin), `target_arch` (amd64/arm64), `host` (fleet inventory alias),
`binary_link`, `install_root`, and daemon `http_port`; optional `env_file` is an operator-owned reference,
never a credential value. Paths must be absolute or home-relative. These keys are a temporary Folio
projection inside project/1's extensible settings, separate from FC-8's shared schema ownership.
The generated `serve --managed --addr` command applies the selected runtime port and keeps auth enforced.
Regenerating deployment data does not change application auth, code, or compiled development defaults.
A target OS/architecture change also requires updating the CI build target before producing artifacts.
Existing descriptor checks still detect identity/link/health/auth disagreement; runtime ports no longer
need to match the compiled development default because the supervisor passes the explicit address.

## Auth and host behavior

Without MCP, class 2 services use the small `internal/authmode` guard. With MCP, the
full-host Substrate auth middleware validates configured credentials and per-route/tool scopes;
it does not stack a second static-token gate over the framework verifier. Only health is public.
The development exception is explicit and unavailable to MCP-enabled HTTP services.

The Cerberus manifest carries references, never token values. `health.version_json` is omitted
until the service's health endpoint reports a version. Actions use canonical BOM pins; changing
them to SHA pins is a coordinated BOM change, not an isolated preset override.

## Shared structural contract

`internal/descriptor` delegates to `github.com/hollis-labs/substrate/kinds` for typed project/1
parsing. New class 2 output requires a Substrate revision containing that package; the first
known-compatible revision is `45b018004dd2`
(`v0.1.1-0.20261115173848-45b018004dd2`). `substrate_version` still has no default; choose
this revision or a compatible successor explicitly. Render-only legacy fixtures do not establish
build compatibility with older framework versions.

The shared parser validates structure, including unknown fields and field types. It does not certify
class-bar compliance: generated class identity, deployment drift, declared-auth checks and runtime
managed/development guards remain in place. Folio's deployment renderer uses the same parser, then
checks its own runtime projection rules. Authored descriptor bytes are never reserialized.

## Not yet

The registry registration hook (awaiting the catalog interface) and `folio sync`/`inspect` in CI.
Folio’s own authored descriptor is [project.yaml](../project.yaml); its local CLI does not claim a Substrate runtime.

## Observed deployment limits

- `fleet-install` needs `--target` when the host alias is a placeholder; dry-run with a tarball works offline.
- `restart: {via: cerberus, resource: ...}` is accepted by fleet-install. It performs the restart only
  when `run: true` permits execution as the local user; otherwise it prints the requested Cerberus
  action and deployment stays pending until `--restart-done`. Verify the running executable after
  deployment: a successful health check alone does not prove the intended binary was restarted.
- The generated build target is explicit. Changing runtime target OS/architecture requires updating
  the CI target before creating a matching artifact; deployment rendering warns instead of editing CI.
- Class 2 CI does not include its own lint/govulncheck gate; the reusable build's govulncheck is advisory.
- Cerberus `build_strategy` and `artifact_path` are omitted by design: fleet-install owns binary
  placement, and Cerberus supervises the installed link. This is the selected Option B contract.

## Managed and development runs

`serve --managed` is the generated Cerberus invocation. It defaults to enforced auth regardless of the
compiled development default, requires a configured token, and refuses an explicit `loopback_open`
override. `loopback_open` requires `serve --dev`; combining `--dev` and `--managed` is refused.
Cerberus uses `supervisor: auto` for Linux and macOS. Set `target_os` and `target_arch` when rendering
the fleet target (defaults linux/amd64). Fleet-install owns the PATH binary; neither supervision nor
runtime operation needs the checkout. These are startup rules, not a way to detect arbitrary supervisors.

Installed tools discover optional `$XDG_CONFIG_HOME/<app>/config.yaml` (fallback
`~/.config/<app>/config.yaml`); `SUBSTRATE_CONFIG` takes precedence. Discovery is read-only.

## Optional MCP

`with_mcp=true` selects the current Substrate agentic profile for either class-2 preset, adds a scoped
`ping` example and the stdio `mcp` command. A service also exposes authenticated `/mcp`. Auth stays
enforced; the simple `<APP>_TOKEN` environment credential becomes a hashed Substrate API-key verifier
with endpoint/read scopes. CLI deployments can instead configure a verifier in operator config.
`mcp --local-trust` is explicit, stdio-only and narrowed to endpoint/read scopes; a supplied bad token
still fails. A server verifier must be configured even for trusted local stdio. MCP-enabled service
HTTP refuses loopback_open, including with --dev. Help/print commands start only read-only modules.
Secret backend relative state resolves under `$XDG_STATE_HOME/<app>/secrets` (fallback
`~/.local/state/<app>/secrets`) rather than the checkout; no directory is created by path discovery.
