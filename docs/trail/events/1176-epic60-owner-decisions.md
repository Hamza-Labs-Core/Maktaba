---
schema: 1
kind: pr
pr: 1176
title: "Epic 60: the owner questions Q1–Q9 recorded as decided"
date: 2026-11-11
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1176 merged as c412f8c48. Epic 60's §12 table now reads "Decided": several taglines in
  English and Arabic, one chosen (Q1); the owner is the only reader who approves the Arabic
  (Q2); the app icon with the name in both scripts, shown in the mockups (Q3); Q4–Q9 as their
  defaults. Story 60.02's Arabic review step now matches Q2.
- The wiki database keeps only each story's opening excerpt, so no Arabic changed;
  `generate_wiki.py --check` passed.
- `lint / repo` was cancelled at its 10-minute limit twice on this pull request (once on the
  first run, again on the re-run). #1177 raised the limit; main was merged in and the run passed.
- The lead's progress note gave a head sha for #1176 before reading it, and the sha was wrong.

## What we learned
- A re-run of a job uses the pull request's own copy of the workflow: a limit raised on main does
  not reach the pull request until main is merged into it.
- Q3 settles the Arabic name on the site: «مكتبة».

## Keep doing
- Read a pull request's head sha from `gh pr view` before reporting it.

## Stop doing
- Re-running a job cancelled by its own time limit without first raising the limit.

## Open
- Part A of the design (#1163) still needs the owner's approval of the look and the voice guide,
  and the choice of font name.
- Epic 59's Arabic install guides write the name in Latin; they move to «مكتبة».
