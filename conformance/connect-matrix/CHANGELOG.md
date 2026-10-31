# Changelog

connect-matrix is new software. Releases are tagged `0.x`; there are no compatibility guarantees before 1.0.

## Unreleased

### Added

- `cli` issuer (Tether `service-token issue`/`revoke`), matrix mode (`callers` x `targets`, `-matrix` grid, per-pair `status` of pass, fail, not-enforced or n-a, JSON `summary`), `posture: auto` detection, `probe.host`, optional `write_probe`, and `examples/agent-os.json`.
- The wrong-audience token now carries scopes of its own audience.

## v0.0.1 - 2026-10-25

First tagged release. Pre-1.0: interfaces can change between 0.x versions.

### Security

- Require Go 1.26.10 for GO-2026-6809 (crypto/x509); no behavior change.
### Added

- `connect-matrix run`: per-pair proof of mint, accepted, wrong-audience refusal, revoke and rejection within the check wait (default 45s), as a table and optional JSON report that never contains a token.
- `connect-matrix mock-issuer`: JWKS, deny-list, mint and revoke endpoints for proving targets with no real issuer.
- An `http` issuer adapter, a placeholder shape until Tether's service-token command exists.
- Review hardening: the revoked check passes only if the refusal arrives within the wait (probes are capped by the time left, the wait between probes honors cancellation); reports show scheme and host only and configuration rejects URLs with credentials, a query or a fragment; every minted token is revoked on every exit and a failed revoke fails the pair; `ok_status` must be 2xx; the issuer client refuses cross-origin redirects.
