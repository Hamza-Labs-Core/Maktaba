---
schema: 1
kind: pr
pr: 622
title: README opens with apps by platform; one Get the apps section
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge 240e68ff7. The README opens with an apps-by-platform grid, and Native apps and Downloads are merged into one Get the apps section.
- Checking the code and release workflows showed no Mac Remote exists, the iPad Remote (`remote.tablet`) was built by no release or CI job, and the Mac server app is one Apple-silicon `.dmg`. The README, `apps/README.md` and the devportal's architecture and repo-layout pages (English and Arabic) now say so.
