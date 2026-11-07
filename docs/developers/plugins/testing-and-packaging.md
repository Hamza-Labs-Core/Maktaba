# Testing and packaging

> **Status: built for Rust (Phase 1).** `mkp` (in `plugind/crates/mkp`) makes a Rust project
> (`mkp new rust <id> <folder>`), checks a package the way the home does (`mkp check`), runs a
> stage in the harness, the host's own code against a mock plugin API answering from a fixtures
> file (`mkp test --stage <name> --fixtures api.json`), packs the `.mkplugin` (`mkp pack`),
> signs it with your key (`mkp keys new`, `mkp pack <folder> <out> --key <id>`), and installs a
> folder on a home in developer mode (`mkp run`). An admin installs the file from Settings,
> Plugins, or with `maktaba-server plugins`, with Allow unreviewed plugins on; `maktaba-server
> plugins dev <folder>` reloads a folder on every save. The TypeScript, Python and Go templates
> are not built yet. The manifest is in [manifest](manifest.md), the WIT and the
> API in the [plugin reference](reference.md). The rest of this page is the design in
> [story 54.12](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/story-54-12-sdk-harness.md)
> and
> [story 54.10](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/story-54-10-file-install.md).

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
| `mkp pack <folder> <out.mkplugin> --key <id>` | writes `files.json`, signs it with your key and makes the `.mkplugin` (without `--key`: unsigned) |
| `mkp run <folder> --home <http://home:port> --user <admin>` | installs the folder on a home in developer mode (Development, turned on) over the admin API; run it again after a change to update it. The password comes from `MKP_HOME_PASSWORD` or the terminal; it speaks HTTP to the home's address on your network (over HTTPS, use developer mode on the home) |
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
2. `mkp pack <folder> <out.mkplugin> --key <id>` writes `files.json` (path, sha256 and size of
   every file), signs it with your developer key into `signatures/developer.sig`, and makes a
   `tar` + zstd `.mkplugin`.
3. Check the size: at most 50 MB. The Python hello-world template targets 25 MB or less.

## Installing from a file

A home installs a package from a file, offline, from Settings, Plugins, **Install from a
file**, or with the CLI:

```sh
maktaba-server plugins install silence-chapters-1.4.0.mkplugin --name "Silence chapters" --enable
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
`remove`, `grants` and `policy`, for homes without a browser:

```sh
maktaba-server plugins policy --allow-unreviewed=on
maktaba-server plugins grants dev.example.silence-chapters --grant media.read,chapters.write --all-libraries
maktaba-server plugins remove dev.example.silence-chapters --purge
```

They call the same admin routes as Settings, Plugins, with the components CLI's flags and exit
codes: every write shows what it changes and asks `Continue? [y/N]` unless `--yes`; `--json`
on `list` and `show`; `--dsn` overrides `DATABASE_URL`. Exit codes: 0 done, 1 refused (the
plugin code is printed), 2 a write with no terminal to ask on and no `--yes`, 3 the server is
not running. An unreviewed install asks for the plugin's name (`--name`), as the dialog does.

## Updating your plugin on a home

- A new version installs beside the old one. The old version drains, then the new one takes
  over. The previous version is kept for rollback.
- If the new version asks for more capabilities, it waits for the admin's approval.
- Bump `data_version` when your stored data changes shape. Rolling back to a version with a
  lower `data_version` is refused unless the admin also clears your plugin's data.
- When an admin turns your plugin off, it stays off through updates, restores and reinstalls
  until an admin turns it on.
