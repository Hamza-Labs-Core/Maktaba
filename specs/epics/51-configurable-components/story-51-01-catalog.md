# Story 51.01 — The component catalog

> Epic 51 · Phase 1 · Depends on: — · Needed by: 51.02, 51.15 (and through them every other story)

## Why

Today nothing lists the parts of Maktaba. Stages live in `engine/graph.py` (49 `StageDef`s,
plus 7 more from the Plex and dizqueTV extensions), supervisor processes are built by hand in
`cmd/maktaba-server/cmd_serve.go`, and about 40 loops are started with `safego.Go` in
`api/main.go`, `api/cluster_bootstrap.go`, `api/internal/router/p*.go`, with `bg.Go` in
`streaming/*_wire.go`, and with `asyncio.create_task` in `pipeline/.../runtime.py`. A new
stage or loop can land without anyone deciding which feature it belongs to, so nothing can
turn it off.

This story writes the one list (README §4.2, §5) and a CI job that fails when code and list
drift apart. Nothing reads the catalog at runtime yet; behaviour does not change.

## Description

### 1. The file

`shared/components/catalog.toml`, one `[[component]]` per component, with the fields of
README §4.2. Fields this story fixes exactly:

| Field | Type | Rule |
|---|---|---|
| `id` | string | `^[a-z][a-z0-9_]*(\.[a-z0-9_*-]+)*$`, unique. `core.*` ids are not switchable. |
| `kind` | string | one of `process`, `stages`, `loop`, `extension`, `asset`, or a `+` join of the first three (`stages+process`, `stages+loop`, `process+loop`). |
| `switchable` | bool | default `true`; must be `false` for `core.*` and `true` for the rest. |
| `group` | string | `core`, `media`, `library`, `processing`, `livetv`, `integrations`, `cluster`, `system`, `runtimes`, `models`, `tools`, `languages`. |
| `title_key`, `desc_key` | string | `components.<id>.title` / `.desc`; the words live in `web/src/i18n/en.json` and `ar.json`, never in the catalog. |
| `stages` | list | `StageDef.name`s. |
| `processes` | list | `"<role>/<instance>"`, where `*` matches any instance: `pipeline/stt-*`, `pipeline/index`, `ollama/`, `relay/`, `tv-slot/*`. The main pipeline worker is `pipeline/`. |
| `loops` | list | `"<host>:<name>"`, host one of `supervisor`, `api`, `streaming`, `pipeline`. The name is the `safego.Go` name where one exists (`api:tvchange_controller`), the asyncio task name in the pipeline (`pipeline:channel-topup`). |
| `requires`, `wants`, `conflicts` | list | component ids; `any:a\|b\|c` allowed in `requires`. |
| `settings` | list | `rules.<section>` names and `toml:<section>.<key>` keys it owns. |
| `platforms` | list | `darwin-arm64`, `windows-amd64`, `linux-amd64`, `linux-arm64`. |
| `bundles` | list | `full`, `core`, `worker`. |
| `drain` | table | `grace_s` (default 30; transcription 120), `checkpoint` (bool). |
| `stop_grace_s` | int | loops only; default 5. |
| `licence` | string | an existing flag from `api/internal/flags/declarations.go`, or empty. |
| `features` | list | user-facing feature words it serves (§4.12): `live_tv`, `transcripts`, `subtitles`, `semantic_search`, `chapters`, `youtube`, `plex`, `remote_access`, `recommendations`. |
| `since` | string | first release with this id. |

Two top-level lists sit beside the components:

```toml
schema = 1
phases  = ["not_installed", "installing", "removing", "off", "stopped", "waiting",
           "starting", "running", "degraded", "draining", "failed"]
reasons = ["dependency_off", "licence_missing", "platform_unsupported", "not_enough_memory",
           "verify_failed", "crash_loop", "held", "stop_timeout", "settings_invalid",
           "env_override", "boot_flag", "reconcile_off", "not_in_payload", "component_off",
           "component_held", "required_by"]
```

