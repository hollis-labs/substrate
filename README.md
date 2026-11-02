# Substrate

Opinionated, versioned framework for [Hollis Labs](https://github.com/hollis-labs) Go apps. An app is a
manifest (`substrate.yaml`) plus its own engine; updating the framework is one version bump.

"Batteries included" means capability and plumbing only. Substrate never owns identity, policy, data or
listeners, and every module is optional: the manifest picks a variant and profiles, the builder generates
a wiring file that imports only what was selected, and `go build` does the rest.

> **Status: pre-release.** Config, logging, health, HTTP host and a capability report exist. Auth,
> secrets, SQLite/migrations, MCP and the plugin host are planned. See [`docs/status.md`](docs/status.md).
> Not yet tagged; APIs and the manifest schema will change.

## Example

```yaml
# substrate.yaml
app: hello-app-host
variant: app-host          # what the binary is
profiles: [minimal]        # module bundles; agentic adds the capability report
config:
  logging: { level: info, format: text }
```

```sh
go run ./cmd/substrate validate -f examples/hello-app-host/substrate.yaml
go run ./cmd/substrate build    -f examples/hello-app-host/substrate.yaml
./examples/hello-app-host/hello-app-host   # serves /healthz
```

## Concepts

| Term | Meaning |
|---|---|
| Module | One optional capability: `modules/<name>`, registered in `catalog/catalog.go`. |
| Profile | A composable module bundle (`minimal`, `agentic`; more planned). |
| Variant | What the binary is: `service`, `app-host`, `headless`, `cli`, `microservice`. |
| BOM | `bom/bom.yaml`, the one place version pins live. |

Modules wrap and pin [`libs`](https://github.com/hollis-labs/libs); they do not copy it.

## Development

```sh
go vet ./... && go test ./...
python3 scripts/scan-public --ref HEAD
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md), [`AGENTS.md`](AGENTS.md), [`SECURITY.md`](SECURITY.md) and
[`CHANGELOG.md`](CHANGELOG.md). Licensed under the [MIT License](LICENSE).
