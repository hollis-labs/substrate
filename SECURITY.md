# Security policy

## Supported versions

Substrate is pre-1.0. Security fixes are made on `main` and in the newest tagged release. Older
releases may not receive backports. `CHANGELOG.md` lists what changed.

## Report a vulnerability

Do not put an exploit, token, credential, private key or other sensitive material in a public issue.
Use GitHub's private vulnerability reporting (the repository's Security tab). If it is unavailable,
contact a maintainer privately through the Hollis Labs organization or maintainer profile. Include the
affected version or commit, Go version and OS, how Substrate was used (which modules, whether untrusted
input reached it), reproduction steps and impact, and a safe way to reach you.

Maintainers acknowledge a private report, investigate, and coordinate disclosure and a fix release with
you. Response times are best effort while the project is pre-1.0.

## Scope

Substrate is a framework and builder. It never owns identity, policy, data or listeners: the app that
builds on it owns those. A vulnerability is in scope when a module or the builder behaves unsafely on
input an app could reasonably pass it (for example a crafted manifest causing the builder to emit or
run unintended code).

## Current limitations

- Pre-1.0: APIs, the manifest schema and module interfaces may change in any minor release.
- Only the config, logging, health, httphost and capabilities modules exist; auth, secrets and policy
  modules are planned and not yet provided.
