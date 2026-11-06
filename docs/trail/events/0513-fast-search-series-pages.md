---
schema: 1
kind: pr
pr: 513
title: Fast search and Series tab pages; sign-in submits with Return
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- #513 (fast search, Series tab pages) merged 3df44b7a1 after origin/main was merged in.
- Failure on the way: `PlayerSearchUITests.testAnArabicQueryIsSentUnchanged`, iPad 1/4. The sign-in helper's tap on Connect came about 1 s after typing a Latin address on the Arabic keyboard and was lost (picker still showing, address in the field with the keyboard, Connect enabled, no progress).
- Fix: `PlayerUITestCase.signIn` submits with the address field's Return (onSubmit runs the same `add`); tapping Connect stays covered by ConnectUITests and PlayerConnectProgressUITests.
- The Player UI shards keep the app's log (`<device>-<shard>-app.log`) next to the xcresult, error-level only at first.
- Added `MockHome.stubPages`, a `seriesListPage` unit test and `testTheSeriesTabPagesToTheEnd`.
- Merged with only known failures owned elsewhere (Libraries audit, Mac View menu, Windows CloudTest #558).

## Open
- The app log in Player UI shards was error-level only; a follow-up agent took the full log (done in #539 via #567).
