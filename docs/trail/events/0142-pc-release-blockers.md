---
schema: 1
kind: pr
pr: 142
title: Release workflow blockers after v0.2.0
date: 2026-10-20
session: e7913aa5
source: claude-trail.md
---

## What happened
Every tag up to v0.2.0 had failed in several release legs. #142 (rust-ini pin, Android SDK and
signing, relay deploy gate, native web bundle) fixed each failure it reached as it got further,
merged main after Maktaba.app's own icons landed, and matched the Windows binary name
`Maktaba.exe` (`mainBinaryName`). It merged 2026-10-21.
