# Story 54.51 — Updates within a variant, moving between variants, and what CI pays

> Epic 54 · Phase 5 · Depends on: 54.50, 54.47, Epic 24, Epic 51 (51.17) · Not built

## Why

Two variants must not become two products. A home stays on its variant through updates, moves to the other without reinstalling or losing jobs or data, and CI proves both without building everything on every pull request.

## Description

### 1. Updates stay in the variant

Each variant has its own update feed key per platform, as the Mac Core app has today (`darwin-aarch64-core`). A home updates from the feed of the variant it installed. The server payload is the same in both; only the preinstalled packages and packs differ.

### 2. Moving between variants

- **Core to Full**: the admin applies the `full` bundle (`POST /api/components/bundle`, 51.17 §5). The plugins and assets install from the store, or from the Full offline file on a home with no internet. Nothing is reinstalled and the queue keeps running. The home's feed changes to Full so later updates bring the packages with them.
- **Full to Core**: the admin applies the `core` bundle. It never silently removes a plugin in use: the dry run lists every plugin and asset that would turn off, the jobs waiting on each and what people lose, and by default only turns them off (files kept). Removing files needs `remove_unused` and a second confirmation. A plugin the admin keeps stays installed and updates through the store.
- The variant is a default for updates, not a lock: any plugin can be installed on Core and removed on Full (54.46).

### 3. Offline, licence, joined machines

- A Core home with no internet installs Maktaba plugins from files (54.49).
- Licence gates are the same in both variants (54.48 §3).
- A joined machine follows its home's set of plugins whatever variant either was installed from (54.48 §5).

### 4. Signing and the update rehearsal

Both variants' artifacts are in the release's signed checksums and both feeds are signed as today. `update-rehearsal` gains a variant dimension for the scenarios that touch packaging: a Core home and a Full home each update and roll back, and a home moves Core to Full and back mid-queue with no job lost or rerun. A tag is refused unless both pass on its commit.

### 5. What CI pays

- **Per pull request**: nothing new unless the change touches packaging, the catalog's `bundles`, a first-party manifest or the release workflow; then one platform's Core archive is built and its contents checked against the bundle. This follows the rule that CI runs only related jobs.
- **Per release**: both variants on every platform, and the variant scenarios of the rehearsal.
- The added minutes per release are to be measured on the first release that builds both and recorded here.

## Tests

- The rehearsal scenarios of §4.
- Full to Core dry run names every plugin in use and removes nothing without `remove_unused`.
- A Core home that installed one plugin keeps it across an update and a rollback.
