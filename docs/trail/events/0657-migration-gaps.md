---
schema: 1
kind: pr
pr: 657
title: Unused migration slots listed as permanent gaps, and a lint rule for them
date: 2026-11-06
session: t3-delegate-aa865d4e-422b-48fe-b7d8-1a0afef5db09
---

## What happened
- Slots 0176–0177 (#643) were unused on main but not listed as permanent gaps. Checking every slot found 0017–0027 were also never used on main (only an unmerged early branch had files there); both ranges are now in MANIFEST.md and rule 7 of the migrations README.
- A migration-lint rule (`gaps.go`) fails when a slot below the highest has no file and isn't listed.

## What we learned
- A one-off list drifts; a lint rule that derives the gaps from the files is what keeps it true.
