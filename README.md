# Substrate

Go modules for the agentic side of Hollis Labs: the agent harness, the LLM core,
the agent mesh and the agent runtime core. Anything that is about agentic work
or the fabric agents run on lives here. General-purpose libraries live in
[`libs`](https://github.com/hollis-labs/libs).

## Status

`harness`, `llm-core` and `mesh` hold code that was imported, with its git
history, from earlier standalone repositories; `harness` and `mesh` also hold
code written in this repository since. `agent` is a skeleton: it builds and has
only a package doc, no API yet. Releases are per module and tagged
`<module>/vX.Y.Z`; the versions of a module are listed in its own
`CHANGELOG.md`.

## Modules

Each module has its own `go.mod`, its own version and its own tags.

| Module | Import path | Scope |
|---|---|---|
| `harness` | `github.com/hollis-labs/substrate/harness` | The agent harness (Cairn): launch, workspace and session plumbing for agent CLIs. |
| `llm-core` | `github.com/hollis-labs/substrate/llm-core` | The LLM core: shared model, provider and routing contracts and types. |
| `mesh` | `github.com/hollis-labs/substrate/mesh` | The agent mesh: messaging, federation, the tether client, human-in-the-loop, agent teams, the broker and agent definitions. |
| `agent` | `github.com/hollis-labs/substrate/agent` | The agent runtime core. |

Use a module the usual way, once it has a release:

```sh
go get github.com/hollis-labs/substrate/mesh@latest
```

Coming from a standalone module such as `go-sandbox` or `go-messaging`? See
[docs/migration.md](docs/migration.md) for the old-to-new table and adoption notes.

## Rules

- **One-way dependencies.** Substrate may depend on `libs`. `libs` never depends
  on substrate. The `libs` repository fails CI on any reference to this one.
- **Independent modules.** No `go.work`, no `replace` directive in a committed
  `go.mod`, no module nested inside another. `scripts/check-layout` enforces it.
- **Independent releases.** A module is released alone, with a tag that carries
  the module's directory as a prefix: `mesh/v0.1.0`, `harness/v0.3.2`. There are
  no repository-wide versions and no lockstep releases.
- **Public from the first commit.** No secrets, tokens, internal host names,
  internal addresses or personal paths in files, history, CI or commit messages.
  `scripts/scan-public` checks all of them.

## Developing across modules (there is no `go.work`)

A `go.work` makes code build on one machine and nowhere else, and it hides the
version a module really requires. This repository has none: it is git-ignored,
CI fails if one is committed, and every script and workflow runs with
`GOWORK=off`.

Work on one module at a time:

```sh
scripts/check mesh          # gofmt, go vet, go build, go test
scripts/check -race mesh    # the same, with the race detector
scripts/check               # every module
```

When a change spans two modules, land it in dependency order, one module at a
time. For example, when `mesh` needs something new in `harness`:

1. Change `harness`, merge it, and release it (`harness/vX.Y.Z`, see below).
2. In `mesh`, run `go get github.com/hollis-labs/substrate/harness@vX.Y.Z`, then
   make the change that uses it.

To try an unreleased change before releasing it, point the consumer at your
local copy with a `replace`, and drop it again before you commit:

```sh
cd mesh
go mod edit -replace github.com/hollis-labs/substrate/harness=../harness
# ... work, run scripts/check mesh ...
go mod edit -dropreplace github.com/hollis-labs/substrate/harness
```

A module from `libs` works the same way, pointing at your clone of that
repository: `-replace github.com/hollis-labs/libs/util=<path to libs>/util`.
`scripts/check-layout` fails on any committed `replace`, so a forgotten one is
caught before it reaches `main`.

## Releasing a module

```sh
scripts/release mesh v0.1.0           # dry run: validate, print the tag mesh/v0.1.0
scripts/release --apply mesh v0.1.0   # also create that annotated tag in your clone
```

The script checks that the version is valid semver, that it is newer than the
module's latest tag, that `mesh/CHANGELOG.md` has a section for it, that the
working tree is clean and that `main` is checked out. It never pushes. A
maintainer pushes the tag (`git push origin refs/tags/mesh/v0.1.0`), and a
pushed tag is permanent: the Go module proxy caches it.

## Tooling

| Script | Purpose |
|---|---|
| `scripts/check [-race] [module...]` | gofmt, vet, build and test, per module. |
| `scripts/check-layout` | No `go.work`, no `replace`, one `go.mod` per module at `<module>/go.mod`, module paths that match their directory. |
| `scripts/check-one-way` | No reference to a forbidden module prefix (see `scripts/repo.conf`). |
| `scripts/release` | Validate a release and print its module-prefixed tag. |
| `scripts/import-repo` | Import an existing repository into a module subdirectory with its history. Needs [`git-filter-repo`](https://github.com/newren/git-filter-repo). |
| `scripts/scan-public` | Scan the working tree and the full history for secrets, internal hosts and addresses, and private paths. Uses `gitleaks` too when installed. |

CI runs one workflow per module, filtered to that module's paths, and a
`guards` workflow for the layout, the one-way rule and the public-hygiene scan.

## License

MIT. See [LICENSE](LICENSE). To contribute, read [CONTRIBUTING.md](CONTRIBUTING.md);
to report a vulnerability, read [SECURITY.md](SECURITY.md).
