---
schema: 1
kind: pr
pr: 655
title: Trail v2 — per-PR events, per-tag snapshots, a skill and a hook
date: 2026-11-05
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- The single trail file (1,623 lines, every trail PR editing it) was replaced by one event file per piece of work in `docs/trail/events/`, a snapshot per tag in `docs/trail/snapshots/`, the `trail` skill, `tools/trail/trail.py` (checker, Missing, Index, snapshot skeleton) and a session hook at Stop, PreCompact, SessionStart and SessionEnd.
- `docs/claude-trail.md` is frozen; its entries became 254 events, written by four parallel agents by line range and attributed to pull requests with `git log -L`, `git blame` and `gh pr view` (files and titles). A three-PR paragraph (#602, #607, #610) split cleanly by matching sentences to each PR's changed files.
- Git corrected a few old facts: #608's migration slots (0178–0181, not 0175–0177), what v0.3.44 carried (#439 and #441), the missing rc.5–rc.7 tags, and the trigger of a stale Mac UI test (#516).

## What we learned
- The trail's value is what the session saw (failed approaches, root causes, corrected rules), not the PR itself; the PR is the anchor that keeps an event focused and lets git find a PR with no event.
- A snapshot can only cite events present at its tag's commit, so the first snapshot comes with the first tag after the events merge.
- A model call in CI was not needed: the agent that tags builds the snapshot from the last one plus the new events, and a periodic full rebuild stops the summary drifting.
- `.claude/` was ignored as a directory, so `.claude/settings.json` and skills could not be tracked; `.claude/*` with negations fixes it.
- With one file per event, events can ship inside the code PR (written while the session remembers) without conflicts; only post-merge learnings need a trail-only PR.

## Keep doing
- Write the event in the PR, before it merges.
- Attribute history with git (blame, `log -L`, PR file lists) instead of guessing.

## Open
- The first snapshot (`docs/trail/snapshots/v0.3.82.md` or the next tag), built with `trail.py skeleton` and `built_from: full`.
