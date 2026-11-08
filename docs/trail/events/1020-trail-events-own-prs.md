---
schema: 1
kind: pr
pr: 1020
title: Trail events go in their own trail-only pull requests (CLAUDE.md rule 2, AGENTS.md)
date: 2026-11-08
session: aa4f6da6-177c-478d-81fa-7ab103e7cac3
prs: [944, 907, 1012, 1010, 1013]
---

## What happened
- CLAUDE.md rule 2 and the generated agent-trail block in AGENTS.md said a pull request's event goes "in that pull request, before it merges". The standing rule since 2026-11-02 is that trail updates go in their own pull requests with no CI and no tests. Trail v2 (per-event files, 2026-11-05) wrote the other wording, and nobody reconciled the two.
- Sessions followed the files. #944 shipped its event inside the code pull request. #907's was moved out to #1012 before it merged, and #1010's went to #1013.
- The coordinator first told the Epic 50 delegate that CLAUDE.md was right, then reversed itself and asked the owner. The owner chose separate trail-only pull requests.
- #1020 rewrites rule 2 to say this, with the check `gh pr diff N --name-only` (no `docs/trail/` path) before merging a code pull request. It also puts an override note above the agent-trail block in AGENTS.md, because `trail init` rewrites that block.
- Its merge watcher saw every check pass but its merge call didn't run; the coordinator re-checked and merged by hand.

## What we learned
- When a rule file and a remembered owner instruction disagree, ask the owner before telling delegates which wins. A confident correction that is then reversed costs each delegate two rounds of churn.
- A generated block (the agent-trail markers) can't carry a repository's own rule. Put the override outside the markers, or the next `trail init` undoes it.

## Keep doing
- Checking a code pull request's file list for `docs/trail/` before merging it.

## Open
- agent-trail's generated text still says "in that pull request". An option in `.trail.toml` for trail-only events would remove the need for the override note.
