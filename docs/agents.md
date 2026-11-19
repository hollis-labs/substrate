# Agent directories

Substrate defines the shared app config key `agents.dir`. It defaults to
`agents/` beside the app's config file. In `substrate.yaml`:

```yaml
config:
  agents:
    dir: agents/
```

`agents` is a shared config block, accepted regardless of the selected modules;
there is no agents module to add. In a `SUBSTRATE_CONFIG` override file the block
is at the top level, just like the other config blocks:

```yaml
agents:
  dir: custom-agents/
```

Use `github.com/hollis-labs/substrate/agents` after config overrides have been
applied (for example in `boot.Options.AfterConfig`):

```go
layout, err := agents.Resolve(app.Config, configFile, stateRoot)
if err != nil {
    return err
}
names, err := layout.Names()
if err != nil {
    return err
}
for _, name := range names {
    authored, err := layout.AgentDir(name)
    if err != nil {
        return err
    }
    data, err := layout.DataDir(name)
    if err != nil {
        return err
    }
    // Pass authored and data to the app's agent loader.
    _, _ = authored, data
}
```

The host supplies its actual config filename and state root. Use the
`SUBSTRATE_CONFIG` filename when that override is active; otherwise supply the
host's config filename (such as its deployed `substrate.yaml`). Generated wiring
embeds config values, not the build machine's config path: the host must supply
the runtime location even if no config file exists there. Substrate does not
choose or create the host's state root.

`Resolve` returns an absolute authored root. Relative `agents.dir` values resolve
beside `configFile`, including `../` paths; absolute values stay absolute. Relative
config filenames and state roots resolve from the working directory at the time
of the call. Missing or null `agents.dir` uses the default; empty strings and
non-string values are errors.

`Dir` returns that authored root. `Names` returns immediate subdirectory names in
lexical order, ignoring plain files and symlinks. It returns filesystem errors,
including a missing root; create an empty root when an app has no agents.
`AgentDir(name)` returns the authored path and `DataDir(name)` returns
`<stateRoot>/agents/<name>`, without checking existence or creating either path.
Both reject empty names, dot segments and path separators. These are lexical
paths, not a filesystem sandbox: the host owns directory permissions and symlinks
under its state root. The host also owns URL encoding of names used in routes.

The helper imports no Nanite code and does not load or validate authored agent
files. See Nanite's [embedding contract](https://github.com/hollis-labs/nanite/blob/rebuild/substrate/docs/embedding.md)
for authored content and the embedding API; pass the returned data path as its
per-agent `agent.Options.DataDir`.
