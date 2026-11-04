# Story 54.27 — Repo layout, packaging, components and ownership

> Epic 54 · Phase 4 · Depends on: 54.07, 54.22, Epic 51 (51.01, 51.17) · Needed by: 54.28

## Why

Fifteen first-party plugins need a home in the repo, a build that signs them with the release,
a place in Epic 51's catalog and bundles, and owners and CI jobs that run only when they change
(README §15.6, §15.7, §15.11).

## Description

### 1. Layout

```
plugins/
  sdk/python/                 maktaba-plugin-sdk (54.12), with native/ (54.23) and parity (54.26)
  first-party/
    _lib/                     maktaba-fp-lib: text, hashing, share paths, rules parsing used by several plugins
    core-media/  thumbnails/  convert/  dedup/  repair/  transcription/  subtitles/
    semantic-search/  classify/  enrich/  youtube/  livetv/  livetv-slots/  plex/  dizquetv/
      manifest.toml           README §15.5
      i18n/en.json, ar.json
      queries/*.sql           54.24
      src/maktaba_fp_<name>/  handlers, fingerprints, domain code
      tests/
      parity.toml             54.26
      pyproject.toml          its own extras
```

- A `uv` workspace at the repo root lists `pipeline`, `plugins/sdk/python`,
  `plugins/first-party/_lib` and every plugin. Each plugin's `pyproject.toml` declares its
  extras (transcription: `mlx` on macOS arm64, `faster-whisper` and `torch` elsewhere;
  semantic-search: the embedder; youtube and repair: none, `yt-dlp` is an Epic 51 asset), and
  `constraints.txt` / `constraints-core.txt` (Epic 51) pin them.
- A plugin's code imports only the SDK, `_lib` and its own package. An import-linter contract
  (`tools/ci/import_contracts.toml`) fails a plugin that imports another plugin or
  `maktaba_pipeline` (except the engine types re-exported by the SDK), and fails
  `maktaba_pipeline` importing any plugin once that plugin's wave is done.
- Each wave moves its code with `git mv` so history follows.

### 2. Build and release

- `tools/plugins/build.py <plugin>` produces `tv.maktaba.<name>-<version>.mkplugin`
  (tar.zst): manifest, strings, compiled `statements.json` (54.24), the plugin's wheel and
  `_lib`'s wheel, `files.json`.
- `release.yml`'s package step builds all fifteen, adds each `files.json` digest to
  `checksums.txt` before it is signed, and the payload carries them under
  `<payload>/plugins/first-party/`. They are a few MB in all; the ML wheels stay in the
  `runtime.ml` pack (Epic 51 §4.9).
- The server build embeds every `manifest.toml` (Go `embed`, Python `importlib.resources`) for
  the graph (54.25) and the catalog (§3).
- Versions: `version` and `maktaba` in each manifest are stamped from the release version by
  the build; a manifest in the repo carries `version = "0.0.0-dev"`.

### 3. Components and bundles

- `shared/components/catalog.toml`: each component that owns stages keeps its id, **its
  kind** and its `processes` and `loops`, and gains one field, `plugin = "tv.maktaba.<name>"`
  (both loaders, `shared/components/catalog.go` and
  `pipeline/src/maktaba_pipeline/components/catalog.py`, learn it, since they refuse an
  unknown field). Kinds stay because a kind says how the machine runs the component (51
  §4.1), and a stage's implementation (`job_stages.impl`, 54.25) does not change that: the
  pipeline runner (51.06) gates the stages the same way either way. The `plugin` kind of 54.37
  is for third-party entries only.

| Catalog id | Kind (unchanged) | `plugin` |
|---|---|---|
| `core.pipeline` (not switchable) | `stages+process+loop` | `tv.maktaba.core-media` (every stage but `component_catchup`, which stays an engine stage) |
| `media.thumbnails` | `stages` | `tv.maktaba.thumbnails` |
| `library.convert` | `stages` | `tv.maktaba.convert` |
| `library.dedup` | `stages` | `tv.maktaba.dedup` |
| `library.repair` | `stages` | `tv.maktaba.repair` |
| `transcription` | `stages+process` | `tv.maktaba.transcription` |
| `subtitles` | `stages` | `tv.maktaba.subtitles` |
| `search.semantic` | `stages+process` | `tv.maktaba.semantic-search` |
| `classify` | `stages` | `tv.maktaba.classify` |
| `enrich` | `stages+loop` | `tv.maktaba.enrich` (the loop `api:enrichment_auto_accept` stays in the API) |
| `youtube` | `stages` | `tv.maktaba.youtube` |
| `livetv` | `stages+loop` | `tv.maktaba.livetv` (its loops stay) |
| `livetv.slots` | `stages+process+loop` | `tv.maktaba.livetv-slots` (the `tv-slot/*` processes and its loops stay) |
| `ext.plex` | `extension` | `tv.maktaba.plex` |
| `ext.dizquetv` | `extension` | `tv.maktaba.dizquetv` |

