---
schema: 1
kind: pr
pr: 1593
title: "Store galleries (60.14 part 2): every screen captured on the demo library, and the full set composed"
date: 2026-11-24
session: e1c12b82-6431-40f4-b0ae-96e43fdfa26e
agent: claude-code
---

## What happened
- **Captures.** The 142 missing captures (74 screens in English and Arabic) were made by
  three child sessions, on #1565's demo library:
  - Apple (46): the iPhone and iPad Player and Remote, the Mac and Apple TV; a new CI job,
    Remote UI tests (ipad), uploads product-shots-ipad-remote.
  - Android (74 Roborazzi references): the Player and Remote on phone, 7" and 10" tablets,
    and Android TV and Fire TV.
  - Roku and the TV web UI (20): Roku's demo screen tests, and capture-tvweb.mjs for the
    Samsung and LG launchers, uploaded as store-shots-tvweb.

  Their branches were merged into one pull request.
- **Roku's Live TV beat.** The Roku channel has no Live TV screen, so its set leaves that beat
  out, which also keeps it at the 6 shots Roku takes. The story and beats.json say so.
- **The full set.** store-gallery.yml on ci.yml's run 45359873399 (run 45367908678) composed
  all 260 images with 0 missing and 0 problems. One image was wrong all the same: the Apple
  TV's Arabic player shot was black (mean luminance 0.002, against 0.44 in English). The
  test had shot once the stream's segments were fetched, before a frame was drawn. It now
  waits for the screen's luminance to pass 0.08 (head f7138f191). Then ci.yml run
  45369556137 and store-gallery run 45376900146 made the set that was reviewed: 260 images,
  0 missing, 0 problems, and no frame darker than 0.058 in its screen area.
- **The no-women check.** Every image was looked at, on each listing's contact sheet. The
  filled checklist is a comment on #1593. No
  woman or girl is in any picture or name: the stand-in names are Yusuf and Omar, and the
  household is haddad.
- **Reference images.** The pull request carries #1565's commits while #1565 is open, so the
  `Reference images:` check needed #1565's 288 lines in this body too. The stale failed run
  from the push was re-run so every entry ended green.

## What we learned
- **Checks pass a blank picture.** The composer's checks (size, format, alpha, bytes, count)
  pass a black frame. A shot test needs a condition on what is drawn (pixels), not only on
  what was asked for. Look at the composed set before calling it done.
- **Read the stores' "unaltered" sets at full size.** The TV stores take the capture
  unaltered, so a capture's defect goes straight to the store.
- **Some app layouts aren't ready for a store.** The 7" Android Remote's split view (cramped;
  a line wrapping letter by letter; Arabic Needs attention broken) and "Version dev" on the
  LG launcher's code screen are app work to do before these go to the stores.

## Keep doing
- Split a capture job by platform among children on one base branch. Merge their branches
  into one pull request, and compose once from a single CI run that holds every artifact.

## Open
- The owner's approval of the full set, on the page built from the contact sheets.
- Before upload:
  - the 7" Android Remote's layout;
  - LG's "Version dev";
  - the Mac shots, at 1024×674 (the runner's 1× display) scaled up into 2880×1800;
  - Roku's player with no picture (brs-engine).
