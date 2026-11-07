# Story 54.07 — Plugins are Epic 51 components

> Epic 54 · Phase 1 · Depends on: 54.01, 54.06, 54.37, 54.38, Epic 51 (51.02, 51.06, 51.08,
> 51.13, 51.18) · Needed by: 54.08, 54.09, 54.10, 54.11, 54.17

## Why

Epic 51 already gives Maktaba one way to install, enable, disable, hold, update, roll back and
remove a part, with a drain that never loses a job, status per machine, an admin API with role
tests, and a Components page. A second lifecycle for plugins would duplicate all of it and
disagree with it. This story makes each installed plugin an Epic 51 component, and adds only
what third-party code needs: independent versions with a compatibility range, side-by-side
updates, data-aware rollback, keep-off and a purge of what a plugin wrote. The catalog entries
themselves (kind `plugin`, read from `plugin_installs`) are 54.37's; the files on disk are
54.38's.

## Description

### 1. A new component kind: `plugin`

Epic 51 has five kinds (`process`, `stages`, `loop`, `extension`, `asset`) and no way to add
a component at run time. 54.37 adds the sixth, **`plugin`**: "a signed third-party package
whose stages run in `plugins.host`". A plugin component:

- has id `plugin.<plugin id>` and `group = "plugins"`, with the fields 54.37 §1 derives from
  the manifest (`requires = ["plugins.host"]`; `compute.*` needs as soft `wants` on `llm` /
  `search.semantic`, 54.21; no bundle, no licence flag, no capabilities feature);
- stops and starts like a `stages` component (Epic 51 §4.5, the pipeline runner of 51.06):
  stop claiming ≤ 2 s, drain ≤ min(`timeout_s`, 120 s) with `Cancel` sent to the host at the
  deadline (the host returns the checkpoint), force-pause the rest, keep the queue, don't
  feed; enable resumes and catches up with 51's `component_catchup`;
- is switched through Epic 51's own routes too: `PUT /api/components/plugin.<id>`
  (`want` `enabled` / `installed` / `absent`), `POST /api/components/plugin.<id>/stop` and
  `/start` (a hold, until Start), with 51's audit actions (`component.enable`,
  `component.disable`, `component.remove`, `component.hold`, `component.release`);
- uses the reason codes of 54.37 §3 (`incompatible`, `grants_needed`, `revoked`,
  `unreviewed_disallowed`, `entitlement_missing`, `plugin_crash_loop`, `sandbox_partial`,
  `kept_off`), added to `reasons` in `shared/components/catalog.toml` with English and Arabic
  words.

**Keep off (decided 2026-11-03).** An admin's disable is sticky. It writes
`plugin_keep_off (plugin_id PK, set_by, set_at)`, a table keyed by plugin id and kept apart
from `plugin_installs`, and the desired state `want = installed` (Epic 51 has no `disabled`
want); the plugin's status row reads `off` with reason `kept_off`. While the row exists,
nothing but an admin's enable turns the plugin on:

