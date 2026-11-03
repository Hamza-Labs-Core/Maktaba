# The manifest and the package

> **Status: planned (Phase 1).** Nothing reads a plugin manifest yet. This page describes the
> design in
> [story 54.01](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/story-54-01-manifest-package.md),
> which can change.

## The contract

Your plugin promises Maktaba a few facts, and Maktaba holds you to them:

- **Who it is:** a reverse-DNS id under a prefix you own, and a semver version.
- **What it runs on:** a Maktaba server range and the plugin API version it was built against.
- **What it asks for:** capabilities, each `required` or `optional`.
- **What it adds:** stages, the events and schedules that feed them, settings and panels.
- **How much it may use:** memory, time, scratch space, data and concurrency.

The same schema validates the manifest at home, in the store and in the SDK. Unknown keys are
an error, not a warning, so nothing you write is ever silently ignored.

## The package

A `.mkplugin` file is a `tar` archive compressed with zstd:

| Path | Required | What |
|---|---|---|
| `manifest.toml` | yes | the manifest |
| `plugin.wasm` | yes | one WebAssembly component (not a core module) |
| `files.json` | yes | `[{path, sha256, size}]` for every other file, sorted by path |
| `i18n/en.json`, `i18n/ar.json` | yes | every string key the manifest and UI files use |
| `icon.png` | yes | 256 x 256 PNG, at most 200 KB |
| `LICENSE` | yes | text matching `licence` |
| `ui/*.json` | no | settings schema, panels |
| `fixtures/` | no | test inputs for the store's sandbox run; never installed |
| `signatures/developer.sig`, `signatures/store.sig` | no | see [Publishing](publishing.md) |

Limits: 50 MB compressed, 200 MB unpacked, 500 files, paths up to 200 bytes. Only regular
files and folders: no links, devices, absolute paths or `..`. File names are UTF-8 in NFC.

## The manifest

`manifest.toml` sits at the package root:

```toml
schema       = 1
id           = "dev.example.silence-chapters"
version      = "1.4.0"
plugin_api   = "1.2"
maktaba      = ">=0.6.0, <0.9.0"
platforms    = ["any"]
languages    = ["ar", "en"]
licence      = "MIT"
price        = "free"
data_version = 2

[strings]
name    = "plugin.name"
summary = "plugin.summary"

[capabilities]
required = ["library.read", "media.audio", "chapters.write"]
optional = ["notify.admin"]

[resources]
memory_mb   = 256
timeout_s   = 300
fuel        = "default"
scratch_mb  = 0
data_mb     = 50
concurrency = 2

[[stage]]
name        = "find_silences"
subject     = "video"
export      = "run-find-silences"
version     = 3
may_request = ["write_chapters"]
  [[stage.trigger]]
  event = "audio.ready"

[[schedule]]
stage = "weekly_report"
every_minutes_setting = "report_interval"

[settings]
schema = "ui/settings.schema.json"
```

## Field rules

| Field | Rule |
|---|---|
| `schema` | `1` |
| `id` | reverse-DNS, 3 to 6 labels of `[a-z0-9-]`, at most 100 characters, lower case only |
| `version` | semver 2.0, no build metadata |
| `plugin_api` | `MAJOR.MINOR` of `maktaba:plugin` you built against |
| `maktaba` | a server range with a lower bound; an upper bound is required to list in the store |
| `platforms` | `["any"]`, or a subset of `darwin-arm64`, `windows-amd64`, `linux-amd64`, `linux-arm64` |
| `languages` | BCP 47 codes for content it is good at (a store filter); may be empty |
| `licence` | an SPDX id or `proprietary` |
| `price` | `free`, or a store product id (Phase 3) |
| `data_version` | integer of 1 or more; bump it when your stored data changes shape |
| `strings.name`, `strings.summary` | keys present in both `i18n` files; name up to 40 and summary up to 140 characters in each language |
| `capabilities` | names from Maktaba's capability list, no duplicates across the two lists |
| `resources.*` | within the limits in [The sandbox](sandbox.md); missing means the default |
| `stage[]` | 1 to 20 stages; `name` matches `[a-z][a-z0-9_]{0,39}`; `subject` is `video`, `series`, `season`, `library` or `catalog`; `export` names a function the component exports; `version` of 1 or more; `may_request` names your own stages only |
| `stage.trigger[]` | an `event` whose subject matches the stage's |
| `schedule[]` | a `catalog` stage; `every_minutes_setting` names an integer settings field with `minimum` of 15 or more |
| `settings.schema` | a file in `ui/` (see [Queue stages and the plugin API](queue-and-api.md#settings)) |
| `panel[]` | Phase 3; accepted and ignored before then |

### Reserved ids

These prefixes are reserved and refused: `tv.maktaba.` (except `tv.maktaba.community.`),
`dev.maktaba.`, and any label that is `maktaba` or `official`. Ids are never case-folded, so
`Dev.Example` is refused rather than lowered.

### Stage names in the queue

Your stage `find_silences` appears in the queue as `p_<8 hex of sha256(id)>_find_silences`.
The name is stable across your versions, and two plugins can never collide with each other or
with Maktaba's own stages.

### Stage versions

Bump a stage's `version` when you want it to run again on everything it already did. A new
plugin version alone does not rerun work unless a stage version or its inputs change.

## Errors

Validation errors are codes with English and Arabic words, for example
`manifest_unknown_key`, `manifest_bad_id`, `manifest_reserved_id`,
`manifest_missing_string`, `manifest_bad_capability`, `manifest_limit`,
`manifest_subject_mismatch`, `manifest_schema_newer` and `package_bad_path`.

## Strings

Every key the manifest and UI files name must exist in both `i18n/en.json` and
`i18n/ar.json`. Lengths count Unicode code points, not bytes. Strings shown to users must not
contain URLs.
