---
schema: 1
kind: pr
pr: 1608
title: "Automatic installs wait while the list of releases is stale (catalog_stale)"
date: 2026-11-24
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1592, 1597, 1601]
---

## What happened
- **Research item 4 (B3):** on 2026-11-24 the home's checks had failed for a day, and the scheduler installed v0.3.157 from a frozen catalog.
- **The rule:** a due automatic run now waits with `catalog_stale` when either:
  - the last good read of the list is older than `StaleHoldAfter` (12 h 30 min, two missed checks);
  - or every check so far has failed.
- **While it waits:**
  - it logs `update_held_stale` at warn level;
  - it notifies the admins once a day (`update.held_stale`);
  - the Updates page and the Apple and Android remotes say why.
- **Not held:** an admin's **Update now**, and a home that has never checked (no state).
- **After its review:** the list is read again before it holds an install (at most every 15 minutes), and holds only while that fails; the limit follows the cadence; a time still to come is not fresh; the hold comes after the other blockers and is told once it lasts.

## What we learned
- **Age is not failure.** A rule on how old the last good check is held homes whose checks never failed (asleep, started before the network, one check a day). Try first, hold on the failure.
- **Holding when the state has no `last_check_ok` would have broken every scheduler test.** They seed releases without any check state. The rule holds only when a check has run and failed, or when the last good read is old.
- **#1597 counts failures in a row and alerts, but holds nothing.** This PR builds on main's `LastCheckOK` alone, so the two merge in either order.

## Open
- The pre-run recheck still reads only the target release's own entry. A withdrawn list (the signed feed) is what would tell the home a release was pulled.
