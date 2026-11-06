---
schema: 1
kind: pr
pr: 518
title: Servers listen without multipath TCP (CVE-2026-98432)
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
CVE-2026-98432 is a critical Linux IPv4 TCP use-after-free. Its reported exploit needs a multipath TCP listener, which Go 1.24+ opens by default.
Added `godebug multipathtcp=0` to the api, cloud, streaming, maktaba-server and maktaba-launcher `go.mod`s, each with `TestListenersUseNoMultipathTCP` (it fails if the line goes; checked).
There is no `go.work`, so release builds get it.
User advisory in English and Arabic: `docs/security/advisories/2026-11-01-linux-tcp-uaf.md`.

## Open
- The VPS kernel was not patched: left for the owner to patch, or to approve patching.
- Specs say a `SECURITY.md` exists (story 23.8); there was none in the repo.
