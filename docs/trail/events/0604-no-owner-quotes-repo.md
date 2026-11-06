---
schema: 1
kind: pr
pr: 604
title: Repo text states requirements neutrally, no owner quotes
date: 2026-11-03
session: unknown
source: claude-trail.md
prs: [603]
---

## What happened
- Merge 6827015f8. About 330 owner quotes across specs, wiki and docs, and about 170 in code, migration, app, pipeline, tool and workflow comments, were replaced with neutral statements of the requirement or decision, in the repo and on maktaba.dev.
- "Owner" as a role (user stories, pending decisions, the home's owner, file and DB owners) stays. CLAUDE.md rule 5 now forbids quoting the owner in repo text.
- Old git history and old PR bodies still hold the quotes; history was not rewritten.
- #604 needed four merges of main and waited for the stall-test (#612) and VideoScreen (#613) root fixes.

## Keep doing
- Never quote or attribute the owner's words in repo text; state the requirement neutrally.
