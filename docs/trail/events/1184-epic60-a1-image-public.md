---
schema: 1
kind: pr
pr: 1184
title: "Epic 60: owner action A1 done, the server image is public"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- The owner made the `maktaba-server` package public on ghcr.io (2026-11-11). An anonymous token
  request answers 200 and its tags list without signing in.
- #1184 merged as 14501fcc8: Epic 60's A1 row reads Done. The daily check of 60.08
  (`image-public.yml`), dispatched on main, passed (run 43139597519): the anonymous pull works on
  amd64 and arm64. Phase 1 no longer waits on A1.
- #1184 and #1183 edited the same rows of the §12 owner-actions table at the same time; #1183
  went conflicting when #1184 merged, and main was merged into it keeping both (A1 Done, A2 the
  shared token).

## What we learned
- Two pull requests that each edit a row of the same spec table conflict even when the rows
  differ, since they sit in one hunk.

## Keep doing
- Let 60.08's release and daily checks guard the image's visibility.

## Stop doing
- Opening a second pull request on a spec table while another one that edits it is open: put
  table edits in one pull request at a time.

## Open
- The other images (streaming, pipeline, caddy, cloud) are still private; that is outside Epic 60.
