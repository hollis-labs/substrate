# Two-stage boot: nothing is created until a command asks for it

Status: design, not built. Incident: an agent ran `go run ./cmd/mentat identity --help` with its real environment.
`boot.StartWith` ran before cobra parsed anything, so `--help` started `database` and `identity`, which created a
signing key and an empty database in Mentat's live data directory. Sub-loom reproduced it for `loom --help`, `loom status`
and `loom path`. Station, Parallax and Tangent each hand-rolled a `plan()` that reads `os.Args` before boot.

## Is this the obvious shape?

Mostly. Two things are not obvious.

1. **The `identity` commands are registered from `identity.Start`** (`cli.AddCommand(app, svc.cliCommand())`). If
   `identity` starts lazily, the command tree has no `identity` command to dispatch to. Commands must be contributed
   without starting the module. This is the one structural change; everything else is wiring.
2. **Changing what `StartWith` does is a break.** Apps call `database.From(app)` between boot and `Execute` (adding
   commands, registering health checks). So staged boot is opt-in first (`Options.Staged`), then made the default in
   the next minor, which the compat policy allows before 1.0.

## Design

**Stage 1 (eager) is exactly the modules that create nothing: `config`, `logging`, `health`, `cli`.** Verified by
reading: `config` only reads the `SUBSTRATE_CONFIG` file, `logging` builds a handler on stderr, `health` allocates an
empty registry, `cli` builds a cobra root. They are a fixed set in `boot` (`eagerModules`), not per-app. `secrets`
is lazy: I did not verify that `libsecrets.New` is creation-free, so the safe default is lazy (open question 3).

**Stage 2 (lazy)** is every other selected module, started by `Running.Ensure(ctx, names...)`.

```go
// boot
type Options struct {
    AfterConfig Hook
    Before      map[string]Hook
    Staged      bool            // new: stage 1 only; the rest start through Ensure
}
func (r *Running) Ensure(ctx context.Context, names ...string) error // idempotent, mutex-guarded
func (r *Running) Execute(ctx context.Context, root *cobra.Command) error // see below

// module
type App struct { ...; Selected []string } // the manifest's full start list, set by boot (new)
type Runtime struct { ... }               // gains startLazy(names) and a started-set, below

// cli
func Needs(c *cobra.Command, modules ...string) *cobra.Command // annotation "substrate.needs"
type Contributor interface { Commands(app *module.App) []*cobra.Command } // implemented by a module
```

**A command declares what it needs.** `cli.Needs(cmd, "database", "identity")` writes `Annotations["substrate.needs"]`.
`cli.Needs(cmd)` with no names means nothing (`loom path`, `loom status` if it only reads config). An unannotated
command means every selected module, which is today's behaviour, so an app that adopts `Staged` without touching its
own commands is no worse off and its `--help` is already clean. Built-ins: `version`, `config` and `help`/`completion`
need nothing (`config` redacts with `secrets.redactor` only when secrets is already started); `health` needs all.

