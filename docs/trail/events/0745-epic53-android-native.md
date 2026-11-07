---
schema: 1
kind: pr
pr: 745
title: Epic 53 on Android — Remote diagnostics, crash reports to the home, Report a problem
date: 2026-11-06
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- The Android Remote opens Diagnostics (Errors, Logs, Overview and Storage, the report inbox) from the account menu. The five-place bar has no room on a cover screen.
- The Player, the Remote and the Android TV app ask once per home whether to send crash reports, off until yes. At start, `last-crash.txt` and (Android 11+) the ANRs, native crashes and low-memory kills since the last start become kept crashes. They are sent as scrubbed envelopes only while the switch is on.
- Report a problem, My reports, and Report this on every error state.
- The envelope, the outbox and the report's part are in `client` (portable Kotlin), for the desktop Player too.

## What we learned
- The first Roborazzi shots showed two layout bugs no assertion caught:
  - the range chips overflowed and "90 days" wrapped letter by letter;
  - a fixed tab row cut "Dashboards" and «لوحات المتابعة».
- `FlowRow` and a scrollable tab row fixed them. Looking at every recorded reference is what found them.
- `InputStream.readNBytes` needs API 33 (lint `NewApi`). An ANR trace is read with a plain loop instead.
- A native crash's `traceInputStream` is a binary tombstone, not text, so only an ANR's trace is kept.
- Keeping the question off under Robolectric (`Build.FINGERPRINT == "robolectric"`) left every existing screen test and reference unchanged.

## Open
- The desktop Player and the Tauri shell follow in their own PR.
