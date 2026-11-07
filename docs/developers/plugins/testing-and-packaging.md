# Testing and packaging

> **Status: built for Rust (Phase 1).** `mkp` (in `plugind/crates/mkp`) makes a Rust project
> (`mkp new rust <id> <folder>`), checks a package the way the home does (`mkp check`), runs a
> stage in the harness, the host's own code against a mock plugin API answering from a fixtures
> file (`mkp test --stage <name> --fixtures api.json`), and packs the `.mkplugin` (`mkp pack`).
> An admin installs the file from Settings, Plugins, with Allow unreviewed plugins on. The
> TypeScript, Python and Go templates, signing (`mkp pack --sign`, `mkp keys`), `mkp run` and
> developer mode are not built yet. The rest of this page is the design in
> [story 54.12](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/story-54-12-sdk-harness.md)
> and
> [story 54.10](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/story-54-10-file-install.md),
> which can change.

## Languages

| Tier | Language | Toolchain | Notes |
|---|---|---|---|
| 1 | Rust | `cargo component`, `wit-bindgen` | smallest, fastest components |
| 1 | TypeScript / JavaScript | `jco componentize` | |
| 1 | Python | `componentize-py` | pure Python only: no NumPy or torch; use host services |
| 2 | Go | TinyGo `-target=wasip2` | a subset of Go; best effort |

Tier 1 languages get templates, SDK helpers and end-to-end tests on every SDK change. Tier 2
gets a template and a smoke test. Other languages that produce components can work, without a
template.

Heavy work (decoded frames, audio, transcripts, embeddings, the language model) is done by
Maktaba and reached through the plugin API, not bundled in your plugin.

## The `mkp` tool

One CLI for every language:

| Command | What it does |
|---|---|
| `mkp new <template> <id>` | a project with `manifest.toml`, `i18n/en.json`, `i18n/ar.json`, an icon placeholder, a sample stage, a fixture and a test |
| `mkp build` | runs your toolchain, then the same manifest validation and import audit as the home and the store |
| `mkp test` | runs your plugin in the local harness against its fixtures |
| `mkp keys new`, `list`, `export-public` | your Ed25519 signing key |
| `mkp pack --sign` | writes `files.json`, signs it and makes the `.mkplugin` |
| `mkp run --home <url>` | installs to a home in developer mode over the admin API |
| `mkp submit` | uploads to the store (Phase 2) |

The SDK version matches the plugin API it targets: `mkp 1.2.x` builds for
`maktaba:plugin@1.2`.

The bindings are generated from the WIT for each language, with helpers for typed events,
subjects, a chapters builder, a WebVTT writer, `data` as a key-value map, and logging with
codes.

## Getting started

```sh
mkp new rust dev.example.silence-chapters
cd silence-chapters
mkp build
mkp test
```

Then edit `manifest.toml` (see [The manifest and the package](manifest.md)), declare only the
capabilities you use, and add both English and Arabic strings.

## The test harness

`mkp test` embeds the same Wasmtime host code that runs plugins at home, plus a mock plugin API
in the same process.

- The mock serves every call from your fixtures: `fixtures/library.json` (videos and series),
  `fixtures/media/` (small sample files) and `fixtures/transcripts/`.
- It enforces capabilities, scope and quotas with the same rule tables as the real API, so a
  plugin that passes locally is not surprised at home.
- It records every call to `trace.json` along with your outputs. `mkp test --golden` compares
  them with stored goldens.
- It uses your manifest's limits. `--limits strict` uses the store's review limits; run with
  it before you submit.

The store's automated checks run your fixtures through the same harness, so keep them
representative.

## Samples

| Sample | Language | Shows |
|---|---|---|
| `silence-chapters` | Rust | `media.audio`, `chapters.write` |
| `tag-from-filename` | TypeScript | `library.read`, `tags.write`, settings |
| `arabic-title-normaliser` | Python | `suggest`, Arabic text handling |
| `weekly-report` | Go (TinyGo) | a schedule, `data`, `notify.admin` |

## Building and packing

1. `mkp build` produces `plugin.wasm` and fails on any manifest error or forbidden import.
2. `mkp pack --sign` writes `files.json` (path, sha256 and size of every file), signs it with
   your developer key into `signatures/developer.sig`, and makes a `tar` + zstd `.mkplugin`.
3. Check the size: at most 50 MB. The Python hello-world template targets 25 MB or less.

## Installing from a file

A home installs a package from a file, offline, from Settings, Plugins, **Install from a
file**, or with the CLI:

```sh
maktaba-server plugins install silence-chapters-1.4.0.mkplugin
```

| The file carries | Installs | Badge |
|---|---|---|
| store and developer signatures (Phase 2) | always, like from the store | none, or Reviewed |
| your developer signature only | only with **Allow unreviewed plugins** on | Unreviewed |
| no signature | only with Allow unreviewed plugins on | Unreviewed |
| an unpacked folder in developer mode | only with Allow unreviewed plugins on | Development |

Until the store exists, every file is unreviewed. **Allow unreviewed plugins** is off by
default; turning it on asks for the admin's password again and is audited. The admin then
reviews the permission dialog and types the plugin's name to confirm. The sandbox, grants and
quotas are exactly the same for reviewed and unreviewed plugins.

## Developer mode

```sh
maktaba-server plugins dev ./silence-chapters
```

Developer mode installs from an unpacked folder, watches it, and on change re-validates and
reloads within about 5 seconds. It needs Allow unreviewed plugins on, marks the plugin
Development, and removes it when the command stops unless you pass `--keep`. It runs in the
same host and sandbox as any plugin.

The CLI also has `list`, `show`, `enable`, `disable`, `start`, `stop`, `update`, `rollback`,
`remove`, `grants` and `policy`, for homes without a browser.

## Updating your plugin on a home

- A new version installs beside the old one. The old version drains, then the new one takes
  over. The previous version is kept for rollback.
- If the new version asks for more capabilities, it waits for the admin's approval.
- Bump `data_version` when your stored data changes shape. Rolling back to a version with a
  lower `data_version` is refused unless the admin also clears your plugin's data.
- When an admin turns your plugin off, it stays off through updates, restores and reinstalls
  until an admin turns it on.
