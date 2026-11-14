---
schema: 1
kind: pr
pr: 1309
title: "Apple: a long press taken as a tap, dropped taps and split double taps get one more try; blocked pages read the limit"
date: 2026-11-14
session: b47dc279-403a-4d10-8efc-873b659d9e1a
prs: [1249]
---

## What happened
- Main run 43598615449 failed `PlayerHomeUITests.testNotInterestedRemovesTheCard` even with a 4 s hold: the kept app log had `touch held 4.00 s at (100, 723)`, a touch that ended rather than being cancelled. An opening context menu cancels the touch that opened it, so the menu's long press never began and the lift was the card's tap. A longer hold cannot fix that (1.2, then 2.5, then 4 s never did).
- Fix chosen: re-check and retry, not accessibility custom actions (XCUITest has no public API to perform one, so the menu would go untested). `PlayerUITestCase.openMenu` presses, waits for the menu's item, and if the card's page opened instead goes back and presses once more; a second miss fails naming which. Both of Home's long-press menu tests use it; no other Player or Remote test opens a menu by a long press.
- The same retry, each judged by a deterministic signal, went to the cases the proof runs found:
  - the sign-in's "With a code" tap (`touch held 0.17 s …, cancelled`): judged by `POST /api/pairing/device`;
  - a Watch double tap whose second tap came 4.3 s late (touches end 14.3 s and 18.6 s), which the app rightly took as two single taps: judged by the position still reading where it started;
  - a cancelled PIN key (`touch held 0.18 s at (417, 560), cancelled`): judged by the pad's `pin.dots` count ("n of 6 digits entered", Arabic-Indic digits in Arabic).
- An app bug, found by the proof (run 43606583050, `testTimeUpAtTheStartInArabic`, no tap involved): the time's-up and blocked-video pages took `parental.current` once, as the screen opened. Opened before the store had read the limit, they got `.none`, so no Ask and no PIN, for good. `BlockedPage.outdated(by:)` (MaktabaKit test) says when such a page is made again; both views make it again when the store's limit changes, and the time's-up screen reads an unread limit itself.
- Proof: run 43623455527 on aa927db66 (every Player shard with `-test-iterations 3`, then reverted): 600 iPhone and 600 iPad Player runs, 0 failed; each changed test 3/3 on both.

## What we learned
- A long press that ends normally (not cancelled) means the context menu never began; the touch log's held time and `cancelled` flag tell a lost press from a short one.
- A `@State` built from a store's value in `init` is a snapshot for the view's whole life. A screen that can open before the store has loaded (a deep link straight after sign-in) must rebuild when the value arrives (`onChange` or `.task(id:)`), not trust the first read.
- "Timed out while evaluating UI query" is not always a hung app. The simulator's system log is in the xcresult's diagnostics (`xcresulttool export diagnostics`; read it with `/usr/bin/log show`, since zsh's `log` is a builtin, and `--start` needs `+0000`). In run 43614149749 it showed the app answering and finding `start.brand`, with each element's attributes taking 0.1 to 0.3 s, so XCTest's fixed 30 s query limit passed first.
- The CI simulators' `apsd` retries a push connection about 28 times a second with no interface, in passing and failing windows alike: background load, not a trigger.

## Keep doing
- Give each retry a deterministic signal (a request, a position, a count, a page) so a real failure still fails at once, and fail naming the step on a second miss.
- When a proof run fails, read that iteration's touch log before deciding: a dropped tap gets the retry; anything else gets its real cause found, which here was an app bug.

## Open
- Accessibility custom actions for Home's card menu (for VoiceOver users), separate from the tests.
- A starved simulator can still outrun XCTest's 30 s query limit; if it recurs, look at the runner's load (and `apsd`'s loop) rather than the test.
