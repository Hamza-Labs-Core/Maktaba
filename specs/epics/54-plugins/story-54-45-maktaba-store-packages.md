# Story 54.45 — Maktaba's plugins as store packages

> Epic 54 · Phase 5 · Depends on: 54.14, 54.15, 54.16, 54.22, 54.27, 54.44 · Needed by: 54.46 to 54.51 · Partly built

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

Today `[trusted]` counts when the package's `files.json` digest is in the running release's signed checksums and its version equals the server's. With packages delivered apart from the release, it counts when the package is countersigned by the store, its developer key is Maktaba's (pinned in the server's trust root, 54.14), and its named statements are exactly the ones the running server embeds for that plugin. **A Maktaba plugin's database statements change only with a server release** (decided 2026-11-19): there is no signed list of statements per plugin version, and a package whose statements differ from the server's is refused, so a server never runs SQL it did not ship. A package that fails any of these is refused with `plugin_trusted_unsigned`.

### 4. Where packages are published

- In the store catalog, for homes that reach the store.
- On each Maktaba release's download page as files, for offline homes (54.49).

## Decided

- **Statements move with the server** (2026-11-19). A Maktaba plugin's database statements change only with a server release, as README §15.6 has it for the schema they run against. §3 checks a store package's statements against the server's own, rather than a signed list per plugin version with a server range.

## Tests

- A package built from each folder installs through the existing install path in a scratch home and runs its stages with parity (54.26).
- The store refuses a `tv.maktaba.*` id from any account but Maktaba's.
- Every refusal of §3 answers `plugin_trusted_unsigned` and runs nothing.

## As built

- **One package per plugin but core's.** `tools/plugins/build.py` leaves out a plugin whose
  `[trusted]` component is not switchable in the catalog (`tv.maktaba.core-media`, for
  `core.pipeline`): core is not a plugin. The release check (`fp-packages`) refuses a core
  package, so a release carries 12 packages. Moving the plugins' code out of the pipeline
  wheel waits for the install path (54.46): until a home installs packages, the runner loads
  the code from the wheel, and taking it out would stop every stage.
- **Signed by Maktaba's key.** `build.py --developer-key <file>` signs each package's
  `files.json` with Maktaba's developer key: the seed in base64, as `mkp keys new` keeps it.
  It uses `shared/plugins/cmd/fp-sign` and `mkplugin -developer-sig`. The signature sits beside
  `files.json` and is not listed in it, so the release's signed checksums are the same either
  way. `release.yml` signs when the `MAKTABA_PLUGINS_DEVELOPER_KEY` secret is set, and warns
  and builds unsigned until then.
- **The pin.** `shared/plugins/maktaba-keys.json`, embedded in the signed server binary, lists
  Maktaba's developer key ids. It is empty until the key is made, and a test fails if a key is
  added without that test changing.
- **The rule, §3** (`shared/plugins/fppackage.CheckStore`). A store package of a Maktaba plugin
  is trusted only when all of these hold:
  - it is a plugin of the server's bundle, with a switch;
  - the store countersigned it (`store.sig` at level automated or reviewed, by a store key the
    home verified);
  - its `developer.sig` is by a pinned key;
  - its manifest, strings and statements are exactly the server's at the server's version,
    and its wheel is that plugin's own code (`CheckContents`).
  
  As decided, the statements change only with a server release, so a package bringing other
  SQL is refused. Every refusal is `plugin_trusted_unsigned`; another version is
  `plugin_trusted_version`.
- **One implementation for both checks.** The content and wheel checks moved from
  `api/internal/plugins/firstparty` into `shared/plugins/fppackage`. The release's own check
  and the home's store check now run the same code, and the install path (54.46, in the
  supervisor) can call it.
- **The store's publisher, §2.** A store admin sets the one Maktaba publisher account
  (`POST /v1/admin/maktaba-publisher {account}`, passkey confirmed, audited). The account gets
  the prefix `tv.maktaba` of kind `maktaba` (catalog migration `0006`), which is never claimed
  and is held by one account only (`409 maktaba_publisher_taken`). `prefixCovers` gates both
  a submission's start and check C2. Maktaba's own ids (`tv.maktaba.*`, community prefixes
  aside) are covered only by that prefix, and that prefix covers no community id.
- **The store's checks of a native package.** The store reads a Maktaba id's manifest in the
  trusted mode. The Rust validator gained `validate_trusted`, rule for rule with Go's
  `ValidateTrusted`, and every first-party manifest of the repo is valid only in that mode. A
  `native-python` package of one of those ids has no component, so the checks change:
  - C4 checks its shape: exactly the manifest, the two strings files, `statements.json` and its
    wheel named for its version (`package_native_shape`);
  - C3 takes its exact `=<version>` as an upper bound;
  - C5 is skipped, since there are no imports;
  - C7's static rules scan the wheel;
  - C8 and C10 run nothing, and a person reviews it.
  
  The home then holds it to its own server (§3).
- **Not yet:** a package installed through the install path with parity, which needs 54.46.
- **Tests.**
  - `shared/plugins/fppackage/store_test.go`: the trusted case at both levels, and every
    refusal. These cover no store signature, no store keys, another developer's key, an empty
    pin, other statements or a wider manifest signed by both keys, a `files.json` changed after
    signing, core's plugin, another version, a third-party id and an unknown one. Each rule was
    seen failing its test when removed.
  - `api/internal/plugins/firstparty/release_test.go`: a package built with `--developer-key`
    and countersigned is trusted by `CheckStore`, its `files.json` and the release's check
    unchanged. Core is never packaged, and a core package in a release is refused.
  - `fp-sign`'s and `mkplugin -developer-sig`'s own tests, and `test_build_packages.py`.
  - `store/test/workers/maktaba.test.ts` covers the publisher and the submissions:
    - only a store admin with a passkey sets the publisher, only for an active developer and
      only once, and `tv.maktaba` cannot be claimed;
    - `prefixCovers`'s cases;
    - another account's `tv.maktaba.*` submission is refused at its start;
    - the publisher's native package passes C2 to C4, skips C5 and C8, and goes to a person;
    - a file more is blocked at C4;
    - the publisher cannot enter a community prefix.
  - The Rust tests: every first-party manifest is valid only trusted, the trusted mode is no
    wider than Go's, and a native package is read trusted, its shape checked.

