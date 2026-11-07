---
schema: 1
kind: pr
pr: 691
title: Epic 53 stories 53.14 and 53.15 — report a problem, crash reports to the home
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The home takes crash envelopes, refusing any without the device's consent, and groups them as errors.
- It takes problem reports, adds its own side from the log index, and notifies admins. An inbox for admins and "My reports" for senders.
- The web app's switch, asked once and off by default; Report a problem; "Report this" on error screens.

## What we learned
- A web error's `describeThrown` text carries every frame, and frames from different call sites differ. A once-per-page fingerprint should take the message line and the first frame's function, with numbers folded.
- A per-person switch can live on the device when the server checks a consent header on every send. That avoided a migration while two other epics were claiming slots.
- A page that loads names in a separate call must be tested with `findBy`, not `getBy`, right after its first list renders. Otherwise the test passes or fails on which call answers first.

## Open
- Native crash capture and forms (Apple, Android, TV, desktop); 53.16 sharing with Maktaba; `docs/app-privacy.md`.
