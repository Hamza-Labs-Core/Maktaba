---
schema: 1
kind: pr
pr: 1400
title: "Watch a broadcast on Apple TV, iPhone, iPad and the Mac (50.27 T03, T05)"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1400 merged as b60f4152e. Apple TV has "Watch a broadcast" on the home picker and in
  Settings: a guest types the publisher's handle and an `MK-` code, or scans a QR code with a
  phone and checks the 4-character code; then channels with now and next, and a player where up
  and down change channel. The Player on iPhone, iPad and the Mac does the same, opened from the
  picker or an invite link (pasted, `maktaba://watch/i/…`, or scanned). Passes are kept in the
  Keychain with Remove; playback goes through the loopback `StreamProxy` (#1370), which puts the
  newest viewer token on each request, so the 10-minute token refreshes without a restart.
- The edge and the apps map edge answers to the same guest states from
  `shared/broadcast/viewer-states.json`; an edge role test checks a pass opens only its own
  publisher's invite channels. Every string ships in English and Arabic.
- The review fixed two things: guest passes are stored `AfterFirstUnlockThisDeviceOnly` (a pass
  counts as one of its invite's devices, so a backup must not carry it to another device), and
  invite links are parsed strictly (watch.maktaba.tv or `maktaba://watch`, path exactly
  `/i/<token>`, no user info or port, token shape `mkbn1.<b64url>.<b64url>`, at most 4 KiB).
- CI on the branch: an iPad row button highlighted but never ran its action until the rows
  used the list's button style; an error row's accessibility label was its icon's name.

## What we learned
- Parental limits can't be a server rule for guest watching: a guest has no home account and
  the edge never learns who watches, so a "limited" flag from the app would be the app's own
  claim. Hiding the entry on limited accounts and devices is a convenience; the device's own
  restrictions are the boundary. A real rule would need rated channels the edge can refuse on.
- The affected-tests coverage map is built on main from xccov runs; a new UI test links its
  app file only after it runs on main, so a `No-Test-Change` trailer is right for that push.

## Open
- Android and Android TV (T04, T06), the desktop Player for Windows and Linux, the Report
  button in the apps (native apps can't produce a Turnstile token yet), universal links (the
  edge must serve the association file) and the remote switch to hide the entry.
- #1400 changed broadcast code; the nightly on main (run 43707283540, b60f4152e) gates the next
  tag.
