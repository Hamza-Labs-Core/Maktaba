---
schema: 1
kind: pr
pr: 1251
title: "Viewer analytics and broadcast monitoring (story 50.28)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1251 merged as a6051dbf4. The edge writes `play`, `beat`, `switch`, `error`, `blocked` and
  `ingest` events to Analytics Engine (a positional schema shared with the cloud). The cloud's
  worker leader polls them into a `broadcast_monitor` snapshot each minute, rolls each hour into
  `broadcast_usage_hourly` (cloud migration `00500019`, kept 13 months), and checks each day
  against 50.20's metering windows (`broadcast_metering_mismatch`, SEV3).
- `GET /v1/broadcast/analytics` answers only the home's owner (403 for anyone else), folds
  counts under k=5 into Other, and gives Pro guest sharing live viewers and hours only. The
  staff API audits each read. A `maktaba-broadcast` alert group with promtool tests, a runbook,
  a canary probe from GitHub Actions and the witness, a hidden Broadcasting status component,
  and the cloud web Homes → Viewers page in English and Arabic.

## What we learned
- Since 50.18 the cloud sends query arguments in binary, so a `[]byte` argument for a jsonb
  column is refused ("unsupported jsonb version number 123"); pass jsonb as a string. The local
  integration run found it before CI did.
- Analytics read events under both the server id and the owner id while the publisher key was
  unsettled; that would count other homes of the same owner. With #1257, server id only.
- An earlier merge committed conflict markers into both CHANGELOGs; merge-main.sh then can't
  place entries. Rebuild as main's file plus the PR's entry.

## Keep doing
- Settle a shared key (here the publisher) in one PR and make the stack follow it.

## Open
- Owner actions: the Analytics Engine SQL API token and account id, `monthly_budget_usd`,
  `CANARY_PUBLISHER` on the edge, the canary publisher's pass URL in `BROADCAST_PASS_URL` and the
  witness's env, optional OCR, Logpush, `broadcasting_listed` when broadcasting opens.
- The home-side analytics page was deferred until 50.22's Broadcast page exists (#1263).
- 50.25's revocation list should call `broadcast.RecordRevocations`.
