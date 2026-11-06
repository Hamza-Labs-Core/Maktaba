---
schema: 1
kind: pr
pr: 75
title: Storage check accepts files of unknown size, so the joined PC takes jobs
date: 2026-10-19
session: 89e92223
source: claude-trail.md
---

## What happened
After the PC joined the Mac's home as node `windows-pc`, its worker took no jobs because the
storage check refused files whose size was not known yet. #75 let the check accept them, and
the worker started taking jobs.
