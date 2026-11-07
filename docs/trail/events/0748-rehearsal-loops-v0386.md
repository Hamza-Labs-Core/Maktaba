---
schema: 1
kind: pr
pr: 748
title: Scenario 12 reads the previous release's loops and components from the home itself
date: 2026-11-07
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
agent: claude-code
prs: [686]
---

## What happened
- The first release since v0.3.82 (v0.3.86) moved the rehearsal's "previous release" forward, and scenario 12 started failing everywhere, twice over:
  - logs.index's `api:errors_notifier` was no longer "added since the previous release" (by `since`) and was not in the hand-kept `tools/rehearsal/loops-full-home.txt`.
  - `lang.ar` and `lang.en` came out `absent` on the Full home, where the check wanted `installed`. The seeded home is made on the previous release, so v0.3.86's *first* seed wrote their rows, and the first seed sets an asset by what is on disk (none of the packs is there), not by the bundle. That is right, and it is what homes in the field got. Only a component the previous release had no row for gets the bundle's want (SeedMissing, story 52.17). While the previous release was v0.3.82, whose catalog lacked them, that was this commit's job.
- Scenario 12 no longer keeps a list or compares `since`. Before the update it reads the loops the previous release runs from its own `component_status`, once every component it left enabled with an api or streaming loop reports one running. "Added by this release" means "the previous release had no row for it". The bundle check covers every such row, and a row the previous release seeded is only checked to be unchanged. `loops-full-home.txt` is gone.
- `since` was wrong for 18 components: 0.3.82 or 0.3.83, but v0.3.83 to v0.3.85 were tagged and never published, and #672 merged after the v0.3.82 tag. v0.3.86 is the first release that shipped any of them, so they now say 0.3.86, and the catalog's comment says "first published release". Nothing at run time reads `since`.

## What we learned
- On the rehearsal's seeded home, the previous release's first seed writes every component of the previous release's catalog. Only components newer than the previous release go through SeedMissing.
- A rehearsal check that reads the previous release's state before the update does not need editing each release. A hand list, or a version comparison against the previous release, breaks every time the previous release moves forward.

## Open
- Story 51.03's spec still names `loops-full-home.txt` (as written when the story shipped); `docs/update-rehearsal.md` describes today's check.