- a plugin update (54.17 auto-update included) installs the new version disabled;
- a server update or rollback, and update-rehearsal scenario 15, keep it disabled;
- a backup restore keeps it disabled (the table is in the backup, and the restore path
  re-applies it before the reconciler's first pass);
- uninstalling and reinstalling the same plugin id installs it disabled; the install dialog
  says "You turned this plugin off on {date}. It stays off until you turn it on."
- only an admin's enable deletes the row (audited): `POST /api/plugins/{id}/enable` or
  `PUT /api/components/plugin.<id>` with `want = enabled`, which go through the same store
  (54.08).

Keep-off is the admin's switch; the store's `disable` revocation (54.17) is separate. Turning
keep-off off never lifts a revocation, and a revoked plugin stays held whatever keep-off
says.

`plugins.host` is an ordinary catalog component (`bundles = ["full", "core"]`) that runs its
process only on demand (54.37 §4): while at least one plugin component is enabled and passes
its gates. An admin can still turn `plugins.host` off as a master switch; every plugin then
waits with `dependency_off`.

### 2. The install table

**`plugin_installs`** `(plugin_id, version, digest, path, manifest JSONB, review_level,
installed_at, previous_version, previous_digest)` is the source of the dynamic catalog entries
(54.37 §2). A write to it sends `NOTIFY plugin_installs_changed`; the plugin's desired row is
in Epic 51's `components` table, keyed `plugin.<plugin id>`, like any other.

### 3. Versions

First-party components move with the server version (Epic 51 H5). Plugins do not: each has its
own semver and a `maktaba` range and `plugin_api` version (54.01).

- At install and at every server start, the store checks the range and the API version. Out of
  range → *Waiting* `incompatible`, stages not registered, queue kept.
- **Before a server update**, the updater (Epic 35; the packs step of 51.18 in
  `cmd/maktaba-server/internal/updater/packs.go`) lists installed plugins whose range
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
3. hand the verified package to the supervisor, which unpacks it to
   `<state>/plugins/<id>/<version>_<sha12>/` with the safe reader and sets `current` (54.38);
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
   `migrate` job (`p_<hash>__migrate`, catalog subject, run on the home) calling the
   plugin's optional migrate step; until it succeeds, the other stages wait. As built:
   `plugin_installs.data_version` (slot 0202) holds the data's version while it is behind
   the manifest's (the update writes it; NULL when up to date). A job of another stage of the
   plugin enqueues the migrate job and is paused (`JobDeferred`, no attempt). The migrate job
   calls the guest's `run("__migrate")` with the payload `{"from", "to"}` when the manifest
   says `migrate = true` (a plugin has one `run` export, so the step is a reserved stage
   name, not a second export), or nothing when it does not; on success it clears the column.
   A rollback compares the kept version's `data_version` with the data's own, so one before
   the migration ran needs no clearing.
5. Keep one previous version on disk; older ones are deleted.

### 6. Rollback

`POST /api/plugins/{id}/rollback` switches back to `previous_*` with the same drain. If the
previous version's `data_version` is lower than the data's, the API answers
`409 plugin_data_newer` unless `?clear_data=true` (the dialog explains: "Roll back and clear what
this plugin stored. Tags, chapters and subtitles it wrote are kept.").

### 7. Uninstall and purge

`want = absent` (Epic 51, `DELETE /api/components/plugin.<id>` or the plugin route): drain,
deregister stages, **delete the plugin's queued jobs** (the
dialog shows the count; this is the only path that drops jobs, because no code will ever run
them), delete files and cache, keep `plugin_data`, `plugin_writes` and settings. With
`purge=true`: delete `plugin_data`, artifacts and settings, and revert each `plugin_writes`
row whose target still equals its `after` value (someone else's later change wins), in one
batched job with progress. Epic 51's `DELETE /api/components/{id}?purge=true` resets only the
component's `rules.*` sections (`Service.ResetSettings`, 51.13); this story extends that purge
for `plugin.*` ids to the plugin's settings key, data, artifacts and writes, so both routes do
the same thing.

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
- **Given** an admin disabled plugin P (keep off),
  **when** P is updated to a new version, the server is updated and rolled back, a backup is
  restored, and P is uninstalled and installed again,
  **then** after each step P is `want = installed` with status reason `kept_off`, and only an admin's enable
  turns it on.
- **Given** P is kept off and also store-revoked with `disable`,
  **when** the admin turns keep-off off,
  **then** P stays held with `revoked` (`409 plugin_revoked`).
- **Given** one plugin enabled,
  **when** it is disabled,
  **then** the supervisor stops the `plugins` process, `plugins.host` reads `running` with
  `idle: true`, and no `maktaba-plugind` process runs (N14).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, go) | install from a fixture package | install | files, `plugin_installs`, desired row, audit, preload called |
| T02 | integration (PG, py) | `plugin_installs` rows | load (54.37) | dynamic entries; `p_*` stages owned |
| T03 | integration (PG, py) | 100 queued, 2 running | disable | Epic 51 §4.5 numbers; 0 lost |
| T04 | integration (PG, go+py) | update with more caps | apply | Waiting `grants_needed`; old running; approve → switch |
| T05 | integration (PG, py) | `data_version` bump | first job | migrate job runs first; others wait |
| T06 | integration (PG, go) | rollback across data versions | rollback | 409 / clear_data path works |
| T07 | integration (PG, go) | purge with edited targets | purge | reverts untouched only |
| T08 | integration (go) | last plugin disabled | reconcile | host process stopped (on demand, 54.37 §4) |
| T09 | CI | `components-chaos` with plugin stages | 100 flips | 0 lost or failed jobs (N9) |
| T10 | CI | update-rehearsal scenario 15 | update + rollback | Running / Waiting `incompatible` as expected |
| T11 | unit | reason codes | words | en + ar for every new code |
| T12 | integration (PG, go) | keep-off set | update, reinstall, restore from a `pg_dump` | `installed`, `kept_off` after each; enable by admin clears it, audited |
| T13 | integration (PG, go) | keep-off + `disable` revocation | admin enable | `409 plugin_revoked`; keep-off row deleted, hold kept |
| T14 | CI | update-rehearsal scenario 15 with a kept-off plugin | update + rollback | still `installed`, `kept_off` |
| T15 | integration (PG, go) | `PUT /api/components/plugin.x` and `DELETE …?purge=true` | apply | same rows and purge as the plugin routes |

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
| `api/internal/plugins/store.go`, `install.go`, `update.go`, `rollback.go`, `uninstall.go` (new) | §4–§7, inside the component store's transactions (`store.SetDesired` / `SetDesiredBatch`, 51.08). |
| `api/internal/components/settings.go` | Purge of a `plugin.*` id reaches the plugin's settings, data and writes (§7). |
| `pipeline/src/maktaba_pipeline/components/runner.py` (Epic 51) | Treat the `plugin` kind as a stage group; send `Cancel` at the drain deadline. |
| `pipeline/src/maktaba_pipeline/plugins/migrate.py` (new) | The migrate job. |
| `cmd/maktaba-server/internal/updater/` | Pre-update incompatibility list. |
| `shared/db/migrations/NNNN_plugin_installs.sql` + `.sqlite.sql` | `plugin_installs`, `plugin_keep_off`. |
| `api/internal/plugins/keepoff.go` (new) | Keep-off rules for install, update, restore and enable. |
| `tests/update_rehearsal/scenario_15_plugins.*` | T10. |
| `web/src/i18n/{en,ar}.json` | Dialog words (the reason codes are 54.37's). |

### Tests

- Integration T01–T08, T12, T13, T15; unit T11; CI T09 (`components-chaos`), T10 and T14 (update rehearsal, gates tags).

### Rollout

Dark until the first install. Touches the updater, so the update rehearsal must pass on the
exact commit before a tag (standing rule).

### Rollback

`[components] reconcile = "off"` (Epic 51) stops runners acting. Release rollback: older
releases ignore `plugin_installs` and read `plugin.*` desired rows as unknown ids; plugin
stages show `not_configured`.

### Dependencies

54.01, 54.06, 54.37 (catalog entries, gates, reason codes), 54.38 (files); Epic 51 51.02
(state tables), 51.06 (stage-group runner), 51.08 (admin API), 51.13 (settings reset),
51.18 (the updater's packs step).
