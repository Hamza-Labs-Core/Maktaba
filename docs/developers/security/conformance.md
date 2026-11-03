# Security rules for plugins and contributors

Maktaba runs in people's homes, holds their libraries and accounts, and is reachable
from their phones and TVs. These rules keep it that way for code written by Maktaba
contributors and, once the plugin system ships, by plugin developers.

To report a vulnerability, email security@maktaba.dev (details at the end of this
page). Never use a public issue.

## Plugins

**Status: planned.** The plugin system is specified in
[Epic 54](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/README.md)
and is not built yet. Every rule in this section is a planned requirement from that
spec; details may change before it ships. Today the only way to extend Maktaba is a
first-party Python extension inside the pipeline, which is not open to third parties.

A plugin is one signed package (`.mkplugin`) holding one WebAssembly component, a
manifest, its strings in English and Arabic, an icon, and optional declarative UI. It
adds stages to the job queue and works through a narrow, versioned plugin API.

### No internet (planned)

- A plugin cannot open a socket, resolve a name or make an HTTP request. The host never
  links networking into the component, so there is nothing to call. There is no
  `network` capability, under any permission.
- Integrations that need the internet stay first-party code, not plugins.
- CI proves this on macOS, Linux and Windows with tests that try to reach the network
  and must fail.

### Sandbox (planned)

- The plugin runs in a WebAssembly sandbox with a minimal WASI subset. It cannot see
  the disk; it reaches media only through handles the API grants for the job it is
  running.
- CPU, memory, disk and time are capped per plugin. A plugin that crashes or exceeds a
  cap is stopped without taking down the pipeline; its health and logs are its own.
- Plugins never run arbitrary code in the apps. UI is declared as data (settings forms,
  panels) and drawn by Maktaba's own code.
- Plugins cannot change playback or streaming and cannot add API routes.

### Only granted API access (planned)

- A plugin declares the capabilities it needs in its manifest (for example
  `library.read`, `transcript.read`, `media.frames`, `tags.write`). The admin grants
  them at install, like phone app permissions.
- The API checks every call on the server. A call without the grant answers
  `403 plugin_capability`; a call outside the job's subject or the granted libraries
  answers `403 plugin_scope`. Both are logged.
- Plugins never get admin rights, user credentials, secrets or another plugin's data,
  unless that plugin exported it and access was granted.
- Plugins can write only allow-listed fields; every write is attributed to the plugin
  and version, so it can be listed and undone.
- An update that asks for new capabilities goes back to human review in the store.

### Signed packages (planned)

- Each package carries a digest of every file and two Ed25519 signatures: the
  developer's and Maktaba's store countersignature, added after automated checks and
  review.
- A home verifies signatures and checks a signed revocation list before installing or
  running a plugin, and can switch off a revoked plugin that is already installed.
- An unsigned or developer-only package installs only with the admin's explicit opt-in
  and is marked "Unreviewed" everywhere. The sandbox is the same either way.
- Developers keep their signing key private. A leaked key must be reported at once so
  its packages can be revoked.

## Contributors

These rules apply to every change in the repository today.

### No secrets or credentials

- Never commit secrets, passwords, API keys, tokens, private keys or signing material,
  in code, tests, fixtures, docs, examples or CI files.
- Read configuration from the environment or from files outside the repository. Example
  files (such as `.env.example`) carry names and safe local defaults only.
- Tests create throwaway keys and use obvious dummy values. Never paste a real token
  into a test, even briefly; anything pushed stays in history.
- If a secret is committed, treat it as leaked: rotate it first, then remove it.

### No personal or private details in docs

- No personal home paths, user names in paths, private network addresses, internal
  host names or personal machine names. Use generic placeholders such as
  `localhost`, `<data-dir>` or `example.com`.
- No screenshots that show real accounts, libraries, tokens or addresses.

### Validate input

- Treat every request body, query parameter, header, file name, uploaded file and
  media metadata as untrusted. Validate type, length and range at the edge.
- Use parameterised SQL; never build queries by string concatenation.
- Resolve and check file paths against the library roots before reading; reject path
  traversal.
- Never put runtime data into log message templates; log it as structured fields.
- Do not render user-supplied HTML; escape output in the web app.

### Server-side role checks

- Every role rule is enforced by the API on the server, with a test that calls the
  endpoint as a role that must be refused. Hiding UI in the web or native apps is not a
  rule.
- A new endpoint is not done until it has its role check and its role test.
- Signed URLs and tokens are short-lived and scoped to what they grant.

### Dependency hygiene

- Add a dependency only when it earns its place; prefer the standard library and the
  libraries already in use.
- Pin versions through the lockfiles (`go.sum`, `uv.lock`, `pnpm-lock.yaml`) and commit
  them with the change; `make lockfile-check` fails on drift.
- Use maintained, widely used packages from official registries. Never vendor code from
  an unknown source.
- Act on security alerts for dependencies promptly, and keep upgrades in their own
  focused changes.
- CodeQL code scanning runs on pull requests; fix new high-severity alerts before merge.

## Reporting vulnerabilities

- Email **security@maktaba.dev** with what you found, how to reproduce it, the version
  or commit, and the impact you expect.
- Never report a vulnerability in a public issue, pull request, discussion or chat.
  Do not publish details until a fix has shipped and the disclosure is agreed.
- The full policy, including scope and response times, is at
  [maktaba.dev/security/policy](https://maktaba.dev/security/policy/).

See also the contributor [conventions](../contributing/conventions.md) and the
[pull request guide](../contributing/pull-requests.md).
