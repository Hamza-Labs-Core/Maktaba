# Story 54.01 — The manifest and the package

> Epic 54 · Phase 1 · Depends on: — · Needed by: 54.02, 54.04, 54.07, 54.10, 54.14

## Why

Every other part of this epic reads the same few facts about a plugin: who it is, which
Maktaba it runs on, what it asks for, what it adds to the queue, and how much it may use. If
the Go API, the Rust host, the pipeline, the store and the SDK each parse that differently, a
plugin will pass the store and fail at home, or worse, a field one side ignores will let a
plugin ask for more than the admin saw. This story defines the manifest and the package once,
in one schema, with one validator per language generated from it.

## Description

### 1. The package

A `.mkplugin` file is a `tar` archive compressed with zstd (the same container as Epic 51
packs, `shared/go/componentpack`; its head is `manifest.toml` and `files.json` instead of a
pack's `pack.json` and `files.sha256`, 54.38),
holding:

| Path | Required | What |
|---|---|---|
| `manifest.toml` | yes | §2 |
| `plugin.wasm` | yes | one WebAssembly component (not a core module) |
| `files.json` | yes | `[{path, sha256, size}]` for every other file, sorted by path |
| `i18n/en.json`, `i18n/ar.json` | yes | every key the manifest and UI files name |
| `icon.png` | yes | 256×256 PNG, ≤ 200 KB |
| `ui/*.json` | no | settings schema, panels |
| `fixtures/` | no | test inputs the store's sandbox run uses (54.16); never installed |
| `LICENSE` | yes | text matching `licence` |
| `signatures/developer.sig`, `signatures/store.sig` | no in Phase 1 | 54.14 |

Limits: ≤ 50 MB compressed, ≤ 200 MB unpacked, ≤ 500 files, paths ≤ 200 bytes, only regular
files and folders (no links, devices, absolute paths or `..`), UTF-8 names in NFC.

### 2. The manifest

The fields and their meaning are in the README §4.6. This story pins the rules:

| Field | Rule |
|---|---|
| `schema` | integer, `1` |
| `id` | reverse-DNS, 3–6 labels, `[a-z0-9-]`, ≤ 100 chars; reserved prefixes `tv.maktaba.` (except `tv.maktaba.community.`), `dev.maktaba.`, any label `maktaba` or `official` |
| `version` | semver 2.0, no build metadata |
| `plugin_api` | `MAJOR.MINOR` of `maktaba:plugin` |
| `maktaba` | a semver range with a lower bound; an upper bound is required for store listing (Nextcloud's lesson) |
| `platforms` | `["any"]` or a subset of `darwin-arm64`, `windows-amd64`, `linux-amd64`, `linux-arm64` |
| `languages` | BCP 47 codes; may be empty |
| `licence` | SPDX id or `proprietary` |
| `price` | `free` or a store product id (Phase 3) |
| `data_version` | integer ≥ 1 |
| `strings.name`, `strings.summary` | keys present in both `i18n` files; name ≤ 40 chars, summary ≤ 140 in each language |
| `capabilities.required/optional` | names from `shared/plugins/capabilities.toml`; no duplicates across the two lists; `plugin-data.read:<id>` takes a valid id |
| `resources.*` | integers within the README §4.7 max; missing means default |
| `stage[]` | 1–20 stages; `name` `[a-z][a-z0-9_]{0,39}` unique; `subject` in `video, series, season, library, catalog`; `export` names a function the component exports (checked in 54.02's audit); `version` ≥ 1; `may_request` names this plugin's stages only |
| `stage.trigger[]` | `event` from `shared/plugins/events.toml` whose subject matches the stage's |
| `schedule[]` | `stage` is a catalog stage; `every_minutes_setting` names an integer field in the settings schema with `minimum` ≥ 15 |
| `settings.schema` | a path in `ui/`; JSON Schema subset (54.11) |
| `panel[]` | Phase 3 (54.20); accepted and ignored before |

Unknown keys are an **error**, not a warning: a field one side does not understand must not be
installed silently.

### 3. One schema, many readers

- `shared/plugins/manifest.schema.json` (JSON Schema 2020-12) is the source. TOML is parsed to
  JSON and checked against it, then the cross-field rules above (stage names, i18n keys,
  settings fields) run in a small shared rule set.
- `shared/plugins/capabilities.toml` lists every capability: name, risk (`low`, `medium`,
  `high`), the WIT interface it unlocks, its `title_key` and `detail_key` in Maktaba's own
  strings, and `since` (plugin API minor).
- `shared/plugins/events.toml` lists every event (README §4.2): name, subject, the core stage
  it maps to, `since`.
- Validators: Go (`shared/go/pluginmanifest`, used by the API, the supervisor and the cloud
  store), Rust (`plugind/crates/manifest`), Python (`pipeline/src/maktaba_pipeline/plugins/manifest.py`,
  the pipeline loader), TypeScript (`tools/plugin-sdk/js/manifest.ts`, the SDK). A shared
  corpus `shared/plugins/testdata/manifests/{valid,invalid}/*.toml`, each invalid file naming
  the error code it must raise, runs in all four.
- Errors are codes (`manifest_unknown_key`, `manifest_bad_id`, `manifest_reserved_id`,
  `manifest_missing_string`, `manifest_bad_capability`, `manifest_limit`, …) with English and
  Arabic words in `web/src/i18n/{en,ar}.json`.

### 4. Stage names in the queue

A plugin stage's `job_stages.name` is `p_<8 hex of sha256(id)>_<stage name>` (≤ 63 chars),
stable across versions, so renaming nothing keeps the queue, and two plugins can never collide
with each other or with core stages (core names never start with `p_`; a lint in
`engine/graph.py`'s `validate()` enforces it).

## Acceptance criteria

- **Given** a valid package,
  **when** any of the four validators reads it,
  **then** all four accept it and produce the same normalised JSON (golden compare).
- **Given** a manifest with an unknown key `network = true`,
  **when** it is validated,
  **then** it is refused with `manifest_unknown_key` in every validator.
- **Given** a manifest whose `strings.name` key is missing from `i18n/ar.json`,
  **when** it is validated,
  **then** it is refused with `manifest_missing_string` naming `ar`.
- **Given** an archive with a member `../../evil` or a symlink,
  **when** it is unpacked,
  **then** nothing is written and the error is `package_bad_path`.
- **Given** `id = "tv.maktaba.subtitles"` from a developer,
  **when** it is validated,
  **then** it is refused with `manifest_reserved_id`.
- **Given** a trigger `event = "transcript.ready"` on a `library` stage,
  **when** it is validated,
  **then** it is refused (`manifest_subject_mismatch`).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit ×4 (go, rust, py, ts) | the shared corpus | validate each file | valid pass; each invalid raises its named code |
| T02 | unit ×4 | valid corpus | normalise | byte-identical JSON across languages |
| T03 | unit (go) | archives with `..`, absolute path, symlink, hardlink, device, 501 files, 201 MB unpacked | unpack | refused with the right code; temp dir empty |
| T04 | unit (go) | `files.json` lists a wrong sha256 | verify | `package_digest_mismatch` |
| T05 | unit (py) | `validate()` with a core stage named `p_x` | run | `GraphError` |
| T06 | unit (ts) | every error code | lookup | en + ar words exist |
| T07 | unit (go) | stage name hashing | two ids, same stage name | different `job_stages` names; same id across versions → same name |

## Edge cases

- **Very long Arabic strings** count Unicode code points, not bytes.
- **A manifest in a newer `schema`** than the server knows: refused with
  `manifest_schema_newer` and the words "This plugin needs a newer Maktaba".
- **Case:** ids are lower case only; `Dev.Example` is refused, not folded, so two ids can never
  differ only by case.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/plugins/manifest.schema.json` (new) | The schema. |
| `shared/plugins/capabilities.toml`, `shared/plugins/events.toml` (new) | The two lists. |
| `shared/plugins/testdata/manifests/` (new) | Corpus. |
| `shared/go/pluginmanifest/` (new module) | Parse, validate, normalise. Unpacking is `componentpack.UnpackPlugin` (54.38), on the rules of Epic 51's `componentpack.Unpack`. |
| `plugind/crates/manifest/` (new) | Rust validator (`jsonschema` crate + rules). |
| `pipeline/src/maktaba_pipeline/plugins/manifest.py` (new) | Python validator. |
| `tools/plugin-sdk/js/manifest.ts` (new) | TypeScript validator. |
| `pipeline/src/maktaba_pipeline/engine/graph.py` | `validate()`: core stage names never start with `p_`. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | `plugins.error.*` codes, together. |
| `tools/ci/tests/test_plugin_corpus.py` (new) | Runs the corpus through all four validators and compares output. |

### Tests

- Unit: T01–T07 in each language's unit job.
- **CI:** `lint/repo` gains `test_plugin_corpus.py`; the i18n completeness test covers the new
  keys.

### Rollout

Library code only; nothing reads a manifest until 54.07. Ships dark.

### Rollback

Remove the modules; no data.

### Dependencies

None. The safe unpacker already exists (`shared/go/componentpack/unpack.go`, Epic 51 51.15);
54.38 adds the plugin head to it.
