---
schema: 1
kind: pr
pr: 1344
title: "Epic 58's spec says what shipped"
date: 2026-11-14
session: 3a1f7bc5-ee1e-4ec2-8980-520aa285af77
agent: claude-code
---

## What happened
- #1344 merged as f98c6333a. Spec text only: the Epic 58 README and stories 58.01–58.12, 58.15
  and 58.18 now match main and the trail events. Fixes:
  - **Paths:** the level check (`api/internal/compat/levelcheck`), the TV build tools
    (`web/tv-build/`), the launcher's `src/platform/`, the cast grant
    (`api/internal/castgrant`), Roku's `.brs` files, and `DeviceCaps.kt` in
    `apps/android/client`.
  - **58.09's sign-in:** the launcher has no cloud code. Its first screen offers saved homes,
    Find on this network, and a typed address. Sign-in is `/tv/`'s, by the home's own code.
    The cloud code, `signIn {grant}`, the cloud's `/v1/tv/*` CORS and the bundled-UI sign-in
    are marked as the unbuilt second part (#1277's Open).
  - **Decisions from events and PRs:**
    - the Samsung emulator and LG simulator are not run;
    - Fire TV / Android TV keep the Epic 57 PIN (README non-goal and §6.7);
    - Amazon releases are staged then committed (no Live App Testing through the API);
    - `accept_queue` is left unforwarded, so a full home answers 503;
    - the maktaba.tv/tv assertion flow;
    - the review home's child account and probe;
    - Roku shaping's open T04 timing.
  - **58.18** is marked waiting on its §12 Q8 start condition.
- 58.17 was left to #1342, which rewrites it. The wiki stores only each story's opening, so
  the only Arabic owed was 58.18's new status line.

## What we learned
- An audit that only greps a spec's backticked paths against the tree finds most path drift
  in minutes. The rest needs the PR bodies' Open and "Not done" sections, which record where
  a story stopped short (58.09's part 2, the Fire TV cloud code).
- Strings tables drift too: the apps' Arabic says الجهاز الرئيسي and مكتبة, and the catalogs
  changed English wording (58.01, 58.09) without the spec following.

## Keep doing
- Check each claim against the file before writing it into a spec. `DV_GOOD_YEARS` is `[2022]`
  on main, not empty as first written.

## Stop doing
- Putting an English note inside a table's Arabic column. It went into a follow-up commit
  before the merge.

## Open
- Two parts were marked not built in the specs: 58.09's second part (the launcher's cloud code)
  and the Fire TV / Android TV cloud code (58.05). 58.09's second part has since merged as
  #1348.
- 58.17's spec was left to #1342, which merged later the same day.
