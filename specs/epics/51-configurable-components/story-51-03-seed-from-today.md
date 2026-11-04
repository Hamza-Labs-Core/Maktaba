# Story 51.03 — Existing installs keep running what they run

> Epic 51 · Phase 1 · Depends on: 51.02 · Needed by: 51.08

## Why

Goal 8 and F15: an update to the first release with this epic must leave every home running
exactly what it ran before. Today that answer is spread over `server.toml`, the environment,
`job_stages`, `extensions`, `hdhr_device` and `app_settings`. If the runners (51.04–51.06)
started from empty desired state, they would switch half of Maktaba off on the owner's Mac at
3 a.m. This story writes desired state **once**, from what runs today, as bundle `full` (H8),
and proves in the update rehearsal that nothing changed.

## Description

### 1. When and by whom

- The **home's supervisor** seeds, at boot, after the database is up and migrated and before
  any role starts. It is the only process that sees `server.toml`, the payload and the
  database at once. This is the one write of desired state not made by the API (README §4.4).
- It runs inside one transaction holding `pg_advisory_xact_lock(hashtext('components_seed'))`
  and only when `component_meta.seeded_at IS NULL`. It then sets `seeded_at`,
  `seeded_release` and `bundle = 'full'`. A second boot, a second machine or a crash halfway
  do nothing more (the transaction rolled back, or the marker is set).
- A **fresh install** in Phase 1 seeds the same way (today's defaults give today's
  behaviour). Setup's bundle choice arrives in 51.17.
- A **node** seeds nothing at home level. At its first boot on the new release it writes
  overrides for itself only where its own `server.toml` differs from the home (§3), through
  `component_override_seed(p_node uuid, p_rows jsonb)`, a guarded function that inserts only
  when no override row exists yet.

### 2. The rules, component by component

`want` is `enabled` unless the table says otherwise. No component is seeded `absent` except
an asset that is not on disk and that nothing enabled needs. All `updated_by = 'seed'`.

