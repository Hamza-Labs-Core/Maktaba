# Story 54.46 — Install and remove a Maktaba plugin

> Epic 54 · Phase 5 · Depends on: 54.44, 54.45, 54.38, Epic 51 (51.15, 51.16) · Needed by: 54.47, 54.48, 54.50 · Not built

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
