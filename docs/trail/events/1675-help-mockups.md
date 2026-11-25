---
schema: 1
kind: pr
pr: 1675
title: "The website's help mockups list the topics there are: main's unit job was red"
date: 2026-11-24
session: 76b80a46-8389-4697-ae6f-d431f462e736
agent: claude-code
---

## What happened
- #1653 took a topic out of the help index. The mockups drawn from it (`help.en.html`,
  `help.ar.html`) were not made again, and main's `unit / unit` failed on `mockups.test.mjs`, as
  did #1615's and #1628's with that main brought in. #1675 runs
  `node web/packages/website/design/mockups/make.mjs` and commits the two files.
- A second pull request made the same two files (#1672, closed, with its trail pull request #1674).

## What we learned
- The website's mockups are generated from the help index: a topic's change needs `make.mjs` run.
- #1653's own checks were green: the check that the mockups are current did not run for a change
  of the help index (#1694 changes that).
- A re-run of a failed job keeps the run's old merge commit, so a fix on main reaches an open pull
  request only with a new push.

## Keep doing
- Checking whether main itself fails before looking for the fault in a branch.

## Stop doing
- Reading a cancelled run on main as a passing one.

## Open
- The mockups' rendered PNGs date from 2026-11-11 and are not compared by CI: they can differ from
  the HTML.
