# Story 54.07 — Plugins are Epic 51 components

> Epic 54 · Phase 1 · Depends on: 54.01, 54.06, Epic 51 (51.02, 51.06, 51.16) · Needed by:
> 54.08, 54.09, 54.10, 54.11, 54.17

## Why

Epic 51 already gives Maktaba one way to install, enable, disable, hold, update, roll back and
remove a part, with a drain that never loses a job, status per machine, an admin API with role
tests, and a Components page. A second lifecycle for plugins would duplicate all of it and
disagree with it. This story makes each installed plugin an Epic 51 component, and adds only
what third-party code needs: dynamic catalog entries, independent versions with a compatibility
range, side-by-side updates, and data-aware rollback.

## Description

### 1. A new component kind: `plugin`

Epic 51 has five kinds (`process`, `stages`, `loop`, `extension`, `asset`). This story adds
**`plugin`**: "a signed third-party package whose stages run in `plugins.host`". A plugin
component:

- has id `plugin.<plugin id>` and `group = "plugins"`;
- `requires = ["plugins.host"]` (the first-party `process` component of 54.02), plus
  `compute.*` needs mapped to `llm` / `search.semantic` as soft `wants` (54.21);
- stops and starts like a `stages` component (Epic 51 §4.5): stop claiming ≤ 2 s, drain ≤
  min(`timeout_s`, 120 s) with `Cancel` sent to the host at the deadline (the host returns the
  checkpoint), force-pause the rest, keep the queue, don't feed; enable resumes and catches up;
- has its own reason codes: `incompatible` (server or plugin API out of range),
  `grants_needed`, `revoked`, `unreviewed_disallowed`, `plugin_crash_loop`, `sandbox_partial`
  (inherited from the host), each with English and Arabic words.

`plugins.host` itself is enabled automatically while any plugin component is `want = enabled`
and disabled when none is (a derived desired state, written by the plugin store in the same
transaction).

### 2. Dynamic catalog entries

`catalog.toml` stays static and embedded. Epic 51's catalog loader (51.01) gains one extra
source: rows of **`plugin_installs`** `(plugin_id, version, digest, path, manifest JSONB,
review_level, installed_at, previous_version, previous_digest)`, turned into catalog entries
at load and on `NOTIFY plugin_installs_changed`. The CI rule "every stage belongs to exactly one
component" (51.01) treats `p_*` stages as belonging to their plugin component.

### 3. Versions

First-party components move with the server version (Epic 51 H5). Plugins do not: each has its
own semver and a `maktaba` range and `plugin_api` version (54.01).

- At install and at every server start, the store checks the range and the API version. Out of
  range → *Waiting* `incompatible`, stages not registered, queue kept.
- **Before a server update**, the updater (Epic 35/51.18) lists installed plugins whose range
  excludes the new version, and the Updates page shows them ("These plugins will pause after
  the update: …"). The update is not blocked.
- When a newer compatible version is in the catalog (54.17), the plugin shows "Update
  available".

### 4. Install

`POST /api/plugins/install` (54.08 owns routes and roles) with a package from the store
(54.17) or a file (54.10):

1. verify signatures and revocation (54.14; Phase 1: developer-signed or unsigned only, with
   the opt-in of 54.10);
2. validate the manifest (54.01) and the range;
3. unpack with the safe unpacker to `<data>/plugins/<id>/<version>_<digest>/`;
4. `Preload` in the host (audit + AOT compile, 54.02) — if the host is not running, it is started
   for the preload and stopped after;
5. write `plugin_installs` and the component's desired row `want = installed` in one
   transaction, with an `audit_log` row;
6. the admin's grants (54.08) are stored; the plugin is enabled only when the admin chooses
   Enable (the install dialog offers "Install and turn on").

### 5. Update (side by side)

1. Install the new version beside the old (steps 1–4); the old keeps running.
2. Compare capabilities: more required capabilities → *Waiting* `grants_needed` on the new
   version, the old one keeps running until an admin approves (or declines and stays).
3. Switch: drain the old version's running jobs (they finish on the old version; new claims
   wait ≤ the drain), atomically write the `current` pointer file and `plugin_installs`
   (`previous_*` ← old), register the new stage versions. Stages whose `version` changed rerun
   per their fingerprints (54.06 §4).
4. Data migration: if `data_version` grew, the first job of the new version is preceded by one
   `migrate` job (`p_<hash>__migrate`, catalog subject) calling the plugin's optional
   `migrate(from-version)` export; until it succeeds, the other stages wait.
5. Keep one previous version on disk; older ones are deleted.

### 6. Rollback

