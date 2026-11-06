---
schema: 1
kind: pr
pr: 578
title: Apple TV app goes to TestFlight on every tag
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 5e4bd3943. Maktaba TV (com.hamzalabs.maktaba.tv) goes to TestFlight on every v* tag as a third entry in testflight.yml: tvOS archive, the same automatic signing, and fail-fast off so a TV failure leaves Player and Remote alone.
- The TV target now declares ITSAppUsesNonExemptEncryption NO; its icons, Top Shelf images and Arabic name were already in place. `test_testflight.py` keeps all three apps in the matrix.

## Open
- The owner had to create the tvOS app record in App Store Connect (created 2026-11-03, see #583).
