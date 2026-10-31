# connect-matrix

Proves that one app can call another app's API with a service token, and that revoking the token stops it. For each configured pair it runs:

| check | meaning |
| --- | --- |
| `mint` | the issuer produced a service token for the pair's audience and scopes |
| `accepted` | the target answered the probe request with the expected status |
| `wrong_audience` | a token meant for another audience is refused (401 or 403) |
| `revoke` | the issuer accepted the revocation |
| `revoked` | the target starts refusing the token, and how many seconds that took. The check waits up to `revoke_bound_seconds` (default 45: one 30s deny-list poll interval plus slack) for the effect; a target that has not refused by then fails. This is a test patience, not a guarantee about the target: revocation takes effect within one poll interval after a successful poll, and if the issuer or deny-list is unreachable the target keeps the last list (stale) |

`wrong_audience` and `revoked` only mean something when the target is enforcing. Set `"enforcing": false` for a target that admits anonymous callers and those two checks are skipped; claiming `enforcing` on a target that is not fails with a hint. Tokens and credentials are never printed or written to the JSON report. Every minted token is revoked before a pair finishes, on every exit path, and a failed revoke is a failing `cleanup` / `cleanup_other` check. The issuer client refuses redirects that leave its origin (scheme, host and port), so the issuer credential is never forwarded.

```sh
go install github.com/hollis-labs/tools/connect-matrix/cmd/connect-matrix@latest

# 1. a stand-in issuer: JWKS, deny-list, mint and revoke (loopback or tailnet only)
connect-matrix mock-issuer -listen 127.0.0.1:9100 -iss mock -pub-key-out /tmp/mock.pub.pem

# 2. start each target trusting it, for example Torque:
#      HOLLIS_AUTH_TORQUE=enforce TORQUE_AUTH_ISSUER=mock \
#      TORQUE_AUTH_JWKS_URL=http://127.0.0.1:9100/.well-known/jwks.json \
#      TORQUE_AUTH_DENYLIST_URL=http://127.0.0.1:9100/auth/denylist torque serve
#    (the mock does not need to be reachable by the target if you pass
#     TORQUE_AUTH_BOOTSTRAP_KEY_FILE=/tmp/mock.pub.pem, but revocation needs the deny-list URL)

# 3. prove the pairs
connect-matrix run -config examples/pairs.json -json results.json
```

Exit status is 0 when every pair passes, 1 when a pair fails, 2 for a usage or configuration error.

## Issuers

- `mock`: a `connect-matrix mock-issuer` instance. Its service tokens are Ed25519 JWTs with `kind=service` and an `exp` ten years out (the go-auth verifier requires `exp`, so "no expiry" is a distant one), revoked through its deny-list. State is in memory.
- `http`: a real issuer. `mint_url` receives `POST {"name","audience","scopes","kind":"service"}` and answers `{"token","id"}`; `revoke_url` receives `POST {"id"}`; both send `Authorization: Bearer <contents of credential_file>`. These shapes are a placeholder until Tether's service-token issuing command exists; only the issuer adapter changes then, not the pair definitions.

- `cli`: the Tether operator CLI on the same machine (`tether service-token issue --json` / `revoke JTI`). Set `issuer.command` and `issuer.global_args` (for example `["--catalog", "/var/lib/fabric/tether/catalog"]`). The token is read from stdout and never reported. Run the harness on the host that has the CLI (agent-os); the issuer principal is `msg://service/<caller>/dev`.

## Matrix mode

`callers` x `targets` in the config expand to one pair per (caller, target), named `caller->target`. `connect-matrix run -matrix` prints a caller by target grid with one status per cell:

| status | meaning |
| --- | --- |
| `pass` | token minted, accepted, wrong audience refused, revoke took effect |
| `fail` | any check failed (the table below the grid says which and why) |
| `not-enforced` | the target admitted the token but also admits a bogus bearer (`posture: auto`, the default in matrix mode) or is declared `permissive`; rejection checks are skipped, nothing is enforced |
| `n-a` | same app, or the target has `"na": "reason"` (no usable read endpoint) |

The JSON report carries `status` per pair and a `summary` count. A target may set `probe.host` (Host header), a harmless `write_probe` (checked as `write_accepted`), and `other_audience` (a real audience the issuer knows; its scopes are rewritten to that audience). `examples/agent-os.json` is the live agent-os matrix.

## Config

See `examples/pairs.json`. Unknown fields are rejected. Targets and issuer URLs must be absolute http(s) URLs with no userinfo, query or fragment (a secret cannot ride in a URL into a report or error; reports show scheme and host only). `probe.ok_status` must be a 2xx status: `accepted` has to show the target admitted the token, so a target that answers 401 to everything cannot pass. A pair needs `name`, `target`, `audience`, at least one scope and `probe.path`; `probe.method` defaults to GET and `probe.ok_status` to 200. `other_audience` (default `connect-matrix-other`) is the audience of the wrong-audience token.

## Development

`go vet ./... && go test -race ./...` in this directory. The tests run a go-auth guard as the target and the mock issuer as the issuer, so they exercise the real verifier and deny-list poller.
