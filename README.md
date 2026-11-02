# Substrate

Opinionated, versioned framework for Hollis Labs apps: an app is `substrate.yaml` + its own engine.
Capability modules are optional; the manifest picks a variant and profiles, the builder generates the
wiring file, `go build` does the rest. Modules never own identity, policy, data or listeners.

Status: build-order steps 1-2 (scaffold, manifest, validator, builder, config/logging/health/httphost,
hello apps, flag test). See `docs/status.md`.

    go run ./cmd/substrate validate -f examples/hello-app-host/substrate.yaml
    go run ./cmd/substrate build    -f examples/hello-app-host/substrate.yaml
    go test ./...