| Component | Seeded `enabled` when | Else |
|---|---|---|
| `core.*` | always | — |
| any stage group (`media.thumbnails`, `library.*`, `transcription`, `subtitles`, `search.semantic`, `classify`, `enrich`, `youtube`, `livetv`) | at least one of its stages is not disabled by an operator (`job_stages.disabled_reason` other than `operator`) | `installed` (every stage was turned off on the Queues page) |
| `livetv.slots` | `[tv] slots` is on (`TVSection.SlotsOn()`) | `installed` |
| `livetv.inprocess` | `[tv] slots` is off | `installed` |
| `livetv.hdhomerun` | `hdhr_device.enabled` is true | `installed` |
| `llm` | the supervisor would build the ollama process today (`serveLLM` returns one), or `[llm] url` is set | `installed` |
| `ext.plex`, `ext.dizquetv`, `ext.cloudflare_stream` | its `extensions.enabled` row is true | `installed` (no row: `installed`) |
| `cloud.link` | `RelayWanted(cfg)` is true | `installed` |
| `cloud.relay` | `RelayWanted(cfg)` is true (the licence check stays where it is today; 51.19 adds the `Licensed` condition) | `installed` |
| `discovery.mdns` | `[server] mdns` is on | `installed` |
| `backups` | `[backups] nightly` is not `off` | `installed` |
| `telemetry` | `[telemetry] enabled` is on (the admin's consent `telemetry.share_server_data` keeps deciding what is sent, unchanged) | `installed` |
| `cluster`, `queue_health`, `analytics`, `updates.auto` | always (their loops always run today) | — |
| assets (`runtime.ml`, `runtime.ollama`, `tool.yt-dlp`, `model.*`) | on disk now (`installed`), never `enabled` (assets have no run); also `installed` when an enabled component needs it and none of that requirement is on disk yet (today the stage fetches it on first use: a fresh home, one that never transcribed), the first of an `any:` group that runs on the platform | `absent` |

Stage groups and the Queues page: a component seeded `enabled` leaves every `job_stages` row
as it is, including stages an operator turned off. 51.06 never overwrites
`disabled_reason = 'operator'`. So the `job_stages.enabled` set is identical after the update.

### 3. `server.toml`: imported once, then the database wins

The keys that become desired state: `[tv] slots`, `[llm] enabled` (and whether `url` is set),
`[cloud] enabled`, `[server] mdns`, `[backups] nightly`, `[telemetry] enabled`. The value seen
at the seed is stored in `component_meta.toml_imported` (`{"tv.slots":"on", …}`).

After the seed, at every boot the supervisor compares each key in the file with the
component's desired state. When they disagree it logs `toml_key_superseded` **once per boot**
with the key, the file's value, the line number and the component, and adds condition
`reason = toml_superseded` on the component's supervisor status (51.10 shows "server.toml
says otherwise; the database wins", in English and Arabic). The file is never rewritten.

`[transcription] workers` and `[pipeline] index_worker` are **not** imported: they size and
place work for an enabled component (how many `stt-N`, whether the index has its own worker),
they do not turn a feature on or off. `workers = -1` today still transcribes in the main
worker. They stay in `server.toml` as settings of their component.

### 4. Environment variables are not imported

`MAKTABA_UPDATE_DISABLE`, `MAKTABA_CLUSTER_AGENT=0`, `MAKTABA_PIPELINE_NO_ENGINE`,
`MAKTABA_PIPELINE_SEMANTIC` and the like are per-process, per-boot overrides that ops and
tests set. Seeding them would freeze a test's setting into the database. They keep winning at
runtime: the runner reports the affected component with condition `reason = env_override` and
the variable's name. `MAKTABA_TELEMETRY_ENABLED` is derived from `[telemetry]`, which is
imported (§2).

### 5. The words

New reason `toml_superseded` (catalog `reasons`), words `components.reason.toml_superseded`
in `en.json` ("server.toml says {value}; the setting here wins") and `ar.json` (Arabic text in
the same PR). `env_override` words already come with 51.01.

### 6. Update-rehearsal scenario 12: no behaviour change

A new scenario in the update rehearsal, profile `small`, the seeded home of
`api/rehearsal_seed_e2e_test.go` with `[tv] slots = on`, the relay off, one extension enabled,
one stage disabled by an operator and `hdhr_device.enabled = false`:

1. **Before** the update, from the previous release: the supervisor's `/processes` (role,
   instance, wanted, running), `job_stages` (stage, enabled, disabled_reason, paused),
   `extensions` (name, enabled), `hdhr_device.enabled`.
2. Update to this commit, the app's way (as scenario 01).
3. **After**, the same four lists, plus `component_status` for the home.
4. Assert: the four lists are equal; `component_meta.seeded_at` is set and `bundle = full`;
   every `components` row has the `want` §2 says; every loop in
   `tools/rehearsal/loops-full-home.txt` (the loops the previous release starts on this home)
   is `running` in `component_status`, and no other switchable loop is.
5. Restart the server once more: `seeded_at` unchanged, no row's `generation` moved.

