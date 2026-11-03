# Story 51.07 — Extensions become components

> Epic 51 · Phase 1 · Depends on: 51.06 · Needed by: 51.08, 51.09

## Why

Plex, dizqueTV and Cloudflare Stream already have an on/off switch: the `extensions` row
(`enabled` default false, `0089_queue_control_and_logs.sql:70-72`), written by
`PUT /api/extensions/{name}` (`api/internal/handlers/queues/extensions.go`) and read by the
pipeline's graph sync, which turns their stages off with `disabled_reason =
'extension_disabled'` (`engine/sync.py`). That switch knows nothing of holds, requirements or
status, and a second switch on the Components page would fight it. This story makes each
built-in extension a component (`ext.plex`, `ext.dizquetv`, `ext.cloudflare_stream`) whose
desired state is the source of truth, keeps the `extensions` row in step, and keeps every
`/api/extensions` route working for today's web and apps.

## Description

### 1. One switch, two places kept in step

The component's desired state decides. The `extensions.enabled` column becomes a mirror,
written in the same transaction as the desired state, by whoever writes:

| Writer | Writes | Also writes, same transaction |
|---|---|---|
| `PUT /api/extensions/{name}` with `enabled` (today's web and apps) | `extensions.enabled` | `components.want` (`enabled` / `installed`) + one `audit_log` row |
| `PUT /api/components/ext.<name>` (51.08) | `components.want` | `extensions.enabled` |
| The seed (51.03) | `components.want` from `extensions.enabled` | nothing (they already agree) |

The rule lives in one place: `api/internal/components/writethrough.go`, a table
`component id → func(tx, want)` that the component store's `SetDesired` calls inside its
transaction. 51.08 uses the same table, so neither route can update one side alone.
`livetv.hdhomerun` ↔ `hdhr_device.enabled` (51.05) moves onto this table too.

A **hold** does not touch `extensions.enabled`: the extension is still enabled, only stopped
now. Its stages get `component_held` (51.06).

`extensions.settings` and the secrets in `home_secrets` stay where they are (H7). The
catalog lists them under `settings` (`ext:plex`, `rules.plexlive`, `rules.dizquetv`), and
nothing about how they are stored changes. Turning an extension off keeps its settings and
secrets, as today.

### 2. What each component covers

| Component | Stages | Other parts | Requires |
|---|---|---|---|
| `ext.plex` | `plex_index`, `plex_collection`, `plex_groups`, `plex_livetv` | `rules.plexlive`; Plex sign-in routes | — |
| `ext.dizquetv` | `dizquetv_show`, `dizquetv_channels`, `dizquetv_audit` | `rules.dizquetv` | `livetv` |
| `ext.cloudflare_stream` | none (manifest only) | the API's Cloudflare calls (`api/internal/tvremote`) | `livetv` |

### 3. In the pipeline

`engine/sync.py:stage_enabled_state()` keeps its order and adds the component gate after it:

1. `unreleased` → `not_configured` (unchanged);
2. extension row disabled → `extension_disabled` (unchanged, so the Queues page shows the
   same words as today for a plain "off");
3. extension enabled but not configured → `not_configured` (unchanged);
4. component gate closed at home level → `component_held` (a hold) or `component_off` (a
   requirement is off: `dizquetv` while `livetv` is off, H3).

So a home that never touches the Components page sees exactly today's `job_stages` rows.
The pipeline component runner (51.06) treats `ext.*` like any stage group: stop claiming,
drain (30 s), return, don't feed, catch up on enable. The extension's own `register()` code
is not changed.

### 4. Status

The runner reports `ext.*` with the usual phases, plus one new reason: enabled but its
required settings are missing → **Waiting, `not_configured`** (today: stages silently off).
`ext.cloudflare_stream` has no stages; the API's runner (51.05) reports it from the
`tvremote` integration's own check (token present, last Cloudflare call result). Words:
`components.reason.not_configured` ("Needs its settings: open {component}'s settings") in
`en.json` and `ar.json`, together. The reason is added to the catalog's `reasons`.

### 5. `/api/extensions` stays compatible

- `GET /api/extensions` answers the same fields as today. It adds two optional fields per
  extension, `component` (`"ext.plex"`) and `held` (bool), which today's clients ignore.
  `enabled` is read from the component's desired state (it equals the mirror).
- `PUT /api/extensions/{name}` keeps its body and answers. With `enabled` it now goes
  through the write-through (§1). Changing settings alone works as today.
- `POST /api/extensions/{name}/test` and the Plex sign-in routes work whatever the state,
  so an admin can set up and test an extension **before** turning it on.
- No route answers `409 component_off` here: these routes manage the component.
- Third-party extensions loaded through Python entry points (H5) are **not** components:
  they keep today's `extensions` row as their only switch, and are listed by
  `GET /api/extensions` as before.

### 6. Roles

Every `/api/extensions` route is already admin-only. This story adds role tests for each of
`GET /api/extensions`, `PUT /api/extensions/{name}`, `POST /api/extensions/{name}/test`:
no one → 401; viewer → 403; cloud-linked play sign-in → 403; admin → 200. The write-through
cannot be reached by any other role, and the audit row names the admin.

## Acceptance criteria

- **Given** Plex enabled through today's web page,
  **when** `PUT /api/extensions/plex {"enabled": false}` is sent,
  **then** `extensions.enabled = false`, `components.ext.plex.want = installed`, one audit
  row, all in one transaction; within 2 s no machine claims a `plex_*` stage, and running
  Plex jobs drain within 30 s and return to the queue.
- **Given** `ext.plex` disabled through `PUT /api/components/ext.plex` (51.08),
  **when** the web's Extensions page loads,
  **then** it shows Plex off (`extensions.enabled = false`).
- **Given** `ext.dizquetv` enabled and `livetv` disabled,
  **when** the runner passes,
  **then** dizqueTV stays `want = enabled`, is Waiting with `dependency_off:livetv`, and its
  stages are `component_off`; **when** `livetv` comes back, **then** dizqueTV runs again with
  nobody touching it, and a catch-up queues what it missed.
- **Given** Plex enabled with no server URL set,
  **when** the runner reports,
  **then** `ext.plex` is Waiting with `not_configured`, and the stage rows are as today
  (`not_configured`).
- **Given** a home that never opens the Components page,
  **when** it updates to this release,
  **then** every `extensions` row and every extension stage's `job_stages` row is unchanged
  (update-rehearsal scenario 12, 51.03).
- **Given** a viewer or a cloud-linked play sign-in,
  **when** it calls `PUT /api/extensions/plex`,
  **then** it gets 403 and nothing changes.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, api) | Plex row enabled | `PUT /api/extensions/plex` enabled false | both sides + audit, one transaction |
| T02 | integration (PG, api) | write-through func fails | PUT | whole transaction rolled back; neither side changed |
| T03 | integration (PG, api) | `SetDesired(ext.plex, installed)` | commit | `extensions.enabled = false` |
| T04 | unit (api) | `GET /api/extensions` | read | same fields as before + `component`, `held` (golden of today's body with the two added) |
| T05 | unit (py) | `stage_enabled_state` table: row off / not configured / held / dependency off | sync | reasons as §3, in that order |
| T06 | integration (PG, py) | dizqueTV enabled, `livetv` off | runner pass | Waiting `dependency_off:livetv`; stages `component_off` |
| T07 | integration (PG, py) | then `livetv` on | runner pass | stages enabled; one `component_catchup` for `ext.dizquetv` |
| T08 | unit (py) | Plex enabled, unconfigured | report | Waiting `not_configured` |
| T09 | role test (api) | the three routes | no one / viewer / cloud-linked play sign-in / admin | 401 / 403 / 403 / 200 |
| T10 | unit (py) | an entry-point extension | discover + sync | not in components; row behaves as today |
| T11 | unit | `catalog_words.mjs` | `not_configured` reason | en + ar present |
| T12 | integration (PG, api) | `POST /api/extensions/plex/test` with `ext.plex` installed | call | runs the test (not 409) |
| T13 | CI | `components-live` | flip `ext.plex` with fake Plex jobs | N2, N4, N5 |

## Edge cases

- **A row written by an older release after a rollback and forward again** (the older
  release changed `extensions.enabled` alone): at boot the API compares mirror and desired
  state; if they differ it takes the newer `updated_at` and writes the other side, with an
  audit row `component.mirror_repaired`.
- **The extension package missing** (`discover()` logs `extension_missing`): the component is
  Failed with `not_in_payload`; its desired state is kept.
- **Plex Live TV (`plex_livetv`)** also needs `livetv`'s channels to have anything to do.
  It is not a hard requirement: with `livetv` off it has no work, which is not an error.
- **Cloudflare Stream token removed** while enabled: Waiting `not_configured`, same as Plex.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/components/writethrough.go` (new) | The write-through table: `ext.*` ↔ `extensions.enabled`, `livetv.hdhomerun` ↔ `hdhr_device.enabled`; mirror repair at boot. |
| `api/internal/handlers/queues/extensions.go` | `PutExtension` through the write-through; `Extensions` adds `component`, `held`. |
| `api/internal/handlers/tv/tv.go` | `hdhr_device.enabled` via the same table (replaces 51.05's direct call). |
| `api/internal/tvremote/integration.go` | Report `ext.cloudflare_stream` status to the API runner. |
| `pipeline/src/maktaba_pipeline/engine/sync.py` | Order of §3. |
| `pipeline/src/maktaba_pipeline/components/stages.py` | `ext.*` stage sets from the catalog; built-ins only. |
| `pipeline/src/maktaba_pipeline/components/runner.py` | `not_configured` from `ExtensionState.configured`. |
| `shared/components/catalog.toml` (+ synced copy) | `ext.*` entries' `settings`; reason `not_configured`. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | `components.reason.not_configured`, English and Arabic. |
| `api/internal/handlers/queues/extensions_component_test.go` (new) | T01–T04, T09, T12. |
| `api/internal/components/writethrough_test.go` (new) | T02, T03, mirror repair. |
| `pipeline/tests/components/test_extensions.py` (new) | T05–T08, T10. |
| `tests/components/test_live.py` | T13 scenario (fake Plex stage handler). |

### Tests

- Unit: T04, T05, T08, T10, T11.
- Integration: T01–T03, T06, T07, T12 (Go and pipeline Postgres tests, `integration` job).
- Role tests: T09.
- **CI:** `components-live` (51.06) gains the extension scenario; `catalog` (51.01) covers
  the new reason's words; update-rehearsal scenario 12 (51.03) proves `extensions` rows are
  unchanged on update.

### Rollout

Phase 1, dark. Seeded from `extensions.enabled` (51.03), so the mirror and the desired state
agree from the first boot, and today's Extensions page behaves exactly as before.

### Rollback

`[components] reconcile = off` stops the runners acting; the write-through still keeps both
sides equal, so the older Extensions page stays correct. A release rollback reads only
`extensions.enabled`, which has always been kept current.

### Dependencies

51.06 (the pipeline runner and stage gating it extends). 51.08 reuses the write-through
table.