Words: `components.phase.<phase>` and `components.reason.<code>` in both language files.
Adding a reason in a later story means adding it here and its two words in the same PR.

### 2. Where every stage, role and loop goes today

Core (not switchable): `core.postgres` (`postgres/`), `core.api` (`api/`, its core loops),
`core.streaming` (`streaming/`, watchdog, cache GC, session reaper, VOD purge, hwaccel and
share-map loops), `core.pipeline` (`pipeline/`, `pipeline/chroma-copy`, stages `scan`,
`probe`, `content_hash`, `thumb_hash`, `link`, `fingerprint`, `style_sample`, `fp_gate`,
`access_check`, `storage_canonicalize`, `categorize`, `component_catchup` (51.06); loops
`worker-hb`, `stage-selection`, `reconciler`, `pruner`, `audio-cache-sweep`, `claim-lease`,
`index-close`), `core.node` (`copy/`, node-mode loops, offline TV, full-node VOD),
`core.updates` (update machinery, `api:updates_health_sampler`, pre-update dumps),
`core.switchover` (`api:switchover_resume`, `_keep_standbys`, `_finish`).

Switchable, as README §5 with these exact stage lists (checked against `graph.py` on
2026-11-03):

| Component | Stages | Processes | Loops |
|---|---|---|---|
| `media.thumbnails` | `thumbnail`, `thumb_small`, `chapter_thumbs` | — | — |
| `library.convert` | `convert_mp4` | — | — |
| `library.dedup` | `dedup_scan`, `dedup_apply`, `trash_purge`, `file_restore` | — | — |
| `library.repair` | `repair_broken` | — | — |
| `transcription` | `extract`, `transcribe` | `pipeline/stt-*` | — |
| `subtitles` | `subtitle_gen` | — | — |
| `search.semantic` | `index`, `chapter_infer`, `topic_assign`, `topic_recluster` | `pipeline/index` | — |
| `classify` | `detect`, `tag`, `check_tags`, `check_clips`, `display_title`, `books`, `topic_collections`, `promo_links`, `clip_groups`, `rate_style`, `review_pages` | — | — |
| `enrich` | `enrich` | — | `api:enrichment_auto_accept` |
| `llm` | — | `ollama/` | — |
| `youtube` | `youtube_sync`, `youtube_fetch`, `youtube_backfill`, `exact_dates` | — | — |
| `livetv` | `plan_channels`, `write_channels` | — | `api:tvchange_controller`, `api:tvremote_controller`, `api:tvremote_publisher`, `api:tvremote_health_sampler`, `pipeline:channel-topup` |
| `livetv.slots` | `tv_stage`, `tv_swap`, `tv_gc`, `tv_rollback` | `tv-slot/*` | `streaming:slots_frontdoor`, `streaming:slots_ops` |
| `livetv.inprocess` | — | — | `streaming:channel_engine`, `streaming:channel_fences`, `streaming:channel_sweeper`, `streaming:push_relay`, `streaming:viewings` |
| `livetv.hdhomerun` | — | — | `streaming:hdhr_ssdp` |
| `ext.plex` | `plex_index`, `plex_collection`, `plex_groups`, `plex_livetv` | — | — |
| `ext.dizquetv` | `dizquetv_show`, `dizquetv_channels`, `dizquetv_audit` | — | — |
| `ext.cloudflare_stream` | — | — | — |
| `cloud.link` | — | `relay/` | — |
| `cloud.relay` | — | — | `api:cloudlink_reporter` (and relayed traffic; gated by `cloud_relay`, 51.19) |
| `cluster` | — | — | `api:cluster_housekeeping`, `api:cluster_hub`, `api:home_link`, `api:cluster_push_database`, `api:cluster_ensure_access`, `api:pairing_sweep`, `pipeline:health`, `pipeline:cluster-agent` |
| `discovery.mdns` | — | — | `supervisor:mdns`, `api:mdns` |
| `queue_health` | — | — | `pipeline:queue-health` |
| `analytics` | — | — | `api:watch_reaper`, `api:usage_counter` |
| `telemetry` | — | — | `api:telemetry_sender` |
| `backups` | — | — | `supervisor:backups` |
| `updates.auto` | — | — | `api:update_checker`, `api:updates_discoverer`, `api:updates_scheduler`, `api:updates_driver` |

