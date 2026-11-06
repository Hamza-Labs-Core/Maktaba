---
schema: 1
kind: pr
pr: 218
title: One version order everywhere
date: 2026-10-21
session: 454c33cb
source: claude-trail.md
prs: [226]
---

## What happened
A `git describe` build now ranks after the tag it was built from, everywhere (#218), and the
Linux runtime copy uses the same version order (#226).
