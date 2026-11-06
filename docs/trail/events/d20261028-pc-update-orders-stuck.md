---
schema: 1
kind: incident
title: Update orders waited on a click; the rollback stalled the PC's copy
date: 2026-10-28
session: f43fad60
source: claude-trail.md
---

## What happened
Each update order from the Mac waited for a click in the PC's v0.3.33 app. Three orders came and
none installed, so v0.3.42 was installed by hand. In the Mac's rollback, the PC's copy rebuild
hung 15 min on a table-sync worker with a dead connection. With the Mac back at schema 167, the
v0.3.42 node paused its jobs and streaming.

On 10-31 the Mac was on v0.3.44 and the PC on v0.3.42. The order waited because no app window
ran and the server's `maktaba://install-update` link got Windows' "Get an app" popup: the link
was registered only in Claude's private registry copy. It was handed to session c17dd289
(#456, #457).

## What we learned
- A node newer than its home's schema pauses its jobs and streaming.
