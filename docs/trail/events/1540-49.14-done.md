---
schema: 1
kind: pr
pr: 1540
title: "Story 49.14 done: AI mode's tab in every client, built as four parallel PRs"
date: 2026-11-23
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
prs: [1540, 1529, 1531, 1533, 1535]
---

## What happened
- Story 49.14 was built by four child agents at once, one PR per client:
  - #1533: web, big screen and `/tv/`;
  - #1529: Apple Player and Apple TV;
  - #1531: Android, Android TV and desktop;
  - #1535: Roku.
- None of them edited the changelogs, the spec, the status page or the wiki. Each reported
  its changelog entry and As built text, and #1540 adds them all at once. Unlike 49.11's four
  PRs, these never conflicted with each other over those lines.
- Each PR had one GLM-5.3 review. The lead fixed every finding in its PR. The details are
  in each PR's own event (1529, 1531, 1533 and 1535).
- Roku shows the answer whole, once written. None of its network calls can stream a POST
  over TLS.

## What we learned
- Leaving the shared lines to one closing PR is cheaper than stacking the PRs.
- A review is worth running against code that already passes CI. Every part was green, and
  every review still found real defects: a refusal shown under the wrong question, a token
  refresh replayed by two Tasks, a bearer 401 never renewed, Results wiped by an AI
  question.

## Keep doing
- Brief parallel children to report their changelog and As built text instead of editing
  the shared files.

## Open
- Roku streaming. Asking a follow-up on Apple TV has no UI test.
- maktaba.dev has no Apple AI mode screenshots yet. AI mode sends no filters.
