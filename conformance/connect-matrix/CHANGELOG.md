# Changelog

connect-matrix is new software. There are no tagged releases or compatibility guarantees yet.

## Unreleased

### Added

- `connect-matrix run`: per-pair proof of mint, accepted, wrong-audience refusal, revoke and rejection within the check wait (default 45s), as a table and optional JSON report that never contains a token.
- `connect-matrix mock-issuer`: JWKS, deny-list, mint and revoke endpoints for proving targets with no real issuer.
- An `http` issuer adapter, a placeholder shape until Tether's service-token command exists.
- Review hardening: the revoked check passes only if the refusal arrives within the wait (probes are capped by the time left, the wait between probes honors cancellation); reports show scheme and host only and configuration rejects URLs with credentials, a query or a fragment; every minted token is revoked on every exit and a failed revoke fails the pair; `ok_status` must be 2xx; the issuer client refuses cross-origin redirects.
