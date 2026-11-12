# Design: one source for scan-public (brief item 8)

Status: proposal, nothing built.

## What exists

`scripts/scan-public` is a 501-line Python 3 script (rules for secrets, internal hosts, private IPs, private paths, filenames; tree and full history; optional gitleaks; allowlist files `scan-public.allow` and `scan-public.allow.d/*.allow`). Each app repo carries a copy and CI runs `python3 scripts/scan-public --ref HEAD` (Substrate's `ci.yml` line 40). I diffed Substrate's against `apps/basalisk/scripts/scan-public`: identical today. It will drift as rules are added: a new host pattern lands in one repo and the others stay blind. The script mentions `scripts/import-repo`, which is not in this repo, so some copies have extra surroundings.

Scope of what must stay per app: the allowlist (paths and reasons are the app's own).

## Options

**A. `substrate scan-public` subcommand (Go port).** Rules live in Go, pinned with the Substrate version the app already pins in `bom/bom.yaml`; CI calls `go run github.com/hollis-labs/substrate/cmd/substrate scan-public --ref HEAD`, as it already does for `pins check`. For: one pin mechanism, no Python in CI, the rules version is the framework version, unit-testable. Against: a rewrite of 501 lines with many regexes and the history walk (a day or two plus a parity test), Go regex differs from Python (no lookbehind), and the scan runs `go run`, which compiles the whole substrate CLI in CI.

**B. Embed the existing Python script in the substrate binary.** `substrate scan-public` writes the embedded script to a temp dir and runs `python3`. For: no rewrite, one source, same pin. Against: still needs Python; embedding a script and exec'ing it is awkward and hard to test; it keeps the language mismatch.

**C. Reusable GitHub workflow in the Substrate repo** (`workflow_call`), apps call `uses: hollis-labs/substrate/.github/workflows/scan-public.yml@<sha>`. For: no code in the app, CI-native, pinned by ref. Against: local runs (the hygiene gate before pushing, which agents rely on) have no equivalent unless the script is also installable; the repo is public so the workflow must live there, but the monorepo layout and org rules on cross-repo workflows are something I could not verify.

**D. Install from the tools monorepo** (`hollis-labs/tools`, built once in CI like other binaries). For: a standalone tool with its own release, usable outside Substrate apps (non-Go repos). Against: a new thing to version and install in every CI and dev machine, a second pin separate from the BOM, and it assumes a place in `tools` I did not look at.

## Recommendation

**A, staged through B.** Step 1 (small, immediate): add `substrate scan-public` that embeds the current script and execs `python3 -I` on it, passing flags through; add the allowlist convention to the docs. Apps change one CI line and delete their copy. The rules become one file in one repo, pinned by the BOM. Step 2, only if Python in CI becomes a nuisance: port to Go behind the same flags with a parity test that runs both on a corpus of known findings. Reason: the gate matters most for being identical everywhere, and step 1 achieves that at near-zero risk; the port is optional polish, not a prerequisite.

## What each app keeps

`scan-public.allow` and `scan-public.allow.d/*.allow` at its root (unchanged format, found by default as today). Nothing else. The command scans `--repo .` by default. Findings stay reported even when allowlisted, as today.

## How CI calls it

```yaml
- run: go run github.com/hollis-labs/substrate/cmd/substrate scan-public --ref HEAD
```

`pins sync` already rewrites the app's CI workflow from the BOM; it would also rewrite this step so the line is never hand-edited (I have not read `pins sync`'s workflow templating and would extend it rather than guess). gitleaks stays optional on PATH; the workflow keeps installing it as now. Exit codes unchanged (0 clean, 1 findings, 2 scanner could not run). Python 3 is already on GitHub runners; `substrate scan-public` exits 2 with a clear message if it is missing.

## Migration

1. Land the subcommand and an embedded copy with a test that the embedded bytes equal `scripts/scan-public` (or make the embed the only copy and delete the script here; Substrate itself then calls the subcommand).
2. Per app: switch the CI line, delete `scripts/scan-public`, keep the allow files, run once and compare findings with the old script on the same ref (must match).
3. `scripts/import-repo` (not in this repo) calls `scripts/scan-public --source`; it needs the same switch, owner to confirm.

## Tests

Embedded script runs and exits 0 on a clean fixture repo, 1 on a planted token, 2 with no python (PATH emptied); flags pass through (`--ref`, `--allowlist`, `--no-allowlist`); an allowlisted finding is reported in the allowlisted section and exit stays 0; the script never prints a secret value (redaction check on the planted token); `python3 -I` used (a hostile `json.py` in the cwd is not imported).

## Decisions asked of the driver

1. A then B-first as proposed, or go straight to the Go port?
2. Should `pins sync` own the CI line?
3. Who owns `scripts/import-repo`?
