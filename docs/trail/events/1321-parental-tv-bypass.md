---
schema: 1
kind: pr
pr: 1321
title: "The new TVs cannot route around parental controls: bypass rows A23–A27 (58.14, the API half)"
date: 2026-11-14
session: 13f121e8-f4e3-4dc2-8e8c-81d703aad903
agent: claude-code
---

## What happened
- #1321 merged as 799783c42. It adds no server code: the home already refused every path
  story 58.14 names. It proves that with five Postgres tests in
  `api/internal/router/p58_parental_tv_pg_test.go`, added to the `parental-bypass` job's `-run`
  pattern and to `tools/ci/parental-bypass-tests.txt`:
  - A23 `TestP58BypassCastGrantNamesItems`: a hidden video's grant answers as missing, a cloud
    sign-in mints none, a video grant reaches nothing else (`cast_scope`), a child's queue grant
    reaches nothing hidden, and a guardian's grant plays what the guardian may see.
  - A24/T05 `TestP58BypassCastGrantAfterTighten`: a queue grant minted at 16+, then the guardian
    tightens to 9+: the next teen item is 404, the old session no longer serves, and the queue no
    longer lists it, while a kid item still opens.
  - A25 `TestP58BypassCloudCodeApprover`: a restricted child approving a cloud code `as_user` a
    guardian or an admin gets 403 `restricted`; its own approval gives a restricted TV signed in
    as the child.
  - A26 `TestP58BypassDeepLinkByID` and A27 `TestP58BypassNoTVOnlyCheck`: the Roku, Fire TV,
    Samsung, LG and phone browser user agents get identical answers for hidden and missing ids,
    the same lists, and the same 403 `parental_time` once time is over.
- The pull request carried a `No-Portal-Change:` trailer: nothing a user or developer sees
  changed.

## What we learned
- Removing the tighten step, or the library block, makes A24 and A27 fail: the tests
  test the rule, not the fixture.
- `TestP57ExistingHome` failed once in a full local rehearsal under load and passed alone three
  times; it passed in CI.

## Keep doing
- Prove a "nothing to build" rule with a test that names the attempt (the A-row), so a later
  change that opens the path fails the bypass job.

## Stop doing
- Nothing.

## Open
- The TV screens (blocked page with Ask a parent, managed line, on `/tv/`, Fire TV / Android TV
  and Roku) follow in the 58.14 screens pull request.
- The Roku player's own time's-up screen is left to 58.13's player work.
