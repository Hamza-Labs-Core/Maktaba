---
schema: 1
kind: pr
pr: 1683
title: "SRC-60's flake was a product race: the URL is rendered in a transition, after the results it names"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- SRC-60 (`Search.ai.test.tsx`) failed on CI with one `POST /api/search` too many (issue #1678,
  job 138626548771). 85ac326fe had made the test wait for the results; the head had it.
- The router renders a URL change in a React transition, after the results are painted. Choosing
  AI mode in between wrote the rendered params, with no `sid`, so coming back searched again.
- A second bug, with no timing luck needed: a search that answered after AI mode was opened wrote
  the URL it was asked with, and put the person back on Results.
- #1683 keeps the URL as the page last wrote it, taken again from each location the router
  renders, and every change starts from it. Under load main failed 6 of 140 with CI's assertion;
  the new code 0 of 140.
- **The first head (895fb260c) had a regression main does not have.** It read "the rendered URL
  lacks the results' `sid`" as "the URL is late". In AI mode the URL can have moved to another
  search (a recent search, Back), and choosing Results rewrote it to the earlier one. The
  independent review found it, with no timing involved. SRC-60d checks it in English and Arabic.
- The same change settled two races main has: new words followed at once by AI mode asked the old
  words, and a search answering after the URL moved on rewrote it.

## What we learned
- `setParams((prev) => …)` gives the params of the render it was made in, not the latest URL. In a
  callback kept over an await it is older still.
- A test can act inside the gap: a `MutationObserver` that clicks as the results appear runs
  before the transition renders.

- The first fix passed every test written for the two races. Its tests never left the one search.

## Stop doing
- Working out where the URL is from other state (what is on screen, what ran last). Keep the URL.
- Making a test wait past a race it found, before asking whether a person can be in it.

## Open
- The big screen's Search has its own code and was not looked at for the same.