The pipeline is the rehearsal's stand-in, so pipeline loops and stage claiming are not in
this scenario (51.06's `components-live` covers them). Downtime budget: 2 min, as scenario 01.
Scenarios 13–14 (packs) belong to 51.18.

## Acceptance criteria

- **Given** a home on the previous release with `[tv] slots = on` and Plex enabled,
  **when** it updates to this release,
  **then** `livetv.slots` and `ext.plex` are `enabled`, `livetv.inprocess` is `installed`, and
  the same processes run as before.
- **Given** every `youtube` stage turned off on the Queues page,
  **when** the seed runs,
  **then** `youtube` is `installed` and the four stages keep `disabled_reason = 'operator'`.
- **Given** the seed has run,
  **when** the owner edits `server.toml` to `[tv] slots = off` and restarts,
  **then** slots keep running (the database wins), the log has one `toml_key_superseded`
  with the line number, and the component shows `toml_superseded`.
- **Given** two boots racing (a crash during the first seed),
  **when** both reach the seed,
  **then** exactly one seed commits.
- **Given** `MAKTABA_UPDATE_DISABLE=1` in the supervisor's environment at the seed,
  **when** the seed runs,
  **then** `updates.auto` is still `enabled` and its status says `env_override`.
- **Given** update-rehearsal scenario 12,
  **when** it runs on a PR,
  **then** processes, `job_stages`, extensions and `hdhr_device` are identical before and after.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | table of configs (each §2 row, both branches) | `SeedPlan(cfg, db snapshot, payload)` | the `want` of §2 |
| T02 | unit (Go) | `[transcription] workers = -1` | plan | `transcription` enabled |
| T03 | unit (Go) | `[llm] enabled = auto`, no ollama in payload, no url | plan | `llm` installed; `runtime.ollama` absent |
| T04 | integration (PG) | scratch DB, empty `components` | seed twice | rows once; `seeded_at` set once |
| T05 | integration (PG) | two goroutines seed at once | race | one commit; same rows |
| T06 | integration (PG) | seeded; toml changed | boot | `toml_key_superseded` logged once; condition set; rows unchanged |
| T07 | integration (PG) | node with `[tv] slots = off`, home on | node first boot | one override for `livetv.slots`/`.inprocess` for that node only |
| T08 | integration (PG) | stage with `operator` reason | seed then 51.06 sync | reason kept |
| T09 | unit | `catalog_words.mjs` | new reason | en + ar words present |
| T10 | rehearsal (CI) | scenario 12 | update | lists equal; budget 2 min |

## Edge cases

- **A key absent from `server.toml`** imports its default, as the code reads it today.
- **A home with `[postgres] enabled = off`** (external database): the supervisor seeds
  through the same URL it gives the children.
- **An unreadable `server.toml`** stops the boot today already; no seed without config.
- **A rollback after the seed** leaves the rows unused; the older release reads
  `server.toml`, which the seed never changed. Updating forward again finds `seeded_at` set
  and does not seed twice.
- **A node that joins after the seed** gets no overrides: it follows the home.
- **`--no-mdns`** on the command line still wins for that boot (`boot_flag` reason).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/componentseed/seed.go` (new) | `SeedPlan` (pure) and `Seed` (transaction, lock, marker). |
| `cmd/maktaba-server/internal/componentseed/toml.go` (new) | The imported keys, line lookup, `toml_key_superseded`. |
| `cmd/maktaba-server/internal/componentseed/seed_test.go`, `seed_pg_test.go` (new) | T01–T08. |
| `cmd/maktaba-server/cmd_serve.go` | Call `Seed` after the database is up, before `Build*`; nodes call the override seed. |
| `shared/db/migrations/0178_components.sql` / `.sqlite.sql` | `component_override_seed` is added to 51.02's slot (coordinate in the same integration branch; no new slot). |
| `shared/components/catalog.toml` (+ synced copy) | Reason `toml_superseded`. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | `components.reason.toml_superseded`, English and Arabic. |
| `api/rehearsal_components_e2e_test.go` (new) | Scenario 12. |
| `api/rehearsal_seed_e2e_test.go` | The small profile's extra state (one extension on, one operator-disabled stage, `hdhr_device` off). |
| `tools/rehearsal/loops-full-home.txt` (new) | Loops the previous release runs on the rehearsal home. |
| `.github/workflows/update-rehearsal.yml` | Job for scenario 12. |
| `docs/update-rehearsal.md` | Scenario 12 row and "Where things are". |

### Tests

- Unit: T01–T03, T09.
- Integration: T04–T08 (Go Postgres tests in the `integration` job).
- **CI only:** update-rehearsal **scenario 12** (README §11), gating every tag.

### Rollout

Ships with Phase 1. The first boot of the new release seeds; nothing visible changes. Watch
`toml_key_superseded` in the owner's log after the update (expected: none).

### Rollback

`[components] reconcile = off` (51.04) makes every runner act on nothing, whatever was
seeded. Rows can be re-seeded by setting `component_meta.seeded_at = NULL` and restarting
(documented in the runbook, never automatic).

### Dependencies

51.02 (tables, marker). 51.04–51.06 are built in parallel and read these rows.