**Where the start happens.** `Running.Execute(ctx, root)` walks the finished tree once, validates every annotation
(unknown or unselected module name is an error before anything starts, the same rule as a `Before` typo), and wraps each
runnable command's `PersistentPreRunE` so it first calls `Ensure(closure(needs))`, then calls the pre-run hook cobra would
have run (the nearest ancestor's). Cobra handles `--help`, `help`, `completion` and bad flags before any pre-run hook, so
no argument parsing of our own is needed and the four `plan()` functions go away. Wrapping `RunE` instead was rejected:
an app's own `PersistentPreRunE` would run before modules exist.

**Order and dependencies.** `closure(needs)` adds `Requires` transitively from `catalog.Modules`, minus what already
started, and starts them in the manifest's `substrateModules` order, which `manifest` already makes dependency-safe
(Requires first). Starting a suffix later is safe because stage 1 never depends on stage 2. `identity` declares
`auth, database, identity` plus `secrets` when it is in `app.Selected` (sub-station found `identity setup` and
`identity key create` need `auth` too, via `auth.AddVerifier`). `Requires` is hard only; an optional wrap such as
`secrets` over `logging` is the command's to list.

**Stop.** `Runtime.started` is appended in real start order, so the existing reverse stop is correct for any subset.
`Ensure` failure stops everything started so far, as a module error does today. `Stop` after a run that never called
`Ensure` stops four no-op modules.

**Hooks.** `AfterConfig` runs in stage 1, right after `config`, unchanged. It must therefore create nothing; that is
now a rule for apps (below) and the conformance test catches a breach. `Before[m]` runs immediately before `m.Start`
whenever that happens, so a guard on `database` runs before the migration it guards and not at all for `--help`. A hook
for a module that is selected but never started in this run simply does not run. The typo check stays: the name must be in
the start list.

**`serve`.** `cli.Needs(serveCmd, "*")` (or leave it unannotated). It gets every module before `RunE`, then builds
`httphost.Handler` and calls `httphost.Serve` exactly as today. Modules that read each other at handler-build time
(`mcp.mount` reading `auth.guard`) are unaffected because all have started before `RunE`.

**Contributed commands.** `identity` implements `cli.Contributor`; `cli.Start` (stage 1) asks every *selected,
linked* module that implements it for commands and adds them. `identity.Commands` returns the tree whose `RunE`s
resolve the service at call time (`app.Value(identity.Key)`), valid because `Needs` ran first. `identity.Start` stops
calling `cli.AddCommand`. `cli.AddCommand` stays for eager boots and is documented as not valid under `Staged`.

## Compat

| Case | Result |
|---|---|
| App unchanged, `Staged` false | Identical to today. |
| `Staged: true`, commands unannotated | Same behaviour for real commands; `--help`, completion, `version`, `config` start nothing. |
| App's code between boot and `Execute` uses `database.From(app)` | Breaks under `Staged` (nil store); move it into `RunE` or a `Before` hook. Called out in the migration note. |
| App with `plan()` | Delete it; annotate its read-only commands with `cli.Needs(cmd)`. |
| New apps | `substrate generate` emits `Staged: true` in the example mains and the adopting guide. |

## Tests I would write

- `e2e`: build a real binary (cli, database, identity, secrets) and run it with `HOME`, `XDG_*`, cwd and the data path
  in a fresh temp dir, `SUBSTRATE_CONFIG` unset. Assert the dir is empty after `--help`, `identity --help`,
  `identity setup --help`, `identity key create --help`, `completion bash`, `version`, `config`. Positive control:
  `identity key create` there does create the files.
- `boot`: a fake module that records `Start`; `Ensure` starts the Requires closure in manifest order, is idempotent,
  stops in reverse, and a failing module stops the earlier ones; `Before` hook runs once, before its module, and not
  for `--help`; unknown `Needs` name fails before any start; an app `PersistentPreRunE` runs after `Ensure`.
- `cli`: `Contributor` commands exist before any lazy module starts; the identity commands resolve the service at `RunE`.
- A reusable helper `bootcheck.NoWritesBeforeCommand(t, newRoot)` that every app imports: it walks the tree of
  `newRoot`, runs `<path> --help` for every command in an empty `HOME`, and fails if any file appears. This is
  requirement 1 as the conformance test each app runs.

## What apps change

Replace `boot.StartWith(...)`+`root.Execute` with `boot.StartWith(..., Staged: true)` and `run.Execute(ctx, root)`;
delete `plan()` and its flag parsing; annotate read-only commands; move any pre-`Execute` use of a lazy module into a
command. Add the helper test. `docs/adopting-substrate.md` section 3 gains the rule: **before the command is known, an
app must not call anything that opens a file, a socket or a database, or creates a directory: not in `main`, not in
`AfterConfig`, not while building commands.** Section 3's snippet and `docs/identity.md` ("same boot as the server")
change with it.

## Not verified

I did not read Station's, Parallax's or Tangent's `plan()`; whether each can be replaced by annotations alone depends on
commands with mixed needs by flag (for example `status --live`). Annotations are per command, so a flag-dependent need
must call `run.Ensure(ctx, ...)` inside `RunE`, which the API allows.

## Decisions for the driver

1. Opt-in `Staged` now and default later, or flip the default in one minor with the migration note?
2. Unannotated command means all modules (recommended, compatible) or nothing (safer, breaks apps)?
3. Is `secrets` creation-free enough to be stage 1? Needs a read of `libs/security/secrets` (state_dir handling).
4. Ship `bootcheck` as a public package of Substrate (recommended) or a documented snippet?