`POST /api/plugins/{id}/rollback` switches back to `previous_*` with the same drain. If the
previous version's `data_version` is lower than the data's, the API answers
`409 plugin_data_newer` unless `?clear_data=true` (the dialog explains: "Roll back and clear what
this plugin stored. Tags, chapters and subtitles it wrote are kept.").

### 7. Uninstall and purge

`want = absent` (Epic 51): drain, deregister stages, **delete the plugin's queued jobs** (the
dialog shows the count; this is the only path that drops jobs, because no code will ever run
them), delete files and cache, keep `plugin_data`, `plugin_writes` and settings. With
`purge=true`: delete `plugin_data`, artifacts and settings, and revert each `plugin_writes`
row whose target still equals its `after` value (someone else's later change wins), in one
batched job with progress.

### 8. The rehearsal

Update-rehearsal scenario 15: a home with one plugin installed and enabled updates to the next
release and back. After each step the plugin is *Running*, its queued jobs are intact, and a
plugin built with a range that excludes the target goes *Waiting* `incompatible` (not
*Failed*), and comes back after the rollback.

## Acceptance criteria

- **Given** an installed, enabled plugin with 100 queued jobs and 2 running,
  **when** an admin disables it,
  **then** no new job is claimed within 2 s, the 2 running finish or are force-paused with
  their checkpoint within the drain, and all 100 queued jobs remain.
- **Given** version 1.0 running and 1.1 installed asking for a new required capability,
  **when** the update is applied,
  **then** 1.1 is *Waiting* `grants_needed`, 1.0 keeps running, and approving the grant
  switches to 1.1 with no job lost.
- **Given** 1.1 with `data_version = 3` replaced 1.0 with `data_version = 2`,
  **when** the admin rolls back without clearing data,
  **then** `409 plugin_data_newer`.
- **Given** a plugin with range `<0.7.0`,
  **when** the server updates to 0.7.0,
  **then** the plugin is *Waiting* `incompatible`, its queue kept, and the Updates page listed
  it before the update.
- **Given** a plugin that wrote tags to 10 videos, 2 of which an admin edited afterwards,
  **when** it is uninstalled with purge,
  **then** its tags are removed from the 8 untouched videos and kept on the 2 edited ones.
- **Given** no plugin enabled,
  **when** the last plugin is disabled,
  **then** `plugins.host` stops and no `maktaba-plugind` process runs (N14).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, go) | install from a fixture package | install | files, `plugin_installs`, desired row, audit, preload called |
| T02 | integration (PG, py) | catalog loader with plugin rows | load | dynamic entries; `p_*` stages owned |
| T03 | integration (PG, py) | 100 queued, 2 running | disable | Epic 51 §4.5 numbers; 0 lost |
| T04 | integration (PG, go+py) | update with more caps | apply | Waiting `grants_needed`; old running; approve → switch |
| T05 | integration (PG, py) | `data_version` bump | first job | migrate job runs first; others wait |
| T06 | integration (PG, go) | rollback across data versions | rollback | 409 / clear_data path works |
| T07 | integration (PG, go) | purge with edited targets | purge | reverts untouched only |
| T08 | integration (go) | last plugin disabled | reconcile | host stopped |
| T09 | CI | `components-chaos` with plugin stages | 100 flips | 0 lost or failed jobs (N9) |
| T10 | CI | update-rehearsal scenario 15 | update + rollback | Running / Waiting `incompatible` as expected |
| T11 | unit | reason codes | words | en + ar for every new code |

## Edge cases

- **Install while the same version is installed**: no-op, answers 200 with `already_installed`.
- **A package with the same id but another developer key** (Phase 2): refused
  `plugin_key_mismatch` unless the store marks a key transfer.
- **Disk full during unpack**: the half-written folder is deleted; nothing in the database
  changes.
- **The previous version deleted by hand**: rollback answers `409 plugin_no_previous`.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/components/catalog.toml` | `plugins.host` entry (kind `process`), reason codes. |
| Epic 51 catalog loader (Go and Python) | Dynamic entries from `plugin_installs`; kind `plugin`. |
| `api/internal/plugins/store.go`, `install.go`, `update.go`, `rollback.go`, `uninstall.go` (new) | §4–§7, inside the component store's transactions (Epic 51 51.08). |
| `cmd/maktaba-server/internal/plugins/` (new) | Unpack, pointer files, preload call; used by the supervisor's reconciler for the files side. |
| `pipeline/src/maktaba_pipeline/components/runner.py` (Epic 51) | Treat `plugin` kind as a stage group; send `Cancel` at drain deadline. |
| `pipeline/src/maktaba_pipeline/plugins/migrate.py` (new) | The migrate job. |
| `cmd/maktaba-server/internal/updater/` | Pre-update incompatibility list. |
| `shared/db/migrations/NNNN_plugin_installs.sql` + `.sqlite.sql` | `plugin_installs`. |
| `tests/update_rehearsal/scenario_15_plugins.*` | T10. |
| `web/src/i18n/{en,ar}.json` | Reason codes. |

### Tests

- Integration T01–T08; unit T11; CI T09 (`components-chaos`), T10 (update rehearsal, gates tags).

### Rollout

Dark until the first install. Touches the updater, so the update rehearsal must pass on the
exact commit before a tag (standing rule).

### Rollback

`[components] reconcile = off` (Epic 51) stops runners acting. Release rollback: older
releases ignore `plugin_installs`; plugin stages show `not_configured`.

### Dependencies

54.01, 54.06; Epic 51 51.02 (state tables), 51.06 (stage-group runner), 51.16 (install
plumbing and safe unpacker).
