---
schema: 1
kind: pr
pr: 1467
title: "Main's two flaky unit tests, fixed at their causes"
date: 2026-11-19
session: 395164eb-55d3-4bf9-b42a-abb243856174
agent: claude-code
---

## What happened
Main's CI failed on 19f9c90eb, 9e4fdcfbf and 56fe43aae, and PRs that ran every job (#1449)
failed the same way, on two unit tests.

- `api/internal/system` `TestFetchReleasesAllocatesLessThanTheList` (from 450cdf619, not the
  advisory paging in a14a9fc74) decoded a 7.1 MB releases list under `-race`. That took about
  1.2 s on a CI runner, right at the unit tier's hard cap (3 × the 400 ms soft cap,
  `tools/test-budget.sh`). It now uses 30 releases of 30 assets (1.5 MB, 0.28 s) with the same
  bound, plus a control: decoding the same list whole has to exceed that bound.
- `web/src/pages/Parental/Activity.tsx` showed a first page's rows for one commit without its
  Load more button, because an effect copied the page's cursor one render late. A test that
  found the rows and then read the button synchronously sometimes landed in that gap. The
  later pages are now kept in state tagged with the first page they follow and derived in the
  same render.

## What we learned
- A test near the hard cap fails on runner load and nothing else. Re-running it alone doesn't
  save it when the test itself is the slow part: it was 1.21 s alone on CI.
- To shrink a fixture without weakening a ratio test, add a control showing the bound still
  separates the good reading from the bad one at the new size. Here that's 31% against 455%
  of the list.
- An effect that copies server data into state (`setCursor(data.next)`) leaves one commit
  where the UI disagrees with the data. Derive it in render, keyed by what it depends on.
- A MutationObserver catches that gap deterministically: its callbacks run as microtasks after
  a DOM commit, before React's passive effects (a macrotask outside `act`). The new test
  failed 3 of 3 runs against the old page.

## Keep doing
- Fix a flaky test by finding its cause and proving the fix with a test that fails on the old
  code. Don't raise a budget or add retries.
