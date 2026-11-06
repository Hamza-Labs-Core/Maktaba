---
schema: 1
kind: pr
pr: 459
title: "48.5: small posters for phone grids"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Added `?size=small` on `GET /api/videos/{id}/poster` (`api/internal/handlers/videos/poster.go`, tests in `poster_size_test.go`).
Small is the pipeline's `thumb_small` copy (`<hash>/small.jpg`, then the import's staged `imported/<id>.jpg`), else the full chain. An unknown `size` is a 400.

## What we learned
- Decided: the API generates nothing. The pipeline makes each size once and keeps it in the thumbnail cache, and both sizes keep `Cache-Control: private, max-age=3600`.
- Decided: the spec asks 404 for a viewer without the library, so the poster route now answers a signed-in viewer without access exactly as a missing video (it said 403 before). No one signed in still gets 403. The other video routes still say 403.
- The query reads `library_id` without `::text`, so the handler runs on the SQLite test table too (lib/pq scans a uuid into a string either way, as `trickplay.go` does).
