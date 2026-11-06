---
schema: 1
kind: pr
pr: 271
title: The cluster updates itself (Epic 35)
date: 2026-10-23
session: 9bd8e01c
source: claude-trail.md
prs: [389]
---

## What happened
Asked why Maktaba did not update by itself, the session built it. #271 (v0.3.0): one update path
per install kind, the home's run drives the nodes, and each machine's journal finishes a run
whichever release starts next.

## What we learned
- Decisions: D1 Ask first (desktop apps stage the home's order and install on a click;
  reversed to no click on 2026-10-27 in #389); D2 a rollback restores the pre-update dump; D3
  the Linux app updates itself without root.
- The order file `updates/app-install.json` goes ordered, staged, go, installing.
- On Windows the app the server starts must break away from the launcher's kill-on-close job.
- Go on Windows ignores `TZ`.
- `go test ... | tail` hides a failing exit code.
- Defender slows tests that start fresh executables.

## Stop doing
- Never raise a test tier's cap.
