# Story 52.17 — Components a later release adds start as the bundle says

> Epic 52 · Phase 1 · Depends on: Epic 51 (51.03 seed, 51.17 bundles, 51.18 update rehearsal) ·
> Needed by: 52.6 (the release that ships `review.words`)

## Why

Epic 51 writes the desired rows **once**. `store.Seed` (`shared/components/store/seed.go`) runs
only while `component_meta.seeded_at` is NULL, and `componentseed.Plan`
(`cmd/maktaba-server/internal/componentseed/seed.go`) gives every catalog id of that release a
want. An id that a later release adds has no row on a home that is already seeded, and **no row
reads as on**: the API's `wantOf` ("no row is the catalog default, on",
`api/internal/components/effect.go`) and the runners' gates (`shared/components/runner`) both
treat it as enabled, and an asset with no row is never installed by the packs runner.

For this epic that means the update that adds `review.words` would:

- start word review on a **Core** home, whose bundle leaves it out;
- start it on a Full home **without** `runtime.lang`, because a requirement with no row also
  reads as on, so `word_check` would run and fail on a missing tokenizer.

Every later epic that adds a component has the same problem, so the fix is general, but nothing
in Epic 51 provides it yet.

## Description

### 1. A seed for new ids, at every boot

`componentseed.SeedNew(ctx, st, cat, release)` (new), called from
`cmd/maktaba-server/serve_components.go` right after the first-boot `Seed`, on the home only:

1. In one transaction under the seed's advisory lock (`store.SeedLock`), read
   `component_meta.bundle` and the ids that have a `components` row.
2. For each catalog id with no row (core ids excluded, they are always on):
   - a `stages`, `process`, `loop` or `extension` component: `enabled` when the home's bundle
     has it in `bundles`, else `installed`. For `custom`, the bundle is the base the API reports
     (`bundle.base` of `GET /api/components`, full or core);
   - an `asset`: `installed` when the home's bundle has it, or when a component this step
     enables requires it; else `absent`. `installed` makes the packs runner download it.
3. Insert the rows (`ON CONFLICT (id) DO NOTHING`, `updated_by = seed`) and write one
   `audit_log` row, action `component.seed`, payload `{"release", "wants", "new": true}`.
4. The insert fires `components_changed`, so the runners and `GET /api/capabilities` move at
   once (`capabilities_changed` on `/ws/home` when a feature changes).

Nothing that has a row is touched: an admin's choice always wins. The step costs one query when
there is nothing new.

### 2. Machines

A joined machine follows the home's new rows (no override exists for a new id). Its own
`server.toml` has nothing to say about a component it never knew, so `SeedOverrides` is not
called for new ids.

### 3. What admins see

The Components page shows the new component with its phase and the usual "Based on Full"
summary; a new component outside the home's bundle is off, so the summary does not change
("Based on Core" stays true).

## Acceptance criteria

1. **Given** a home seeded as `core` on release N, **when** it updates to N+1 that adds
   `review.words`, `runtime.lang`, `lang.ar` and `lang.en`, **then** `review.words` is
   `installed` (off), the assets are `absent`, and no `word_check` job is created.
2. **Given** a home seeded as `full`, **when** it updates, **then** `review.words` is `enabled`,
   `runtime.lang`, `lang.ar` and `lang.en` are `installed` and downloaded, and `word_check` jobs
   defer (no attempt used) until the `runtime.lang` pack is on the machine (52.16), then run.
3. **Given** an admin who set a row before the update, **then** that row is unchanged.
4. **Given** two replicas booting at once, **then** each new id gets exactly one row and one
   audit row is written.
5. **Given** a `custom` home based on core with `transcription` ticked, **then** new ids follow
   the core bundle.
6. **Given** `update-rehearsal`, **then** a Full and a Core fixture home on the previous release
   update to a release with one fixture component more, and each gets it as its bundle says.

## Build plan

### Files

- `cmd/maktaba-server/internal/componentseed/seed.go`: `SeedNew`, sharing `Plan`'s bundle rule.
- `shared/components/store/seed.go`: `SeedMissing(ctx, plan)` (the lock, the read, the insert,
  the audit row) next to `Seed`.
- `cmd/maktaba-server/serve_components.go`: the call after `Seed`.
- `tools/rehearsal/`: the "one component more" step of the update rehearsal (51.18).

### Tests

- `cmd/maktaba-server/internal/componentseed/seed_test.go`: core, full, custom, asset rules,
  rows kept.
- `shared/components/store/seed_test.go`: SQLite and scratch Postgres, the race of criterion 4.
- The update rehearsal step (criterion 6), CI only.

### Rollout

- Ships in or before the release that ships 52.6. A home seeded before it gets rows for every
  id it lacks at the first boot of that release. Ids added by Epic 51 itself all have rows
  already (51.03 seeded them), so nothing else changes.
- Back out: revert. The rows stay and mean what they say.

## Edge cases

- **A component a release renames:** the old id's row stays (ignored, as for any dropped id); the
  new id gets the bundle's default. A rename that must keep the admin's choice copies the row in
  the same release (not needed by this epic).
- **`[components] reconcile = "off"`:** the rows are still written; no runner acts on them until
  reconcile is on.
- **A new asset with `in_payload = true` on this platform:** it is on disk already, so `installed`.
