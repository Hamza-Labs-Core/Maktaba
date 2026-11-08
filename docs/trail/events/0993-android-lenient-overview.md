---
schema: 1
kind: pr
pr: 993
title: "53.12: the Android Remote decodes the Overview's optional fields leniently"
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [983]
---

## What happened
- A `names` entry that is not text is dropped (that machine shows its node id), and `names` that is not an object counts as no names. `machines.shown/all/offline` drop entries that are not text, `machines.total` and `omitted` fall back to 0, and a `machines` that is not an object counts as absent. Required fields decode as before. Android now matches web and the Apple Remote.
- Per-field serializers (`core/api/Lenient.kt`) read each optional field as raw JSON. A test covers mixed, non-object and missing values through the client and a plain `Json`.

## What we learned
- kotlinx.serialization's `coerceInputValues` turns only a null into the default; a value of the wrong type still fails the whole answer.
- On this Mac the Android tests run with Homebrew's openjdk@17 as `JAVA_HOME`; the Robolectric ones also need `ANDROID_HOME=$HOME/Library/Android/sdk`.
