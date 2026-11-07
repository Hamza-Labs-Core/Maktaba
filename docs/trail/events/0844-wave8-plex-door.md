---
schema: 1
kind: pr
pr: 844
title: Plex runs as Maktaba's built-in plugin, through the home's service door (Epic 54 wave 8, 54.35)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [823]
---

## What happened
- The five Plex stages moved into `tv.maktaba.plex`, in the `service` runner (no network). Every Plex call goes through the home's service door. On the home that's the plugin API's `trusted/service`; on a joined machine, the worker's channel to the home. The door adds the token and scrubs it. Roles: migration 0208.
- The door's Plex allow-list gained the 13 Live TV calls, for a caller on the home only.
- The dizqueTV stages stayed in the worker, with no body templating in the door. dizqueTV stores programme bodies that carry the token, and a plugin could read them back (issue #827).

## What we learned
- A secret a door adds to a request can come back in the answer when the far side stores what it was sent. Before filling a secret into a body, ask whether the plugin can read that body back through any allowed path.
- A joined machine's Go API has no channel to the home; only the Python worker's agent does. A runner on a node reaches the home through its worker, as a relayed op.
- An extension's stages are tagged `extension` in `job_stages`, so code that tells first-party stages from extension stages by that tag must learn the handover. Two places did: the token mint, and the catalog's plugin rule.
- Capture the exact requests a client sends (a capturing transport) and use them as the allow-list's test cases. The channel map's numbered query keys needed a key-pattern rule the matcher didn't have.
- Per-job work in `run_native` costs every plugin job. Keep it to the stages that need it (an N16 failure under load pointed at `discover()` on every job).
- A stage module that only names its stages through `_fp(...)` has no `StageDef(` text, and the catalog test skipped such files.

## Open
- dizqueTV behind the door, with programmes built on the home: issue #827.
