---
schema: 1
kind: pr
pr: 1454
title: "Past the seats, the newest non-admin accounts are read-only (story 16.4)"
date: 2026-11-19
session: 3592ba55-275b-49f6-842b-123bf5ccaec9
agent: claude-code
---

## What happened
- #1454 merged as 56fe43aae. When a home has more users than its licence's seats, admins are
  exempt and the newest accounts beyond the seat count are read-only
  (`subscriptions.SeatGuard`, `api/internal/subscriptions/seats.go`): they watch, keep their
  progress, history, playlists and favourites and everything else under `/api/me`, and are
  refused uploads, metadata edits and managing the home (`403 seat-read-only`). Nothing is
  cached: removing users or adding seats restores them at the next request.
- The gate sits in front of the API router; the GraphQL bridge runs each field's REST
  sub-request under the same gate (`graphql.SubRequestGate`). The admin's licence view names
  the read-only accounts; an account's entitlements say `read_only`.
- Reviews (GLM-5.3) found remote live TV (`POST /api/tv/remote/tune`) refused, GraphQL going
  around the gate, and personal writes (app sign-ins, devices, pinned libraries) refused; all
  fixed. A Kimi review was attempted first and returned nothing: its usage limit was exhausted.

## What we learned
- A write allowlist has to be checked against the clients' real playback calls (web, Apple,
  Android): live TV away from home starts with its own route.
- The GraphQL bridge serves fields as in-process sub-requests on the router: a gate on the
  public mux does not see them unless the bridge applies it.

## Keep doing
- Keep a read-only account's personal state open as one prefix (`/api/me/`) rather than
  route by route.

## Open
- No test classifies every non-GET route as open or refused; a future route under an open
  prefix (`/api/stream/sessions…`, `/api/cast/grants…`) would be born open.
- The admin's over-seats message is English only, like every message of the licence view.
