---
schema: 1
kind: pr
pr: 1721
title: "Two specs name the universal Player, not the removed iPad app"
date: 2026-11-25
session: 366f9ce3-e03b-40f4-944e-650e7dafc5b7
agent: claude-code
prs: [1721, 1616]
---

## What happened
- #1616 removed the iPad-only Player. Its review found two specs that still named it:
  `story-56-12-players-parity.md` (T02's test target) and Epic 57's `research.md` (the bundle ID).
  #1721 corrects both lines.

## What we learned
- **The Arabic wiki carries neither line,** so `generate_wiki.py --check` passed with nothing to
  regenerate.
- **A `specs/**` change runs the wiki jobs and no Apple job,** so a wording fix costs no Mac
  runner.

## Keep doing
- Search `specs/` as well as the code when a target or a bundle ID goes.

## Stop doing
- Nothing new.

## Open
- #1706's minors are in the Apple UI tests. They wait for a pull request that runs the Apple jobs
  anyway.
