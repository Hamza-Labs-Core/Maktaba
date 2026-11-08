---
schema: 1
kind: decision
title: The Play workflow's native-symbols path is right on AGP 9.4; nothing to change
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [967]
---

## What happened
- The tag runs since #967 (v0.3.103 to v0.3.105) stopped at the crash-symbols token gate (no `MAKTABA_SYMBOLS_TOKEN` secret), so the native step never reached its path.
- A local release build of the player, remote and TV app from main put AGP 9.4's merged libraries at `build/intermediates/merged_native_libs/release/mergeReleaseNativeLibs/out/lib/<abi>/*.so`; the step's recursive `find` under `merged_native_libs/release` finds all 36. Its own loop, run under bash, ends with \"none of the 36 native libraries has symbols (36 without a symbol table)\": three AndroidX prebuilts (`libandroidx.graphics.path`, `libimage_processing_util_jni`, `libsurface_util_jni`) on four ABIs in three apps, stripped by their publisher.

## What we learned
- A step behind a secret's gate can be proven before the secret exists by building its inputs locally and running its own script.
- In zsh an array starts at 1: `${libs[0]}` is empty. The workflow's scripts run in bash, so check them in bash.

## Open
- The first tag after the secret is added shows the notice in a real run.
- Native frames get app-own tables only when the apps build native code.
