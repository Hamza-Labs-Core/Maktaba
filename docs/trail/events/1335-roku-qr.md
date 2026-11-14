---
schema: 1
kind: pr
pr: 1335
title: "Epic 58 story 58.12: the Roku code screen's QR, drawn on the device"
date: 2026-11-14
session: fe74e94d-9f17-4608-8868-2c6bcb57fc86
agent: claude-code
---

## What happened
- #1335 merged as ad13f747e. The Roku code screen shows a QR of the code's `verify_url`:
  - for a cloud code, the cloud's `https://maktaba.tv/tv?c=<code>`;
  - for the home's own code (Find on this network), its `/pair?code=…`.
- How the QR is made:
  - `source/qr.brs` ports the web app's encoder (`web/src/lib/qr.ts`; byte mode, level M,
    versions 1–10) to BrightScript;
  - it runs in `CodeTask`, about 1 s on brs-engine for a 29-module symbol;
  - `components/widgets/QrCode` draws one Rectangle per run of dark modules.
- The placeholder `Poster` that pointed at a cloud `qr.png` is gone, and the story's
  build-plan row for that endpoint now names the device encoder.
- `docs/stores/roku.md` adds two checklist rows:
  - row 30: a cloud-code sign-in an admin approved never acts as an admin
    (`principal.ApplyRestricted`, 58.03);
  - row 31: the QR shows within 2 s on a Roku Express and scans.

## What we learned
- Every TV client draws its QR from `verify_url` on the device: Android's `QrCode.kt`, the
  web app's `qr.ts`, and now Roku. The spec's server-rendered `qr.png` was never built and
  isn't needed: BrightScript is fast enough in a Task thread.
- Porting an encoder is checked best module for module against the original on texts at
  each version's limit. The first run caught one bug the URL alone would have hidden: byte
  mode's character count takes 16 bits from version 10 up, and the port had hardcoded 8.
- A decode closes the loop: OpenCV (`uv run --with opencv-python-headless`) read the QR in
  the channel's own screenshot back as the verify URL.
- brs-node binds ECP on port 8060. When another session's Roku tests hold it, the screen
  tests fail with `EADDRINUSE`. A Docker run gives its own network namespace and also runs
  the Linux-only Find test.

## Keep doing
- When a reference set is regenerated in a container, restore every image except those
  the change touches, so the diff stays reviewable.

## Stop doing
- Specifying a server endpoint for something the other clients already do on the device,
  without checking them first.

## Open
- Real-device rows 30 and 31 in `docs/stores/roku.md`, with the rest of the checklist,
  before the first submission.
