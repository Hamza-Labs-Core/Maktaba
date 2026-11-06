---
schema: 1
kind: decision
pr: 175
title: The Windows server runs from a sign-in task, not a service
date: 2026-10-21
session: 295d4839
source: claude-trail.md
prs: [170]
---

## What happened
The Windows app runs the server from the sign-in task `\Maktaba Server <SID>`, which starts
`Maktaba.exe --server-agent` 30 s after sign-in, with a 5-minute time trigger as the watchdog
(#170, #175).

## What we learned
- A service cannot see `O:` and `Y:`: mapped drives exist only in the user's session.
- Task Scheduler's restart-on-failure never retries a crash, hence the time-trigger watchdog.
