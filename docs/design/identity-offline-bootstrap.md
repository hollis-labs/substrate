# Design: offline identity bootstrap (Change 9)

Status: proposal, nothing built.

## Problem

Setup and key creation exist only as HTTP routes. A headless instance has a window between start and the first `POST /api/setup` in which anyone who reaches the port can become owner, and a stdio app needs a running server to get a key. An operator on the host needs the same two actions with no server.

## Shape: exported functions, plus subcommands the module contributes

Recommendation: **the identity module contributes the subcommands itself when the cli module is also selected, built on exported service methods the app can call directly.**

1. `identity` exports on `*Service`:
   ```go
   func (s *Service) SetupOwner(ctx context.Context, in OwnerInput) (PublicUser, error) // ErrSetupClosed if any user exists
   func (s *Service) CreateKey(ctx context.Context, in KeyInput) (key string, c PublicCredential, err error)
   type KeyInput struct { Name string; Scopes, Audiences []string; TTL time.Duration; CreatedBy string }
   ```
   The HTTP handlers become thin callers (`createFirstUser` and `createCredential` already hold the logic), so one rule set serves both paths.
2. At Start, if `cli.Root(app)` is non-nil, identity adds `identity setup` and `identity key create` to it. cli must start before identity: declare `cli` an optional predecessor in the catalog, or add the commands from a post-start hook (I did not check whether boot has one). identity importing cli is cycle-free (cli imports only health).

Why not functions only: every app would rewrite the flag parsing, 0600 handling and password reading, which is the part that must not vary. Why not commands only: an app with its own provisioning flow needs the functions. Putting the commands in cli instead would make cli import identity, which is worse. Cost: identity gains a soft dependency on cli.

## Commands

```
<app> identity setup --username NAME [--display-name NAME] (--password-file PATH | --password-stdin)
<app> identity key create --name NAME --scope S [--scope S ...] [--audience A ...] [--ttl 720h] [--out PATH]
```

`setup`: the password comes from a file or from stdin (one line, trailing newline trimmed); no flag carries the value and nothing reads an environment variable. Neither or both sources is a usage error. A file must be regular with no group or other bits (mode 0600 or tighter); a looser file is refused with the chmod hint, not silently fixed, since it is the operator's file. Runs `SetupOwner`: the same `newUser` validation (minimum length, bcrypt cost) and the same atomic `createFirstUser`, so an existing owner gives "setup is already done", exit 1, nothing changed. No session is created. Output: `owner <username> created`.

`key create`: needs an owner (`userCount > 0`, else "run setup first"). `--scope` is repeatable and required. It may grant any scope including `*`: the operator can already open the database, so a ceiling would be theater; the HTTP rule (nobody grants what they do not hold) stays for HTTP callers. `CreatedBy` is `local:operator`, the same id as the stdio principal, and shows in `GET /api/identity/keys`. Without `--out` the key (`key_...`) is the only thing on stdout, newline-terminated, so `$(...)` works; notes go to stderr and never include it. With `--out PATH` the file is created `O_CREATE|O_EXCL` at 0600 (an existing file is an error, never overwritten) and stdout carries only the credential id. The key is never an argument to any logger call.

Both commands run the normal boot path, so they open the database through the database module. Until the lazy-key change (see the stdio design) they also create the signing key file if absent; on an existing instance that is a no-op.

## Safe beside a running server

The store has one write connection per process; that rule is per process, so a second process does not contend for the first one's connection. Across processes SQLite serializes writers with the file lock, and the second writer waits (if a busy timeout is set) or fails with "database is locked". Setup race: `createFirstUser` counts and inserts in one `WriteTx`, so against the server's `POST /api/setup` exactly one wins and the other gets `setup_closed`. The server sees the new rows at once because `Verify` queries per call (I read it; there is no key cache).

Not verified: whether the store sets a busy timeout and WAL by default. I did not read the store DSN. If it does not, the command must either retry briefly or the module must set one; that is a finding the test below will surface.

The test that proves it: a real server started on a temp SQLite file, a second `*store.Store` on the same file standing in for the second process, and one case that execs the built binary. A goroutine keeps writing against the server (logins creating sessions). Meanwhile `key create` runs. Assert exit 0 within a timeout, the key verifies on the server (`GET /api/me`), and no server write failed. Second case: `setup` races `POST /api/setup`, and exactly one succeeds.

## Trust statement for the docs

Whoever can run the binary against the database and key file already controls the instance: they can read the database and the signing key and write rows or mint tokens directly. These commands add no authority; they make the same authority usable without an open setup window. They do not check who the operator is.

## Files touched

`modules/identity/service_admin.go` (new: `SetupOwner`, `CreateKey`, input types), `modules/identity/cli.go` (new: commands, password and out-file handling), `modules/identity/handlers.go` (call the methods), `identity.go` (register commands), catalog entry, `docs/identity.md`, `docs/adopting-substrate.md`, `CHANGELOG.md`.

## Tests

Fresh database: setup succeeds and the owner signs in over HTTP. Second setup refused (exit 1, no change); setup refused after a user was made over HTTP. The password never appears in stdout, stderr or captured logs, including error paths (use a distinctive password and grep every buffer). No flag takes the password (enumerate flags). Password file mode 0644 refused, 0600 accepted, missing file and missing source are errors. `key create` before setup refused. Scope `*` accepted. The key authenticates against a real server started afterwards, and its audiences and ttl hold; an expired key is refused. `--out` makes a 0600 file, refuses to overwrite, and stdout holds no key; without `--out` stdout is exactly key plus newline. Creator recorded as `local:operator`. The concurrency tests above. Mutations that must fail a test: drop the owner-exists check, loosen the mode check, log the key.

## Decisions asked of the driver

1. Module-contributed subcommands over exported methods, with the soft dependency on cli?
2. Any scope grantable offline, creator id `local:operator`?
3. Refuse rather than fix a loose password file?
4. File or stdin only, or also an interactive no-echo prompt?
