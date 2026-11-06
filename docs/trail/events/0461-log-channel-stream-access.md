---
schema: 1
kind: pr
pr: 461
title: "48.3: a channel's stream follows library access"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Closes G1. `stream_gate.go` asks `GET /api/channels/{id}` (the channels handler's `canRead`, the same rule as the guide's `visibleChannels`) for every non-admin `/stream/channel/{id}/…` and `/stream/via-home/channel/{id}/…`.
It caches allowed and refused verdicts per credential hash and channel for `streamAuthTTL` (30 s).
Added an API role test (`api/internal/handlers/channels/get_access_test.go`). Before it, nothing tested `Get`'s library rule, and the gate now depends on it.

## What we learned
- Decided: 404 and 403 from the API both become a 403, so the gate does not tell which ids exist.
- A 401 from the channel check drops the cached session too. A 5xx is a 502 and is never cached.
- Admins skip the check: the admin's staging `Get` reads draft rows, and admins see everything anyway.

## Keep doing
- A change to the front door needs the update-rehearsal CI green on its exact commit before merge.
