---
schema: 1
kind: pr
pr: 1425
title: "The search field lets go of the keyboard before the results swap in, and the other forms let go before they change the screen"
date: 2026-11-15
session: d7be748e-5805-4169-8f18-e8c41dc4df65
agent: claude-code
prs: [1419]
---

## What happened
- #1425 merged as a4074b59d. It answers the open item in the #1419 event: the one stall of
  `PlayerSearchUITests.testATitleOpensItsPage` (job 132841983346, ipad 6/6).
- The job log shows the same signature as #1419. After `Type '\n'` at 32 s, the wait for idle
  ran its full 60 s, and the title tap waited another 60 s. Pushing the video page ended the
  stall: the next idle wait took 3 s. A passed test keeps no screen recording, and the app log
  had nothing about search.
- `SearchScreen.submit` already set `typing = false`. It also started `model.submit` in the
  same main-actor turn, so the swap (suggestions cleared, then a spinner, then the results)
  landed in the same update as the field letting go.
- Fix: `LetGoFirst` (MaktabaKit) holds the change while the field is focused, drops the
  focus, and runs the change from `onChange(of: typing)` once the field has let go. With
  nothing focused, the change runs at once. The Remote's log search uses it too.
- The same pattern (a focused field, then submit, then a swap or push) was fixed by letting
  go first in: cloud sign-in, approve a device, guest Watch, report a problem, the parent
  PIN, and the Remote's machine approval and word review.
- tvOS needed no change: its keyboard is a full-screen modal that closes before submit.
- First push: the iphone 4/6 and 6/6 shards were red. To let go when a result opens, a
  `.simultaneousGesture(TapGesture())` had been added beside each result's NavigationLink.
  On iPhone it swallowed the link's tap: the tap took 1 s, but no page or player opened. On
  iPad it worked. Removed. Submitting already lets go, so a result is reached with the field
  let go.
- `affected-tests` blocked CloudViews, GuestViews and FamilyViews. The guest test now
  guards the channel tap after Watch. A `No-Test-Change` trailer covers the cloud sign-in
  and the PIN save, which no UI test goes past. Final checks: 35 pass, 33 skipped.

## What we learned
- Setting `@FocusState` to false and changing the screen in the same turn is not "letting
  go first". The change has to wait for the focus update (`onChange` of the focus).
- A tap gesture beside a `NavigationLink` in a `List` can block the link on iPhone and not
  on iPad. Only a UI test that checks where the tap led catches it.
- `tapWhenIdle` is in `PlayerUITestCase` now, for any Player UI test.

## Keep doing
- Read the failing job's own step timings (`t = …`) to find the exact step where idle
  stopped, before changing code.
- Check every shard's result, not just the step the change was about: the iPhone-only
  regression showed up only there.

## Open
- A result tapped after refocusing the field to refine still opens its page with the
  keyboard up. There is no way to drop focus before a `NavigationLink(value:)` push without
  the path, which `SearchScreen` does not own.
