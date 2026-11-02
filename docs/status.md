# Status

Done: manifest schema + validator, builder (validate/modules/generate/build), module registry,
core modules (config, logging, health, sqlite, auth, policy, httphost over libs app-host), capabilities module,
hello-service, hello-app-host, flag test (minimal vs agentic).

Placeholders: catalogue entries with status `planned` (mcp, secrets, pluginhost, cli) are
rejected by the validator. The `agentic` profile is only `minimal + capabilities` until step 3.

Awaiting decisions: standards table (CLI, router, migrations, health, config format, default auth/policy),
profile names, BOM placement vs Folio, gui-shell location, wiring-file-only (implemented) vs main package.
