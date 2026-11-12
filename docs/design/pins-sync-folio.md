# `pins sync --folio`: framework, libs, kits and the npm toolchain

Status: design, not built. Two asks from the Folio port (agent sub-folio). Grounded in Folio's
`platform-pins.yaml`, `scripts/check-platform-pins-drift.sh` and the `app-dashboard` preset (hollis-labs/tools, read-only
clone), Loom's `frontend/package.json` on main, and `bom/bom.yaml` and `bom/pins.go` here.

## What exists

- `pins sync --folio` writes only `go.version` (from `toolchain.go_floor`) and the `ci` block. `renderFolio` produces the
  whole file, so `pins check --folio` is a string comparison of the render: any key we add is covered by `check` for free.
  Folio's own tail (`chimera:`) is carried over.
- Folio's file is flat, two levels, `key: "value"`, read with awk (it says so). The keys below stay within that.
- Folio's checkout still says `go.version: "1.26.6"`; the BOM is at 1.26.11. It has not been synced since the floor moved,
  which is the drift the check exists to catch.
- Folio's `app-dashboard` preset renders ranges (`^1.3.0`, `^19.0.0`, `vite ^5.4.0`) for go-webui and literals for the
  chimera branch (`react 19.3.0`, `vite 7.3.6`, ...), copied by hand from Loom. Nothing compares them to a BOM.
- Apps pin Substrate as a pseudo-version of main (Loom and Tangent: `v0.1.1-0.20261112164520-da9f59bb59b7`). The only tag,
  `v0.1.0`, is 53 commits behind main.

## Ask 1: framework, libs and kit pins in the mirror

### The framework version

