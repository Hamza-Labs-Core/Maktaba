# Story 54.12 — SDK, templates, test harness and docs

> Epic 54 · Phase 1 · Depends on: 54.02, 54.04 · Needed by: 54.16

## Why

A plugin system lives or dies by how quickly a developer goes from nothing to a working plugin
(Plex's plugins faded when the framework aged and was hard to support). A developer should be
able to start from a template in the language they know, run their plugin against fake media
on their own machine without a Maktaba server, and package and sign it with one command.

## Description

### 1. Languages

| Tier | Language | Toolchain | Why |
|---|---|---|---|
| 1 | Rust | `cargo component`, `wit-bindgen` | smallest, fastest components; what the host is written in |
| 1 | TypeScript / JavaScript | `jco componentize` (StarlingMonkey) | the largest developer pool |
| 1 | Python | `componentize-py` | Maktaba's pipeline language; pure Python only (no NumPy: use host services) |
| 2 | Go | TinyGo `-target=wasip2` | Go developers; a subset of Go |

Tier 1: templates, SDK helpers, e2e tests in CI on every SDK change. Tier 2: template and a
smoke test; best effort. Other languages that make components (C, C#) work but get no template.

### 2. The SDK

`tools/plugin-sdk/` publishes:

- **`mkp`**, one CLI (Rust, prebuilt for macOS, Linux, Windows; also `cargo install`):
  - `mkp new <template> <id>`: a project with `manifest.toml`, `i18n/en.json`, `i18n/ar.json`,
    an icon placeholder, a sample stage, a fixture and a test;
  - `mkp build`: runs the language toolchain, then the same validation and import audit as the
    home and the store (54.01, 54.02);
  - `mkp test`: runs the plugin in the **harness** (§3) against its fixtures;
  - `mkp keys new|list|export-public` (Ed25519, stored in the OS keychain or a passphrase file);
  - `mkp pack --sign`: writes `files.json`, signs it, makes the `.mkplugin`;
  - `mkp run --home <url>`: installs to a home in developer mode over the admin API (needs the
    home's admin to have allowed unreviewed plugins);
  - `mkp submit` (Phase 2): uploads to the store.
- **Bindings** generated from `shared/plugins/wit/` for each language, with small helpers:
  typed events, `subject()` helpers, a `Chapters` builder, a VTT writer, `data` as a key–value
  map, logging macros that use codes.
- **Version pinning:** the SDK version equals the plugin API version it targets
  (`mkp 1.2.x` → `maktaba:plugin@1.2`).

### 3. The harness

`mkp test` embeds the **same Wasmtime host code** as `maktaba-plugind` (the `plugind` crate is
a library with a thin binary) and a **mock plugin API** in-process:

- the mock serves every route from fixtures: `fixtures/library.json` (videos, series),
  `fixtures/media/` (small sample files; frames and audio extracted by the harness's bundled
  ffmpeg-free decoders for the fixture formats it ships, or by a local ffmpeg if found),
  `fixtures/transcripts/`;
- it enforces capabilities, scope and quotas with the **same rule tables** as the Go API
  (generated from `capabilities.toml` and a shared JSON table of scope rules), so a plugin
  that passes locally is not surprised at home;
- it records every call (`trace.json`) and the outputs; `mkp test --golden` compares with
  stored goldens;
- limits are the manifest's; `--limits strict` uses the store's review limits;
- `mkp test --with <other.mkplugin>` runs a two-plugin chain: events the first emits are
  delivered to the second's subscriber stages with their lineage, and `mkp flow` draws the
  flow as a text chart and Mermaid (54.41). The SDK's bindings add `events.emit` and its
  templates a `flow.toml` of one plugin (54.39, 54.40).

The Go API and the mock are checked against each other in CI: the same request corpus
(`shared/plugins/testdata/api-corpus/`) goes through both and the answers must match.

### 4. Docs on maktaba.dev

`docs/plugins/` in this repo, published to **maktaba.dev/plugins** by the wiki site build:

- Getting started per language (hello world in 10 minutes);
- the manifest reference (generated from `manifest.schema.json`);
- the API reference (generated from the WIT, with the capability each call needs);
- events, stages and schedules; limits; settings schemas; panels (Phase 3);
- the security model in plain words: no internet, no files, what the admin sees;
- store guidelines and the review checklist (Phase 2);
- English first; Arabic pages for getting started and the guidelines.

### 5. Sample plugins

In `tools/plugin-sdk/examples/`, also the e2e fixtures of `plugins-e2e`:

| Sample | Language | Shows |
|---|---|---|
| `silence-chapters` | Rust | `media.audio`, `chapters.write` |
| `tag-from-filename` | TypeScript | `library.read`, `tags.write`, settings |
| `arabic-title-normaliser` | Python | `suggest`, Arabic text handling |
| `weekly-report` | Go (TinyGo) | schedule, `data`, `notify.admin` |

## Acceptance criteria

- **Given** a clean machine with the SDK and a language toolchain,
  **when** a developer runs `mkp new rust dev.example.hello && mkp build && mkp test`,
  **then** it passes in under 2 minutes after toolchain install.
- **Given** a plugin calling `tags.write` without declaring it,
  **when** `mkp build` runs,
  **then** it fails with `plugin_undeclared_import`, the same code the home gives.
- **Given** the API corpus,
  **when** run against the Go API and the mock,
  **then** every answer (status and code) matches.
- **Given** the Python template's hello world,
  **when** packed,
  **then** it is ≤ 25 MB (N15).
- **Given** a change to the WIT,
  **when** CI runs,
  **then** bindings regenerate and every tier-1 sample builds and passes `mkp test`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI (3 OS) | templates × tier-1 languages | new, build, test | pass |
| T02 | CI | TinyGo template | build, test | pass (tier 2, non-blocking) |
| T03 | unit (rust) | undeclared import | build | `plugin_undeclared_import` |
| T04 | integration (go + rust) | API corpus | Go API vs mock | identical answers |
| T05 | CI | Python hello | pack | ≤ 25 MB |
| T06 | unit (rust) | `mkp pack --sign` | verify with the Go verifier | valid |
| T07 | docs build | WIT, schema | generate | references match the source (no drift) |

## Edge cases

- **componentize-py and a dependency with a C extension**: `mkp build` fails with a message
  naming the package and suggesting host services or a pure-Python alternative.
- **Developer on Windows without a keychain**: passphrase-protected key file.
- **Large fixtures**: the harness refuses fixtures > 50 MB total, so the store's sandbox run
  stays fast.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugind/` | Split into `plugind-core` (library) and the binary, so the harness reuses it. |
| `tools/plugin-sdk/mkp/` (new, Rust) | CLI, harness, mock API. |
| `tools/plugin-sdk/{rust,js,python,go}/` (new) | Bindings, helpers, templates. |
| `tools/plugin-sdk/examples/` (new) | Samples. |
| `shared/plugins/testdata/api-corpus/` (new) | Shared corpus. |
| `docs/plugins/` (new) | Docs, built to maktaba.dev/plugins. |
| `.github/workflows/` (SDK job in `_unit.yml`, `plugins-e2e`) | T01–T07. |

### Tests

- CI T01, T02, T05, T07; unit T03, T06; integration T04.

### Rollout

The SDK is published as `mkp` release assets with each server release that changes the plugin
API, and to crates.io / npm / PyPI under `maktaba-plugin-*` names (owner to approve the
registry accounts; no publishing from this epic's build without that).

### Rollback

SDK versions are pinned to API versions; an old SDK keeps working with hosts that support its
API version.

### Dependencies

54.02 (host core), 54.04 (API routes and rule tables).
