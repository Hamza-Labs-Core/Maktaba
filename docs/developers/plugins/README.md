# Building Maktaba plugins

> **Status: Phase 1 is on `main`.** The manifest and the `.mkplugin` package, the plugin host
> with its OS sandbox, the plugin API with media handles, plugin stages in the queue, install
> from a file, grants, the lifecycle, health and the circuit breaker, settings, the admin's
> Plugins page, and `mkp` with its test harness (Rust). Plugins run on a home with Postgres, on
> macOS and Linux (not Windows yet). Phase 2 is being built: signing is on `main`. Phase 3 is planned. The [plugin reference](reference.md) is generated from
> the code when this site is built, so it always matches `main`. Maktaba's own stages are
> becoming first-party plugins on the same interface: see [Maktaba's own plugins](first-party.md).

## What a plugin is

A plugin adds a function to a Maktaba home without changing Maktaba's own code. It:

- **takes jobs from the queue.** Your plugin declares stages. Maktaba queues a job for a stage
  when a stable event happens (a video was added, a transcript is ready), on a schedule, or
  when another of your own stages asks for it.
- **has no internet.** It cannot open a socket, resolve a name or make an HTTP request. It
  cannot see the disk. This is enforced twice: the WebAssembly runtime links no network or
  filesystem interfaces, and the process that runs plugins is locked down by the operating
  system.
- **gets only the API access the admin grants.** You declare capabilities such as
  `library.read` or `chapters.write`. The admin grants them at install, picks which libraries
  the plugin can see, and the API checks both on every call.

A plugin writes its results back through the API: metadata fields, tags, chapters,
subtitles, artwork, suggestions for the admin to review, and its own data. It can declare a
settings form and, later, read-only panels that the apps draw.

A plugin is one signed package (`.mkplugin`) holding a WebAssembly component, a manifest,
English and Arabic strings, an icon and optional UI declared as data. You can write it in
Rust, TypeScript or JavaScript, Python, or Go (TinyGo).

## What a plugin cannot do

- Reach the internet, under any permission. There is no `network` capability.
- Change playback or streaming, or add API routes for the apps.
- Run code inside the apps. Settings forms and panels are data; Maktaba draws them.
- Run at boot or outside a job.
- Read users, sessions, tokens, secrets, file paths or machine addresses.
- Talk to another plugin, except by reading data that plugin chose to export, with a grant.

## Plans and phases

The design ships in phases. Each guide marks which phase a feature belongs to.

| Phase | What | Status |
|---|---|---|
| 1 | Manifest and package, plugin host and sandbox, plugin API and media handles, queue stages | On `main` |
| 1 | Lifecycle, grants and roles, health, install from a file, settings, the Plugins page, SDK and test harness | On `main` (the SDK for Rust only) |
| 2 | Signing and trust | On `main` ([Signing and the chain of trust](trust.md)) |
| 2 | The Maktaba store, automated checks and review, catalog, updates and the kill switch | Being built |
| 3 | Paid plugins, panels in every app, local compute (embeddings and the home's language model) | Local compute and panels on `main` (next release); paid plugins: the store's payments on `main`, buying waits for the store ([Selling a plugin](selling.md)) |

Maktaba's own queue stages are also planned to move behind the same manifest, dispatch and
plugin API as first-party plugins. That work does not change what third-party plugins can do.

## What exists today

On `main`, you can write a plugin in Rust with `mkp`, test it in the harness, pack it, and an
admin can install it from a file on Settings, Plugins. The supervisor starts the host
`maktaba-plugind` only while a plugin is on; the pipeline runs each job in a fresh sandbox, and
the plugin API answers it on a unix socket. See [Installing and managing plugins](installing.md)
for what the admin does.

Separately, Maktaba's pipeline loads Python packages registered under the `maktaba.extensions`
entry point group. That is a developer feature for Maktaba's own integrations: such a package
runs inside the pipeline worker with full access. It is not the plugin system and is not meant
for code from other people.

## The guides

- [The manifest and the package](manifest.md): the plugin contract, `manifest.toml` and the
  `.mkplugin` format.
- [The sandbox](sandbox.md): no internet, the OS sandbox, resource limits, crash isolation and
  health.
- [Queue stages and the plugin API](queue-and-api.md): events, schedules, how a job runs, the
  API interfaces, media handles, grants, settings, panels and local compute.
- [Installing and managing plugins](installing.md): install from a file, grants and
  libraries, turning a plugin on and off, updates and rollback, removing it, settings, health
  and the Plugins page.
- [Testing and packaging](testing-and-packaging.md): the SDK, templates, the local test
  harness, building, packing and installing from a file.
- [Publishing](publishing.md): signing keys, the Maktaba store, review, the catalog, the kill
  switch and paid plugins.
- [Plugin reference](reference.md), generated from the code: every manifest key, capability,
  event, API function, refusal code and limit.

## A plugin in one picture

```
queue job (pool "plugins")
  -> pipeline worker asks the API for a per-job token
  -> plugin host runs your component's run(stage) in a fresh sandbox
       your code calls maktaba:plugin/* imports
       the host forwards each call with the token to the plugin API
       the API checks token, capability, library scope and quota
  -> result goes back to the queue (retry, park or done)
```

## Read more

- [Epic 54 design](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/README.md)
- [Research behind the design](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/research.md)
