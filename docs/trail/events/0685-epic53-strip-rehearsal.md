---
schema: 1
kind: pr
pr: 685
title: Epic 53 stories 53.06 and 53.07 — the status strip in every app, fault rehearsal
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The web, Apple and Android apps read `/status.json` and show one strip saying which features are down. The player says when streaming is down.
- The update rehearsal matrix kills the API, stops Postgres, kills streaming and restarts the supervisor (F03, F04, F06, F10), and checks the status, the recovery page and the strip each time.

## What we learned
- Refreshing the status on every failed request changed the API tests' exact fetch counts. Refreshing only while a strip listens keeps a page without one quiet.
- The affected-tests gate maps each native view file to UI suites that never run locally. Views that only mount a component whose logic is unit tested take a `No-Test-Change` trailer naming those tests.

## Keep doing
- Put a fault scenario in the rehearsal matrix that already builds the stack, not in a new workflow.

## Open
- The Apple player's own error sentence when streaming is down (the strip says it for now).
