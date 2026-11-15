# Class 2 tool presets

`substrate-cli` and `substrate-service` render a class 2 tool (spec: project/1 class bar, RC1). `substrate-app` and
`app-dashboard` stay the class 1 presets. Both compose `substrate-tool-baseline`, which composes `go-baseline`.

| | `substrate-cli` | `substrate-service` |
|---|---|---|
| Substrate variant, profile | `cli`, minimal | `microservice`, minimal (+ `httphost`, `cli`) |
| `project.yaml` interface | `cli`, auth `none` | `daemon`, auth `enforce` or `loopback_open` |
| `deploy.yaml` | install only | install, `restart: {via: cerberus, resource: <name>}`, health URL |
| `<name>.cerberus.yaml` | no | yes (Option B) |
| auth guard | n/a | `internal/authmode` |

Inputs: `repo_name`, `substrate_version` (no default; from the Substrate BOM), `description`; service also `http_port`,
`auth`; both `owner` (default `user:chrispian`), `deploy_host`, `build_workflow_ref`. `go_version` comes from
`platform-pins.yaml` through go-baseline.

## Single source

`project.yaml`, `deploy.yaml` and the Cerberus manifest are rendered once, from the same inputs. Folio does not
re-render `project.yaml` (sync policy `skip`), so later edits can diverge; `internal/descriptor` tests, run by CI,
fail on divergence of: project id / Cerberus owner / project id; Cerberus `command[0]` / `deploy.yaml` symlink path;
Cerberus `port`, `url`, `health_check.url` / `deploy.yaml` `health.url` / `substrate.yaml` `httphost.addr`;
`restart.resource` / Cerberus resource id; declared auth / `authmode.Default`. It also forbids `build_strategy` and
`artifact_path` in the manifest (Option B: they would overwrite the fleet-install link).

## Embedded schema

`internal/descriptor/descriptor.go` is a copy of the project/1 RC1 rules, marked for replacement by the
Substrate-owned package (FC-8) when it lands.

## Not yet

The optional `mcp` subcommand, the registry registration hook, Folio's own `project.yaml` and `folio sync`/`inspect` in CI.

## Friction notes for the 1.0 cut

- `project.yaml` "rendered from" is one-shot: re-render after a descriptor edit is not possible, so a drift test replaces it.
- `fleet-install` needs `--target` when the host alias is a placeholder; dry-run with a tarball works offline.
- Restart cost (Substrate manager, fleet-install spec): `via: cerberus` needs `resource`; fleet-install runs it itself only with `run: true` as the local user, else it prints a human request and the deploy stays pending until `--restart-done`. A Cerberus unit without the fleet-install `command[0]` could restart an old copy with health still 200; verify the running executable after a deploy. Verb and resource id on live Cerberus still to be confirmed with the Mig Cerberus Lead.
- `deploy.yaml` `restart: {via: cerberus, resource: ...}` is accepted by the installed `fleet-install` (it prints a
  request for a human to run `cerberus resource reload <id> --ack`); the Substrate manager should confirm this is the intended shape.
- `substrate pins sync --app` is not in tag v0.1.0; the tests use a pseudo-version.
- Class 2 CI has no lint and no govulncheck of its own (the build workflow's govulncheck is advisory); decide whether the bar is too light.
- Option B for tools Cerberus should build is not covered: the manifest has no build_strategy.
