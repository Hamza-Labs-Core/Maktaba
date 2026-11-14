# Story 54.48 — What is preinstalled, licence gates, and moving existing homes

> Epic 54 · Phase 5 · Depends on: 54.46, 54.47, Epic 51 (51.17, 51.19) · Needed by: 54.50 · Not built

## Why

When Maktaba's plugins are store plugins, a fresh home still has to work at once, a bundle still has to mean something, a licence still has to gate what it gates, and a home that runs today must keep running through the change with no downtime and no lost job.

## Description

### 1. Bundles are sets of preinstalled plugins

The catalog's `bundles` field stays the one definition (README §15.7). A bundle names components; for a component a Maktaba plugin implements, being in the bundle means the plugin is preinstalled and on. Setup's "Choose what to run" (51.17) and `POST /api/components/bundle` are unchanged in shape.

### 2. A fresh install

A fresh home installs the plugins of the bundle the admin chose from the packages its installer carries (54.50), with no store visit and no internet. Plugins outside the bundle are `absent` and can be installed later.

### 3. Licence gates

A component's `licence` flag gates its plugin as today: install is allowed, enable is refused with `licence_missing` naming the plan. The store listing of a gated Maktaba plugin says which plan it needs.

### 4. Moving an existing home

At the update that brings this in, each Maktaba plugin the home has is recorded as installed from the release's own packages, with the state its component has (`enabled` or `installed`). Nothing is downloaded, no stage changes where it runs, and no job is dropped: the stage rows and the queue are untouched. The move is one expand-only migration plus the supervisor adopting the packages already on disk, and it is rolled back by the server's rollback.

### 5. Joined machines

A joined machine runs the Maktaba plugins the home has installed, in the versions the home has, fetched from the home as third-party packages are (54.42). Its per-machine overrides (51.12) keep working.

## Tests

- update-rehearsal: a `full` home and a `core` home move across the change mid-queue and back; the set of plugins and every job survive.
- A fresh install of each bundle offline ends with exactly the bundle's plugins installed.
- A gated plugin installs and refuses enable with `licence_missing`.
