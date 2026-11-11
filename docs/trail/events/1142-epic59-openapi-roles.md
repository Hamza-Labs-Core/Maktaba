---
schema: 1
kind: pr
pr: 1142
title: "Epic 59 story 59.16: the admin routes in OpenAPI (en, ar), a role table and a route walk"
date: 2026-11-11
session: 92c52017-5155-48b6-b2b2-266188b710b6
---

## What happened
- #1142 (merged at c5b5f79c5) is Epic 59's route checklist. The routes were read from the
  merged PRs' diffs (#1089, #1092, #1096, #1109, #1110, #1112, #1119, #1121, #1135, then #1138
  and #1140 as they landed): none of Epic 59's own routes was in `shared/api/openapi.json`.
  They are now, in English (json, yaml) and Arabic (`openapi.ar.json`): This machine, speech
  to text, server.toml with `trusted_proxies`, a library's access and its check, the three Run
  now routes, plugin isolation, `GET /api/system/updates` with its admin-only `container`
  block; and the fields and errors (library `access`, `quiet_at`, `software_only`, 409
  `library_read_only` and `library_no_access`, 503 `transcode_busy`).
- Each admin operation says `x-maktaba-role: admin` (the new ones, #1140's home shares, and
  the older operations whose 403 said "Admin only.").
- `api/internal/router/openapi_roles_test.go`: every `/api` route the home mounts is described
  or on `testdata/openapi_undocumented.txt` (374 older routes; the list only shrinks); every
  operation marked admin runs through the auth gate as no one (401), a viewer, a viewer with
  the library, a viewer's token, an admin's cloud play sign-in (403 each), an admin and a cloud
  manage sign-in (past the check); each is placed `home` for a full node.
- Found by the checklist: `PUT /api/server/machine` forwarded only `{"tier": ...}`, so the
  web's quiet hours and data disk saves sent an empty tier the supervisor refused (422). It
  now forwards each field sent and audits `machine-changed`. The isolation consent's audit row
  gained the level.
- Found by the role table after #1147 merged: `POST /api/videos/{id}/process` and
  `/reprocess` (marked admin) read the video before the role check, so a non-admin got 500 on
  a database error and 404 for a missing video. The role is checked first now.
- Story 59.16's string `library.not_available_here` (en, ar) is the web's one reason a
  non-admin reads for a library's refusal (`libraryRefusalText`, `libraryAccessErrorText`,
  `errorText` with `admin: false`); an admin keeps the read-only reason and its fix. Every
  action a library's access refuses is an admin's, so no viewer screen offers one today.

## What we learned
- A role check driven by the description (`x-maktaba-role`) covers every admin route with no
  test to write per route, and it found a real ordering bug in a PR merged the same day.
- A handler that loads its object before checking the role leaks existence (404 vs 403) and
  turns a database error into a 500 for callers who should get 403. Check the role first.
- A UI field (quiet hours) can ship with a server route that drops it: the web, the API and the
  supervisor each had tests, none across the three.
- `openapi.yaml` dumped by PyYAML with `width=1000` round-trips the file byte for byte until
  someone edits it by hand; edits by script keep it that way.

## Keep doing
- Read the routes from the merged diffs, not from the story's list: half of the story's route
  names (access-check, settings/schedule, isolation-consent) were not what shipped.
- Re-apply scripted spec edits on top of main's file at a conflict instead of merging JSON
  hunks by hand.

## Stop doing
- Marking a route admin-only in OpenAPI without a test that calls it as a non-admin.

## Open
- `testdata/openapi_undocumented.txt` lists 374 routes OpenAPI does not describe, among them
  the streaming routes, which the description has under stale paths (`/sessions`, not
  `/api/stream/sessions`).
- 59.16 is still on #1137's `PENDING` list in `web/src/i18n/epic59.test.ts`; it comes off once
  #1137 merges (its key is in both catalogs).
- `TestAJobThatKeepsFailing` (supervisor) failed once in integration on #1142 and passed on
  the re-run: it reads the snapshot when the state turns failed, possibly before the failed
  event is recorded.
