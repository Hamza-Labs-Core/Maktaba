---
schema: 1
kind: pr
pr: 906
title: The home's settings say when the router blocks its LAN name
date: 2026-11-07
session: c0d50e69-00c4-47e2-9b33-1ac69218ae34
prs: [897, 903]
---

## What happened
- Story 50.15 §3's last piece. The home's `/api/direct` lists its LAN names (`lan_urls`) once its listener has a certificate.
- The Direct access card, opened from the home's network, tries each name from the browser with a `no-cors` fetch of `/api/health` (4 s). When none answers it shows `direct.rebinding`.

## What we learned
- The spec's "the LAN name fails but the raw IP works" needs no IP test in the browser. The page itself came over the LAN address, which proves it. A browser couldn't test the address anyway: it can't check a certificate against a name other than the URL's.
- A `no-cors` fetch tells "answers" from "doesn't" without any CORS headers on the home: an opaque response resolves, and a DNS refusal, refused connection or bad certificate rejects.

## Keep doing
- When a check can't be made directly, look for what the context already proves (here: how the page was reached).

## Open
- The probe can't tell a blocked name from a LAN firewall on the listener's port. Both mean apps fall back to the address, so the notice's advice holds either way.