A BOM cannot carry the pseudo-version of its own commit (the hash depends on the file's content). Options:

| | Source of truth | Verdict |
|---|---|---|
| A. BOM carries a released tag, `framework.version: v0.2.0` | The maintainer cuts tags | Recommended. Tags are the maintainer's decision, and 53 untagged commits with every app on a pseudo-version is the case for cutting one. Release CI checks the field equals the tag being cut. |
| B. `sync --folio` computes the pseudo-version of the Substrate checkout it runs in | A git commit | Works without a tag and is not circular (Folio is another repo), but `check --folio` then fails after every Substrate commit, docs included, so Folio's pin would move constantly. |
| C. A hand-maintained pseudo-version in the BOM, bumped in a follow-up commit | Humans | Always one commit behind and valid. Fallback if there is no tag. |

Until a tag exists the field may hold a pseudo-version (C), and `pins check --online` can resolve it with `go list -m`.
The mirror carries whatever the BOM says; Folio never derives it.

### Keys

The BOM gains a block, hand-edited like the rest:

```yaml
framework:
  module: github.com/hollis-labs/substrate
  version: v0.2.0
```

The mirror gains sections; existing ones are unchanged:

```yaml
substrate:
  module: "github.com/hollis-labs/substrate"
  version: "v0.2.0"
libs:                 # BOM libs + app_libs. key = module path after github.com/hollis-labs/libs/, "/" -> "_"
  app_app-host: "v0.1.0"
  core_store: "v0.1.0"
  mcp_plugin-mcp: "v0.6.0"
  security_auth: "v0.3.1"
kits:                 # BOM kits. key = package name without the "@hollis-labs/" scope
  design-tokens: "0.5.0"
  app-shell: "0.1.0"
npm:                  # Ask 2. key = package name, leading "@" dropped, "/" -> "__"
  react: "19.3.0"
  base-ui__react: "1.8.0"
```

One mechanical rule per section, so a template computes the key from the name it already has and the drift check does the
same with awk. A preset that needs a key the file lacks must fail at render naming the key (Folio's side). `third_party`
(cobra, yaml, x/crypto, go-sdk) is not mirrored: those arrive through the framework's own requires. Add a section if a
template needs them.

### kit-chat

It belongs in the BOM, because Folio's `chat-app` preset renders it, but it cannot be pinned today: the comment in
`bom.yaml` records that 0.6.0 needs design-bindings and kit-cards `^0.1` while the BOM is on `^0.2`. Pin the first kit-chat
release that follows. Until then the key is absent from `kits:` and a chat-app preset rendering a Substrate app fails
loudly instead of guessing. No placeholder entry: `CheckNPM` fetches every pinned version.

### app-shell 0.11.0 with design-app-runtime 0.6.0

The BOM moves both in one change. Enforcement exists already: `CheckNPM` fails a pinned kit whose dependency range on
another `@hollis-labs` package the BOM's pin does not satisfy, so app-shell 0.11.0 with design-app-runtime still at 0.5.0
fails `pins check --npm`. The PR says "both, together"; no new mechanism. (I read `CheckNPM`'s doc and its registry
handling, not every branch.)

## Ask 2: the toolchain behind a rendered app

Exact versions a rendered app needs, as Loom has them today: react 19.3.0, react-dom 19.3.0, vite 7.3.6, tailwindcss 4.3.3,
@tailwindcss/vite 4.3.3, @biomejs/biome 2.5.14, eslint 9.39.4, typescript 5.9.3, typescript-eslint 8.58.0,
@base-ui/react 1.8.0, lucide-react 1.52.0, @types/react 19.3.0, @types/react-dom 19.3.0, @types/node 26.6.4,
@hollis-labs/eslint-config-design 0.5.0 (and @playwright/test 1.63.0 for an app with e2e).

| | (a) `npm_toolchain` block in the BOM, mirrored | (b) a named app's `package.json` at a commit |
|---|---|---|
| Single hand-edited place | Yes, matching the 2026-11-02 ownership decision | No: a second source, owned by an app |
| Drift between apps | Detected: `pins check --app <dir> --frontend` compares an app's `package.json` with the BOM | Only against that one app |
| Source breaks for its own reasons | No | Yes: Loom's change moves the platform for everyone, and Loom is then checked against itself |
| Needs to read | Nothing | The app repo at a commit (pin it, fetch it) |
| Cost | One block, one renderer, one app check | A parser for a foreign file and a way to choose the commit |

**Recommendation: (a).** The BOM already owns kits, libs and CI pins; the toolchain is the same kind of fact, and (b)
puts the platform's source of truth inside one app. Seed the block from Loom's literals in the same PR.

```yaml
npm_toolchain:        # exact versions; mirrored to Folio as the "npm:" section
  react: "19.3.0"
  "@base-ui/react": "1.8.0"
  "@hollis-labs/eslint-config-design": "0.5.0"   # moves with the design kit
```

`@hollis-labs/eslint-config-design` goes here, not in `kits:`: `kits:` is the set the generated frontend manifest may name
and a lint config is not one. `validate` requires exact versions (no `^`, `~`, `latest`). `CheckNPM` also verifies the
toolchain versions are published.

### Code touched

`bom/pins.go`: `BOM.Framework`, `BOM.NPMToolchain`, `validate`, `renderFolio` (four new sections; the key rules in one
small function), and a new `CheckFrontend(b, packageJSON)`. `cmd/substrate/pins.go`: `--frontend` flag for
`pins check|sync --app`. `bom/bom.yaml` header. `docs/adopting-substrate.md` section 9 (pins). Folio cannot import the key
rule, so it is documented in the BOM header and tested here with a table.

### Tests I would write

Render golden for the new sections from a fixture BOM. `check --folio` fails when one new key is edited and when one is
missing. Sync is idempotent and keeps the `chimera:` tail. Key-rule table (`@base-ui/react` to `base-ui__react`,
`app/app-host` to `app_app-host`) with a no-collision assertion over the real BOM. `CheckFrontend` flags a range, a wrong
version and a missing required package, and passes Loom's file at the seeded versions. `validate` rejects a caret.

## Not verified

I did not run `pins sync --folio` against Folio's checkout, did not read Folio's preset engine (its mirror says it does not
read the file yet), and took the versions from Loom's `package.json` on main today. Tangent's file did not decode in my
read, so "Loom and Tangent agree" is untested. That awk tolerates `__` and `-` in keys is likely, not run.

## Decisions for the driver

1. Cut a Substrate tag now and let the BOM carry it (A), or a hand-bumped pseudo-version meanwhile (C)?
2. Key rules as above (`substrate.version`, `libs.<path with _>`, `kits.<name>`, `npm.<name with __>`)? Folio's templates and
   drift check can be written to them now.
3. Toolchain: (a), recommended, or (b)?
4. Mirror `app_libs` inside `libs:` (assumed yes) and `third_party` (assumed no)?
5. Run `pins sync --folio` for the stale `go.version: "1.26.6"` mirror as part of this, since the floor moved today.