- The `stages` list stays in `catalog.toml`. The `catalog` job gains a check that it equals
  the plugin manifest's `[[stage]]` names, so 51.01's "every stage belongs to exactly one
  component" keeps reading one file.
- Bundles are the catalog's `bundles` field (`full`, `core`, `worker`; `custom` at setup,
  `shared/components/bundles.go`), unchanged: this story moves code, not switches.
  `GET /api/components/bundles` and `POST /api/components/bundle` answer as before. 51.17's
  `bundle-size` budget counts the packages.
- The desired-state rows (Epic 51 `components` and `component_overrides`) are keyed by
  component id, so nothing moves.

### 4. Ownership and CI

- `CODEOWNERS`: one line per `plugins/first-party/<name>/`, plus `_lib`, the SDK and the
  runner.
- `.github/workflows/first-party.yml`: one job `fp-<name>` per plugin, with `paths:` filters on
  its folder, `_lib`, the SDK and the runner; it runs the plugin's unit tests, its contract
  tests (SDK harness with a fake API serving its statements against a test Postgres under its
  role) and `plugins-parity --stages <its migrated stages>`. Related-only, as the standing rule
  asks; `ci-success` treats skipped jobs as passing.

### 5. Docs

`docs/plugins/first-party.md` (maktaba.dev): how a first-party plugin is laid out, how to add a
statement, how to add a stage, how parity works. English, with the Arabic UI strings in the
plugin's `i18n`.

## Acceptance criteria

- **Given** a release build,
  **when** `checksums.txt` is verified,
  **then** it lists the `files.json` digest of every first-party package, and 54.22's check
  accepts each.
- **Given** a plugin that imports another plugin's package,
  **when** CI runs,
  **then** the import contract fails naming both.
- **Given** a PR that touches only `plugins/first-party/thumbnails/`,
  **when** CI runs,
  **then** `fp-thumbnails` runs and the other `fp-*` jobs are skipped.
- **Given** a home with Epic 51's desired state from before this story,
  **when** it updates,
  **then** every component keeps its desired state, and the Components page shows the same
  switches.
- **Given** a Core install,
  **when** the venv is built,
  **then** no ML wheel is resolved.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI (release dry run) | build all packages | verify checksums and trust | 15 packages, all trusted |
| T02 | CI | import-linter contracts | planted cross-plugin import | fails |
| T03 | CI | path filters | PR touching one plugin | only its job runs |
| T04 | unit (py, go) | catalog with `plugin` fields | load, `make catalog-check` | same ids and kinds; each `stages` list equals its manifest's; each stage owned once |
| T05 | CI (`bundle-size`) | Core payload | footprint | within budget |
| T06 | integration (PG) | desired state from the previous release | update | unchanged |

## Edge cases

- **A wave moves code that another, not yet moved stage still imports**: the shared part goes
  to `_lib` in the same PR; the import contract keeps the direction one-way.
- **A plugin with no stages left after a refactor**: the build refuses a first-party plugin
  with no stage (it would be an empty component).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/` (new tree), root `pyproject.toml` (`uv` workspace) | §1. |
| `tools/plugins/build.py` (new), `.github/workflows/release.yml` | §2. |
| `shared/components/catalog.toml`, Epic 51 catalog loaders | §3. |
| `CODEOWNERS`, `.github/workflows/first-party.yml` (new), `tools/ci/import_contracts.toml` | §4. |
| `docs/plugins/first-party.md` (new) | §5. |

### Tests

- CI T01–T03, T05; unit T04; integration T06.

### Rollout

Lands with empty plugin folders except the manifests of wave 1 (stages still `inproc`).

### Rollback

Release rollback. Kinds and ids do not change, so an older release's embedded catalog agrees with the desired-state rows.

### Dependencies

54.07 (plugin components), 54.22 (trust), Epic 51 51.01 (catalog), 51.17 (bundles).
