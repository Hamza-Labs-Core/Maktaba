# Story 33.6 — Staging preview for admins

## Description

*As an admin, I want to watch a staged change before it goes live — the new channel, the new look,
the new encoder settings — in my browser, and optionally in a test Plex.*

Design: [design.md](design.md) §7.

## Acceptance criteria

- **AC1** `POST /api/tv/changes/{id}/preview` (admin) returns `{hls_base, m3u, xmltv, expires_at}` signed
  for 1 h; the URLs are rejected after expiry or for a change not in `verifying`/`ready`/`approved`.
- **AC2** `/stream/staging/channel/{id}/index.m3u8` on the web port requires an admin session and serves
  the staging slot's HLS for that channel (warming it within the budget if needed); a non-admin gets 403.
- **AC3** `/staging/lineup.m3u` and `/staging/xmltv.xml` render the staged lineup and generation N+1, so
  VLC can browse the staged lineup.
- **AC4** Preview viewers count as demand for the staging track only: they never keep a live channel
  warm and never trigger a push.
- **AC5** With `preview_hdhr = true`, a second HDHomeRun device ("Maktaba TV (staging)", own DeviceID,
  port 8782) is advertised by SSDP and serves the staged lineup; it is off by default and stops when
  no change is in flight.
- **AC6** The Changes page's Preview tab plays a channel with the web player and shows the staged guide
  strip for it (33.11).

## Test cases

- **TC1** `TestPreviewTokenExpiry`.
- **TC2** `TestStagingGateAdminOnly` (web port gate).
- **TC3** `TestStagingXMLTVUsesStagedGen`.
- **TC4** `TestPreviewNotLiveDemand`.
- **TC5** `TestPreviewHDHRDevice` — distinct DeviceID, lineup from the release.

## Edge cases

- **EC1** Preview of a channel the change removes: served from live (labelled "removed in this change").
- **EC2** Budget exhausted: preview answers 503 with `Retry-After` and the reason.

## Dependencies

33.5.
