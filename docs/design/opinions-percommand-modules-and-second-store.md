# Opinions: per-command module sets, and a second named store (brief item 9)

Status: opinion only, nothing built. I did not read Station's gateway or any app's workaround; this rests on the framework code and the brief.

## 1. A per-command module set for CLI-variant apps

Today `boot` starts every module in the manifest for every command. Consequences seen in this work: `<app> version` with identity selected creates a signing key and migrates tables; auth in off mode warns on every command (change 10.4); a short stdio or CLI run opens listeners-adjacent state it never uses.

Does it deserve a design? **Not as a general feature yet.** The two concrete harms are fixable narrowly: lazy signing-key creation (see the stdio design) removes the file-creation surprise, and moving the auth warning to `httphost.Serve` removes the noise. A per-command module set is a large contract change: the manifest would have to say which commands need which modules, the builder would have to validate dependencies per command, modules' `Start`/`Stop` ordering and `app.Value` lookups (`database.From`, `identity.From`) would return nil for some commands, and every module and app would need to tolerate absence. That is the kind of change that is cheap to get wrong and hard to remove.

Smallest viable shape if the narrow fixes prove insufficient: an option on boot, not on the manifest, chosen by the app's code per command:

```go
boot.Options{ Only: []string{"config", "logging", "database"} } // or Skip: []string{"identity", "httphost"}
```

`Skip` is validated against the catalogue's dependency graph (skipping a module another started module requires is a start error), skipped modules are not started or stopped, and `From(app)` already returns nil for a missing module, which is the existing contract. The app's main picks the option from the cobra command before boot. No manifest change, no builder change. Revisit when a second app, not just the one that hit the key-file problem, has a concrete cost that lazy start does not fix.

Recommendation: **no design now; do the two narrow fixes; keep `boot.Options.Skip` as the named fallback.**

## 2. A second named store in the database module

Station's gateway keeps a Postgres ledger outside the module. The database module opens one store from `database:` config and publishes it at `database.store`; `database.From(app)` returns it. Code, health check, schema modes and secret-from-file DSN handling all assume one.

Does it deserve a design? **Yes, a small one, but only after a second app asks.** A ledger in a different Postgres from the app's own database is a plausible repeating shape, and doing it outside the module means the DSN-from-file rule, the health check, the migrate-versus-verify split and shutdown are reimplemented per app, which is where mistakes in secret handling creep in. Unlike item 1, the change is additive: nothing changes for a single-store app.

Smallest viable shape:

```yaml
database:                 # unchanged: the default store
  sqlite: { path: app.db }
  stores:                 # optional, named
    ledger: { driver: postgres, postgres: { dsn_file: /run/secrets/ledger, max_conns: 4 }, schema_mode: verify }
```

```go
database.From(app)               // the default store, unchanged
database.Named(app, "ledger")    // *store.Store, nil if not configured
```

Each named store reuses the existing open, health-check (`database.<name>`) and close code, with its own schema mode; migrations for it are the app's (the module already leaves the schema to the app). No cross-store transactions, said plainly in the docs. Per the store contract, each SQLite store has its own single write connection.

Recommendation: **yes to a design when Station (or another app) is ready to move its ledger; no work before that.** I could not verify that Station's ledger needs nothing the above lacks (for example a different driver or pool tuning per store); ask Station's agent for its actual config before fixing the shape.

## Decisions asked of the driver

1. Agree to defer per-command modules in favour of lazy key plus the auth-warning move, keeping `boot.Options.Skip` as the fallback?
2. Is a second app waiting on a named store, or do we wait for Station to ask?