Streaming loops have no names today; 51.05 gives them the names above. `media.chapters`
(README §5) has no stage of its own today, so it is not in the catalog until one exists (see
the report on README corrections). The assets (`runtime.*`, `model.*`, `tool.*`) are listed
with `in_payload` and `source` as README §4.2 shows; their `files` sha256 values are filled by
51.15.

### 3. Loading it

- **Go.** A new module `github.com/Hamza-Labs-Core/Maktaba/shared/components` with its
  `go.mod` in `shared/components/`, beside the file, so `//go:embed catalog.toml` works (Go
  cannot embed a file above the module). `components.Default() *Catalog` parses once;
  `Get(id)`, `ByStage(name)`, `ByProcess(role, instance)`, `ByLoop(host, name)`,
  `Requires(id)` (resolved, `any:` kept as a group), `Order()` (requirements first; a cycle
  is a load error). `api/`, `streaming/` and `cmd/maktaba-server/` add the module with a
  `replace` line, as they do for `shared/go/ports`.
- **Python.** `maktaba_pipeline.components.catalog` reads
  `maktaba_pipeline/components/catalog.toml` with `importlib.resources` and `tomllib`, with
  the same accessors. That file is a byte-for-byte copy of the shared one, written by
  `make catalog-sync`; the `catalog` job fails when the two differ (the
  `api/internal/handlers/rules/schema.json` pattern).
- Both loaders validate the same rules (§1) and raise on an unknown field, so a typo fails
  tests in both languages.

### 4. The `catalog` CI job

A job in `.github/workflows/_lint.yml`, gated by a new `catalog` filter in
`.github/ci-paths.yml` (the catalog, `engine/graph.py`, `extensions/**`,
`cmd/maktaba-server/internal/supervisor/**`, `cmd_serve.go`, the loop start files, both
language files). It runs:

1. `make catalog-sync` then `git diff --exit-code` (the copy is current).
2. `uv run --extra dev pytest pipeline/tests/components/test_catalog.py`: every `StageDef`
   in `GRAPH` and in every built-in extension's manifest belongs to **exactly one**
   component; no catalog stage is missing from the graph.
3. `go test ./shared/components/...` and
   `go test ./cmd/maktaba-server/internal/supervisor -run TestCatalogRoles`: every
   `Process` the `Build*` functions can return (each `Role` constant, each instance name:
   `stt-N`, `index`, `chroma-copy`, `blue`/`green`/`amber`) matches exactly one catalog
   `processes` entry.
4. `node tools/ci/catalog_words.mjs`: every `title_key`, `desc_key`, phase and reason has a
   non-empty string in **both** `en.json` and `ar.json`, and the Arabic one is not a copy of
   the English one.

The loop half of F1 is added to this same job by 51.05 (Go hosts) and 51.06 (pipeline): each
runner's declared loop table must equal the catalog's loops for that host.

## Acceptance criteria

- **Given** the catalog on `main`,
  **when** the `catalog` job runs,
  **then** every stage, supervisor process and listed loop is in exactly one component and
  the job passes.
- **Given** a PR that adds a `StageDef` to `graph.py` and not to the catalog,
  **when** CI runs,
  **then** the `catalog` job fails naming the stage.
- **Given** a stage listed under two components,
  **when** CI runs,
  **then** the job fails naming both.
- **Given** a component whose `title_key` has English words but no Arabic words,
  **when** CI runs,
  **then** the job fails naming the key and the missing language.
- **Given** a component that requires itself through another component,
  **when** either loader parses the file,
  **then** it fails with `requires cycle: a -> b -> a`.
- **Given** the Go and Python loaders,
  **when** both read the catalog,
  **then** they return the same ids, stages, processes, loops and order (a shared fixture
  test compares their JSON dumps).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | real catalog | `Default()` | loads; ~30 switchable ids; `Order()` puts `runtime.ml` before `transcription` |
