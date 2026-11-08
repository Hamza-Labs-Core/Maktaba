---
schema: 1
kind: pr
pr: 938
title: Rehearsal 16.8 turns the Plex extension on as a real home does
date: 2026-11-08
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
---

## What happened
- Scenario 16.8 (the Plex stages across a rollback and an update) never claimed a job, so its matrix row had been dropped. The rehearsal's seed home had Plex off.
- The first fix waited for the `plex_groups` stage row. The row appears only after the graph sync, but the stage still did not claim: a stage of an extension runs only while the extension's component (`ext.plex`) is on, and seeding the row does not turn it on.
- The scenario now turns Plex on through the admin route (`PUT /api/extensions/plex`), which sets the component the way a real home's Settings page does. The row was restored, 16.8 passed in CI, and #938 merged at baef1a513.

## What we learned
- To make a rehearsal home look like a real one, change it through the same API route the app uses, not with direct table writes. The route also sets the state no one remembers, such as the component flag here.
- A stage row comes from the graph sync after the server starts. A scenario must wait for the row before it queues work.
