---
schema: 1
kind: pr
pr: 307
title: A machine gets its database access back by itself
date: 2026-10-25
session: 9bd8e01c
source: claude-trail.md
prs: [276]
---

## What happened
The Mac moved its Postgres without the node's role, and the PC node lost its access to the
home's database. #276 made a machine follow its home's database by itself, and #307 made a
node get its database access back by itself.
