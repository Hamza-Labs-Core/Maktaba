---
schema: 1
kind: pr
pr: 564
title: Android app names carry Maktaba
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge b5fe27d. After the Apple Remote's rejection (#560), the Android Remote's launcher name "Remote" / «ريموت» became "Maktaba Remote" / «ريموت مكتبة».
- The Player's name moved from an English-only `resValue` (Arabic phones showed English) to `res/values*/strings.xml`: "Maktaba Player" / «مشغل مكتبة». TV ("Maktaba" / «مكتبة») and Capacitor ("Maktaba") already passed.
- New `tools/ci/tests/test_android_app_names.py`: every Android app module's manifest label is a `@string` in `values` and every `values-<lang>` (Arabic required), never a `resValue`, starting with Maktaba or holding «مكتبة»; it also checks the Capacitor `appName`. Application ids unchanged.

## What we learned
- A `resValue` app name is English-only; names belong in `res/values*/strings.xml` so they localize.
