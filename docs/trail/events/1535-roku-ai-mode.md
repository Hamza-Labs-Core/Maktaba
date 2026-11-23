---
schema: 1
kind: pr
pr: 1535
title: "Story 49.14 on Roku: Search's AI mode tab, the answer read once the home has written it"
date: 2026-11-22
session: 5c92d0a6-25db-4bf8-9419-20e3094b7067
agent: claude-code
prs: [1519]
---

## What happened
- #1535 adds the AI mode tab to Roku's Search (`apps/tv/roku`), on the API of story 49.12. It is
  one of four parallel PRs for 49.14 (web and big screen, Apple, Kotlin, Roku). The CHANGELOGs,
  the spec and the epic status are left to one closing PR.
- Two tabs, Results and AI mode, head the right column of 49.11's screen (#1519). AI mode runs
  on a second `ApiTask` (`m.global.aiApi`: 5 minute timeout, a `follow` flag that takes the
  scene's tokens before each call, a `cancel` field that drops the call with that tag), so the
  shared Task never waits on an answer. The logic is in `source/ai.brs`: an SSE reader, the
  answer's state, bodies, refusals, and the passage lines. The new screen test is T08.
- **No streaming on Roku.** `roUrlTransfer` gives a response body only whole.
  `AsyncGetSSEvents` (Roku OS 16, and in brs-engine 2.6.2) streams `roSSEvent`s but sends no
  request body, and `POST /api/search/ai` needs one. `roStreamSocket` has no TLS, and homes are
  reached at https names. So the channel reads the stream when it ends, runs the same events,
  and shows the whole answer at once. The queue position is never shown while waiting.
- Problems hit and fixed on the branch:
  - `tab` is a reserved word in BrightScript (bsc BS1045). A regex rename of the parameter
    also renamed `m.tab` and a log string, and Down from the tabs went to Results' parts. T08
    caught it, and both were restored by hand.
  - The answer's line height was too tight: 42 px a line for a 32 px font overlapped the label
    below. Now 48 px.
  - In a passage row, "[n]  Title · 3:12" carried the time to the far side of an Arabic title,
    because a number after Arabic letters joins the right-to-left run. The time now comes
    before the title.
  - T08 pressed Left after New topic, but New topic hides itself and moves the focus to Ask,
    so the Left reached the keyboard and the next OK typed a letter. The test now waits for
    the focus line and presses nothing.
  - The first failed runs wrote `search-ai-en.png` references from broken screens (a missing
    reference is written, and the test passes). They were deleted before the final run.
  - CI's first run (job 137480431013) failed T03 at the library grid, and T04 after it. The
    second page of the grid arrived after the focus had reached the first page's last row,
    and the focus went back to 0. T07 and T08 passed. The second ApiTask ran from launch, and
    brs-engine's `Wait` in a Task polls every 10 ms, so every screen test carried a second
    polling thread. Now MainScene creates the Task without running it, and SearchScreen runs
    it at the first question (33df43004). `ApiTask` sends a call it was given before it
    started, and never sends the same node twice.

## What we learned
- brs-engine's `roUrlTransfer` writes `AsyncPostFromFileToFile`'s output only on 200, so a
  problem body (409's `code`) is lost on that path. `AsyncPostFromString` with
  `RetainBodyOnError` keeps it.
- Before a screen test passes, delete any reference a failing run wrote: `compare` writes a
  missing one and the next run measures against it.
- An `UPDATE_SCREENS=1` run rewrites every reference it passes, including ones byte-different
  but visually the same (`account-*`, `code-en`). Restore those with `git checkout`.
- A Task waiting in brs-engine is not free: its `Wait` polls (`Atomics.wait` for 10 ms a
  turn). Start a Task when it is first needed, not at launch.
- Rename a BrightScript identifier with a pattern that cannot match a member (`m.tab`) or text
  inside a string.

## Keep doing
- Keep a client's AI logic apart from its screen (`source/ai.brs`), as 49.11 did, so the
  reader is tested at every split point and a streaming transport would change only the Task.
- Give a long call its own Task instead of raising the shared Task's timeout.

## Open
- Streaming on Roku. It needs `AsyncGetSSEvents` with a request body, a GET form of the API, or
  plain-http homes over `roStreamSocket`. Until then the answer appears whole.
- The cancel path (asking again or leaving while an answer is read) has no screen test. In
  brs-engine the mock's answer completes before a second key arrives.
- If the AI Task and the main Task both refresh an expired token at the same moment, one
  refresh can be refused. The window is small and not tested.
- On a device, check the tab widths in Arabic and the answer's wrap at 58 characters.
