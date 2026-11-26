# Story 54.46 — Install and remove a Maktaba plugin

> Epic 54 · Phase 5 · Depends on: 54.44, 54.45, 54.38, Epic 51 (51.15, 51.16) · Needed by: 54.47, 54.48, 54.50 · Built

## Why

After 54.44 a Maktaba plugin can be turned on and off on its page, but its files are always on disk and its Remove is absent with the reason in words. This story lets an admin install and remove each Maktaba plugin like any store plugin, with one source of truth for its state and no lost jobs.

## Description

### 1. One source of truth

A Maktaba plugin keeps the Epic 51 component whose stages it implements as its desired row (README §15.7 as amended): `want` is `absent` (not on disk), `installed` (on disk, off) or `enabled`. The component gains the asset behaviour of 51.16 for the plugin's package: `not_installed`, `installing`, `removing`. No second row is created and `plugin.<id>` entries stay third-party only, so licence gates, bundles and per-machine overrides keep working unchanged.

### 2. Install

`POST /api/plugins/install` with `{store: {id, version}}` or a file installs a Maktaba package through the path third-party packages use (54.38): the supervisor verifies and unpacks it under `<data>/plugins/packages/`, then the component's required assets install (51.16). Its stage rows are created, or their `impl` set, only once the package is on disk.

### 3. Remove

`DELETE /api/plugins/{id}` sets `want: absent`. Running jobs drain; queued jobs of its stages are parked, not dropped, and say why (`component_off`). The files are removed after the drain. Data the plugin wrote (transcripts, thumbnails, tags) is Maktaba's own data and stays; a separate, explicit action clears it where a stage already has one. Removing a plugin others require is refused with what requires it (`component_required_by`).

### 4. The runner and the worker

A runner group loads only the plugins installed on its machine. A stage whose plugin is not installed is not claimed by any worker. The in-process path for the stage (54.36) is gone for a plugin that is not installed: there is no code to run.

### 5. The page

The Plugins page shows Remove and Install for Maktaba plugins, and `source` becomes `store` or `file`. The with-release refusals of 54.44 §4 go away.

## Tests

- Remove under load: no job lost or run twice; the queue shows the parked jobs with the reason; install again and they run.
- Remove refused while a dependant is on; allowed after it is off.
- Role tests: every route admin only.
- update-rehearsal: a home with some Maktaba plugins removed updates and rolls back with the same set.

## As built

- **One source of truth.** A Maktaba plugin's state is its component's desired row, as §1
  has it: `absent` means not installed (phase `not_installed`), `installed` means off, and
  `enabled` means on. Asset phases `installing` and `removing` do not occur yet because
  nothing downloads before 54.49. No `plugin.<id>` entry is made.
- **Remove** (`DELETE /api/plugins/{id}`) sets the component's want to `absent` through the
  components service. That service refuses it with what needs it (`component_required_by`)
  while a component that is on requires it. The answer counts the queued jobs parked
  (`queued_jobs_parked`). Their stages wait with `component_off` and no worker claims them,
  since `absent` closes the gate as off does. Running jobs drain as for any component turned
  off. `?purge=true` checks whether another enabled component requires the plugin first
  (`component_required_by`, 409) and otherwise refuses the purge (`plugin_data_kept`, 409):
  what a Maktaba plugin wrote is the library's own and stays.
- **Install** (`POST /api/plugins/{id}/install`, `{"enable": bool}`) puts a removed Maktaba
  plugin back from the release: want `installed`, or `enabled`, and its parked jobs run again.
  Its code ships with the server, so nothing is downloaded. A package of a Maktaba plugin from
  the store or a file is trusted only by 54.45 §3. No such package can be had yet: the store
  is not deployed and Maktaba's key is not made. So installing from a store package or a file
  (§2's other half) comes with the offline files of 54.49, through the same rule. Any other id
  is `404` here and installs from a file or the store as before.
- **The runner and the worker (§4).** A removed plugin's stages are never claimed, so its code
  never runs: the runner imports a stage's module only for a job. The code stays in the signed
  pipeline wheel, which is the package's own code at the server's version (54.45 §3). Moving
  it out of the wheel waits for 54.47, which lets a plugin's version differ from the server's.
- **The page (§5).** A Maktaba plugin shows Remove, with words saying its jobs wait and its
  data stays. A removed one shows Install. The "cannot be removed yet" refusal is gone; the
  other with-release refusals (update, roll back, grants, schema settings) stay until 54.47.
  `source` stays `release` for a plugin that came with the server.
- **Tests.**
  - `api/internal/handlers/plugins/published_pg_test.go`: removal parks the queued job (still
    pending), the page says `absent`, installing again turns it on with the job still there,
    install without enable is off, purge is refused, and a third-party id is refused. Removing
    `transcription` while `subtitles` is on is `component_required_by`, and allowed once
    `subtitles` is off.
  - `TestEveryPluginRouteIsAdminOnly` lists the new route.
  - `pipeline/tests/components/test_stage_groups.py`: an `absent` component's stages are off
    with `component_off`, and open again when enabled.
  - `PluginsSection.test.tsx`: Remove and its confirmation, Install of a removed plugin, the
    words in English and Arabic.
  - Update rehearsal scenario 15: a Maktaba plugin removed on the release under test stays
    removed through a failed update's rollback and through the next update.

