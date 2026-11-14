# Story 54.45 — Maktaba's plugins as store packages

> Epic 54 · Phase 5 · Depends on: 54.14, 54.15, 54.16, 54.22, 54.27, 54.44 · Needed by: 54.46 to 54.51 · Not built

## Why

A plugin Maktaba publishes is a store plugin whose publisher is Maktaba. Today it is not a package at all: its code rides in the pipeline wheel and its manifest in a bundle embedded in the server, so it cannot be installed, removed or updated on its own. This story makes each one a real signed package that the store lists under the verified publisher Maktaba. It reverses the part of README §15.6 that said the store never lists, countersigns or serves a `tv.maktaba.*` package.

## Description

### 1. One package per plugin

- `tools/plugins/build.py` (planned in 54.27 §2, not built) builds one `.mkplugin` per folder of `plugins/first-party/` except core's: manifest, strings, named statements, and the plugin's Python code as its own wheel. The plugin's code leaves the pipeline wheel; `_lib` becomes a wheel the packages depend on.
- The package carries no heavy dependency. ML wheels, models and `yt-dlp` stay Epic 51 assets that the plugin's component `requires`, installed when the plugin is.
- Core's plugin (`tv.maktaba.core-media`) is not packaged for the store: it stays in the server and its wheel.

### 2. The publisher

- The store has one publisher account for Maktaba, marked verified by the store operator. Only it may submit an id under `tv.maktaba.` (the store refuses the prefix to anyone else, as the manifest check already does at install).
- A Maktaba package is signed by Maktaba's developer key and countersigned by the store after the same automated checks as any plugin (54.16), plus the trusted-manifest checks of 54.22.

### 3. What makes `[trusted]` count

Today `[trusted]` counts when the package's `files.json` digest is in the running release's signed checksums and its version equals the server's. With packages delivered apart from the release, it counts when: the package is countersigned by the store, its developer key is Maktaba's (pinned in the server's trust root, 54.14), and the server's own signed list of trusted statements for that plugin version covers every statement it registers. The statement list for each published plugin version is signed and served with the catalog, so a server never runs SQL it cannot verify. A package that fails any of these is refused with `plugin_trusted_unsigned`.

### 4. Where packages are published

- In the store catalog, for homes that reach the store.
- On each Maktaba release's download page as files, for offline homes (54.49).

## Open questions

- Whether a Maktaba plugin's database statements may change without a server release, given that statements move with the schema (README §15.6). The proposal above keeps them tied to a signed list per plugin version with a server range; the alternative is that a statement change always needs a server release.

## Tests

- A package built from each folder installs through the existing install path in a scratch home and runs its stages with parity (54.26).
- The store refuses a `tv.maktaba.*` id from any account but Maktaba's.
- Every refusal of §3 answers `plugin_trusted_unsigned` and runs nothing.
