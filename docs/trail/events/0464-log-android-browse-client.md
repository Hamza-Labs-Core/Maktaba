---
schema: 1
kind: pr
pr: 464
title: "48.8: the Android client's browse calls"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Added the Android client's browse calls (`apps/android/client/…/core/api/Browse.kt`): Home, recommendations, libraries and pins, hub, grid, facets, series, a video's page, marks, search.
The Profile routes were left out (story 48.31's).

## What we learned
- Decided: calls are `ApiClient` extensions in their own file. For that, `ApiClient.get`/`decode`/`send` went from `private` to `internal`; 48.9 needs the same edit.
- A 404 is `NotAvailable`, a new `ApiException` subclass whose cause is the `Status`, so `errorKind()` and Details work without touching `HomeError.kt`.
- Lists decode through `LenientList`, which drops elements it cannot read.
- Fixtures are hand-made from the Go structs, not recorded from a branch stack. To keep them honest, `api/internal/handlers/browse/native_fixtures_test.go` decodes each one into the handlers' types with unknown fields refused.

## Keep doing
- Check client fixtures against the Go handler types with unknown fields refused, so a renamed field fails in the PR that renames it.
