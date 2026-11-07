---
schema: 1
kind: pr
pr: 897
title: The home list's LAN entries, by name and by address pinned to the home's name
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [892]
---

## What happened
- Story 50.15 §3 keeps a raw-IP LAN entry for routers that block DNS rebinding. The home already reported its listener's LAN addresses in `META_ENDPOINTS`, but the cloud never stored them, so no app could have used a LAN entry.
- The cloud now keeps the private ones (`servers.direct_lan`, migration 00500010). `GET /v1/homes` lists each one first by name (`local`), then by address (`local_ip`) with `tls_name`, the name the app checks the certificate against.
- `DBDirect` had no test of its own; `TestDBDirectEndpoints` is its first.

## What we learned
- A new connection kind is safe to add: the Apple and Android clients already drop kinds they don't know, so old apps keep working unchanged. Carrying the pin as a separate field (`tls_name`) keeps the URL a plain address that any client can dial.
- A field a frame carries but no store keeps is easy to miss. Reading the frame's struct against the store's columns found it.

## Keep doing
- When a frame or payload gains a field, check the store keeps it and a test reads it back.

## Open
- The Apple and Android clients' use of `local_ip`, and the home settings' `direct.rebinding` notice.
