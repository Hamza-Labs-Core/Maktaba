---
schema: 1
kind: pr
pr: 602
title: maktaba.dev cut to developer guides; rolling security.txt; wiki from the repo
date: 2026-11-03
session: unknown
source: claude-trail.md
prs: [607, 610]
---

## What happened
- Merge 589c4c17a. maktaba.dev was cut down to developer guides (plugins, contributing, security), the API reference and the changelog. The epics stay on GitHub, and `/wiki/` redirected there.
- security.txt's `Expires` rolls to 11 months after each build, and a monthly deploy keeps it fresh.
- The wiki generator reads the repo's own specs, and a CI check keeps its output in step. Linear links are gone from everything served.
