---
schema: 1
kind: pr
pr: 1207
title: "maktaba.tv's front door takes the chosen tagline"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1207 merged as eadc8c25d: the Phase 1 front door's headline became the tagline chosen on
  2026-11-12, "Your own library, on every screen at home." / «مكتبتكم، على كل شاشة في البيت.»,
  with a build test. Its eight references (360, 768, 1280 px, en and ar, dark, and 1280 light)
  were taken from CI's own Linux renders (Docker off on the Mac), each looked at beside the mockup.
- It then sat red for about nine hours with nothing wrong: its "changed reference images are
  listed" check showed CANCELLED. A push and a body edit seconds apart had started two runs of the
  check on one head, and the workflow's `cancel-in-progress` group cancelled one; the fail-closed
  merge rule rightly reads CANCELLED as not green. Nobody was watching: the lead had ended its
  turn waiting on CI. Re-running the cancelled run passed; #1211 removed the cause.

## What we learned
- A check that two events can start on the same head must not cancel itself: the cancelled run
  stays on the head as a red mark.
- Ending a turn while waiting on CI leaves nobody to see a red check. Wait in the foreground,
  with a long timeout, and say what is pending.

## Keep doing
- Take references from CI's own renders when Docker is off, and look at each one.

## Stop doing
- Ending a turn on "CI is running".

## Open
- The home page (60.10, #1208) replaces the front door.
