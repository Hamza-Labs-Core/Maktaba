---
schema: 1
kind: incident
title: CodeQL's bundle download failed with HTTP 504 and held every PR for about an hour
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [1000, 1004]
---

## What happened
- From about 09:15 UTC GitHub release downloads, the CodeQL bundle among them, returned HTTP 504 on and off (an unrelated project's release asset too; no incident on githubstatus.com). Each CodeQL default-setup run has five analyses, each downloading the bundle, so most runs had one fail; #1000 and #1004 each failed fail-closed twice.
- Downloads came back steady around 11:00 UTC; both got a fresh head then.

## What we learned
- CodeQL default-setup runs cannot be rerun, as a run or a job (HTTP 403): only a new head commit retriggers them.
- Probing the bundle URL (`curl -r 0-1023`) until several rounds in a row succeed costs nothing; pushing a new head during the outage costs a whole Apple UI run and likely fails again.
