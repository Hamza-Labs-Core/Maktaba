---
schema: 1
kind: pr
pr: 1353
title: "List the link-recheck test in the tv-clients rehearsal (main was red)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1336 added `TestPGCloudAssertionLinkChangedBeforeRedeem` to the router's tv-clients set.
  #1342 added `TestTVClientsListNamesEveryTest`, which requires `tools/ci/tv-clients-tests.txt`
  to name every test the tv-clients rehearsal runs. Each passed alone; once both were on main,
  `integration (api)`, `tv-clients`, `unit` and `lint` failed on every open PR touching api.
- #1353 added the one missing line and merged as c8ba3f802.

## What we learned
- A list-must-match-the-code check and a new matching test can merge in either order and both
  pass, yet break main together. The same red check on several unrelated PRs points at main, not
  at the branches: run the failing test on a branch's head before sending a fix to each branch.

## Keep doing
- Fix such a collision once, on main, in its own PR, and tell the branch owners to re-merge main
  rather than patching it in each branch.
