---
schema: 1
kind: pr
pr: 706
title: The first trail snapshots, v0.3.83 (full) and v0.3.84
date: 2026-11-06
session: 173ff697-de7d-4a8a-b865-212e91cf10f5
---

## What happened
- origin/main had no `docs/trail/snapshots/` at all, so every v* tag (96, v0.1.0 to v0.3.84) counted as new. Only v0.3.83 and v0.3.84 have `docs/trail/events/` and `tools/trail/trail.py` at their commits; older tags have no events to cite, so they got no snapshot.
- v0.3.83 was written fresh from all 287 events (`built_from: full`), about 100 items, grouped under bold lines inside each section. v0.3.84 carried it forward with #688, #689, #693 and #698. Merged as 254066709 after the lint-only checks passed.

## What we learned
- Every `#N` in a snapshot item must be a PR that has an event (its `pr:` or `prs:`); stray numbers like a TV change or a CodeQL alert must be written without `#`, and an issue only as `issue #N`.
- Reading 287 events whole is about 270 KB; stripping front matter to title, kind and prs plus the sections made it readable in five chunks.
- A later event can settle an earlier one's Open item (#677 made the outbound-host test scan the pipeline that #668 left open): check each Open against the newer events before carrying it.

## Keep doing
- Build snapshots in a scratchpad worktree off origin/main, and run `trail.py check` before pushing.