| T02 | unit (Go) | fixture with a cycle | load | error names the cycle |
| T03 | unit (Go) | fixture with unknown field `stagez` | load | error names the field |
| T04 | unit (Py) | real catalog + `GRAPH` + built-in extensions | map each stage | each in one component; none unmapped |
| T05 | unit (Py) | catalog copy edited | compare with shared file | test fails ("run make catalog-sync") |
| T06 | unit (Go) | each `Build*` in supervisor | map role/instance | exactly one component each |
| T07 | unit | en.json without `components.llm.title` | `catalog_words.mjs` | exit 1, names the key and `en` |
| T08 | unit | ar.json value equal to the English | `catalog_words.mjs` | exit 1 |
| T09 | unit | Go and Py dumps | compare JSON | equal |
| T10 | unit (Go) | `core.api` with `switchable = true` | load | error |
| T11 | unit (Go) | `conflicts` on one side only | load | error: conflicts must be listed on both sides |

## Edge cases

- **Entry-point extensions** (third-party, H5) are not in the catalog. Their stages are
  grouped at runtime under `ext.<name>` with `switchable = true`, no words of their own
  (the UI shows the manifest's label). The lint only checks built-in extensions. Third-party
  plugins proper, with a store and no internet access, are Epic 54, built on this catalog.
- **Unreleased stages** (`StageDef.unreleased`) still belong to a component, so the day they
  ship they are switchable.
- **Hidden or legacy stages** (`scan`, `extract`) are mapped like the others.
- **Instance counts vary** (`stt-1..N`): the `*` pattern covers any N.
- **Words for Arabic plurals** are not needed: titles are names, not counts.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/components/catalog.toml` (new) | The catalog: §1 fields, §2 assignments, the asset entries of README §5. |
| `shared/components/go.mod` (new) | Module `github.com/Hamza-Labs-Core/Maktaba/shared/components` (BurntSushi/toml v1.6.0, as the server). |
| `shared/components/catalog.go` (new) | `//go:embed`, types, `Default()`, accessors, validation, `Order()`. |
| `shared/components/catalog_test.go` (new) | T01–T03, T09 (Go side), T10, T11. |
| `shared/components/testdata/` (new) | Bad-catalog fixtures. |
| `pipeline/src/maktaba_pipeline/components/__init__.py` (new) | Package. |
| `pipeline/src/maktaba_pipeline/components/catalog.py` (new) | Loader and validation. |
| `pipeline/src/maktaba_pipeline/components/catalog.toml` (new) | Synced copy. |
| `pipeline/tests/components/test_catalog.py` (new) | T04, T05, T09 (Python side). |
| `cmd/maktaba-server/internal/supervisor/catalog_roles_test.go` (new) | T06. |
| `cmd/maktaba-server/go.mod`, `api/go.mod`, `streaming/go.mod` | Require + `replace` the new module. |
| `tools/ci/catalog_words.mjs` (new) | T07, T08. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | `components.<id>.title` / `.desc`, `components.phase.*`, `components.reason.*`, English and Arabic together. |
| `Makefile` | `catalog-sync` target. |
| `.github/workflows/_lint.yml` | The `catalog` job (§4). |
| `.github/ci-paths.yml` | The `catalog` filter. |
| `.github/workflows/ci.yml` | Gate the job on the filter. |

### Tests

- Unit: T01–T11 (`go test ./shared/components/...`, the pipeline's `uv run --extra dev
  pytest`, `node tools/ci/catalog_words.mjs`).
- CI: the new **`catalog`** job (README §11) on every PR touching its filter.
- `tools/ci/tests/test_ci_paths.py` gains a case: a change to `engine/graph.py` runs `catalog`.

### Rollout

Nothing reads the catalog at runtime in this story. It ships in the next release with no
visible change.

### Rollback

Revert the PR. No data, no schema.

### Dependencies

None. 51.02 and 51.15 build on it.
