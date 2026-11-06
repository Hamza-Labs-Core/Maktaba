---
schema: 1
kind: decision
title: Two Dependabot alerts dismissed
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [530, 531, 534, 535]
---

## What happened
- The 2026-11-02 sweep took 24 Dependabot alerts to 0 open (#530, #531, #534, #535).
- Dismissed alert #160 glib as `not_used`: tauri 2's GTK3 stack holds glib 0.18, and nothing calls VariantStrIter.
- Dismissed alert #184 web minimatch as `tolerable_risk`: lint-only, and eslint had no minimatch-11 release.

## Open
- Revisit #160 when tauri moves to GTK4 and #184 when eslint adopts minimatch 11.
