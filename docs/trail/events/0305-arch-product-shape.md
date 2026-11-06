---
schema: 1
kind: decision
pr: 305
title: Product shape - Player, Remote, Server and the service admin
date: 2026-10-25
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Epic 42's spec (#305, `specs/epics/42-apps-player-remote-server/`) set the product shape on
2026-10-25: phone, foldable and tablet apps on Android and iOS, three Player and three Remote.
Desktop Player and desktop Server, and a TV Player. The web is part of the server. A cloud web
service on the VPS acts as a Remote and a Player for a connected home. The Maktaba service admin
is separate from home admins. Player builds are viewer-only, like the TV apps (decided the same day).
