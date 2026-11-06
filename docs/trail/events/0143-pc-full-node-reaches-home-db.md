---
schema: 1
kind: pr
pr: 143
title: A full node's API and pipeline reach the home's database
date: 2026-10-20
session: 798feb8d
source: claude-trail.md
---

## What happened
To run the PC as a full node from source, #143 let its API and pipeline reach the home's
database. Review items (session 2356e2cd) fixed a start-up probe that could hang and home-only
database loops that were running on nodes, then a `make lint-log` failure. #143 merged
2026-10-21.

## What we learned
- The node's database role is limited on purpose, so home-only loops must stay on the home.
- `make lint-log` wants runtime data in log fields, not in the message.
