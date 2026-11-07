---
schema: 1
kind: incident
title: Landing #867, #873 and #883 after main's Core bundle check went green
date: 2026-11-07
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [867, 873, 883, 859]
---

## What happened
- After #859 made `mac-app / Bundle size (Core payload)` green, #867 had main merged in and went fully green. It still failed to merge, because main had moved and both CHANGELOGs conflicted (`DIRTY`). The merge watcher now handles this itself: on `DIRTY` it reruns `tools/dev/merge-main.sh`, pushes, and keeps watching, at most three times. It stops for a person on any other conflict.
- Two Apple Player UI tests failed once each, on #883 (`testFiveSectionsAdaptToTheDevice`) and #867 (`testSignInAndRestoreStatesArabic`). Both failure messages named the simulator's dropped tap (#639). Neither PR touches the Apple apps. One rerun of the failed jobs passed.
- Bringing main into #873 conflicted with main's operator-console split (`3ff8ee3ce`, 50.32 §2). That commit moved `web/cloud/src/{api,i18n,session,ui}` into `src/shared/`. Besides the two conflicted pages, #873's own new files (`providers.ts`, `Turnstile.tsx`, `turnstile.test.tsx`) still imported the old paths. Git can't flag those, because no line conflicted.
- The hand-merged `apps.mjs` entry then failed CI's `prettier --check` (lint / lint).
- Bringing main into #883 conflicted with #873 in `config.go`, `validate.go` and the integration harness. Both sides were kept each time. #873's `setBool(TrustProxyHeaders)` was dropped, because #883 removes that field.

## What we learned
- A PR that goes green can still fail to merge, because main moves while CI runs. A watcher should handle `DIRTY` itself when only the CHANGELOGs conflict, instead of stopping.
- When main moves files, run a typecheck after merging it in. Imports in files the branch added never show as conflicts.
- Run `prettier --check` (and the web lint) after hand-resolving a conflict in `web/`.
- Two branches that each add a test-harness constructor variant conflict. Resolve with one core constructor taking every option, and keep each variant as a thin wrapper.

## Open
- 50.31's rehearsal scenarios T06–T08: #884, to start after #892.
