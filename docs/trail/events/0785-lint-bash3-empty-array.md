---
schema: 1
kind: pr
pr: 785
title: cloud-expand-only passes with no new migrations under macOS's bash 3.2
date: 2026-11-07
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
prs: [780]
---

## What happened
- Running the whole `lint / repo` set locally for #780, `tools/lint/cloud-expand-only.sh` failed on the Mac with `files[@]: unbound variable`. With no new cloud migrations the file list is empty, and macOS's bash 3.2 treats `"${files[@]}"` on an empty array as unset under `set -u`. CI's bash 5 does not, so CI never saw it.
- The loop now expands with `${files[@]+"${files[@]}"}`. The test has a no-files case that fails against the old script under `/bin/bash` and passes now.

## What we learned
- Lint scripts run on owners' Macs as well as CI. Under `set -u`, an array that can be empty needs the `${a[@]+"${a[@]}"}` form for bash 3.2.

## Keep doing
- Test lint scripts with `/bin/bash` on the Mac as well as in CI.
