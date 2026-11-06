---
schema: 1
kind: decision
pr: 648
title: Epics 50, 52, 53 and 54 specs build on Epic 51 as built
date: 2026-11-04
session: unknown
source: claude-trail.md
prs: [608, 635]
---

## What happened
- Merge d6d648c41. The specs for Epics 50, 52, 53 and 54 build on Epic 51 as built (#608, #635), with its real component ids, routes and migration slots.
- New stories: 50.35 (remote access is on when any remote path is on), 52.16 (word review and the language packs in the component catalog), 52.17 (a component a later release adds starts as its bundle says), 53.18 (Epic 53's parts in the catalog), 54.37 and 54.38 (plugin entries in the catalog, and plugin files).
- Epic 54's packs, manifest, runner and layout stories use Epic 51's signed components index, and broadcasting is gated by the `broadcast` licence flag (51.19).
- Epic 51 holds slots 0178-0181, #634 holds 0182 and #628 holds 0183, so the next free slot was 0184.
- Every changed entry has its Arabic, and `generate_wiki.py --check` reports 1363 of 1363 entries.
