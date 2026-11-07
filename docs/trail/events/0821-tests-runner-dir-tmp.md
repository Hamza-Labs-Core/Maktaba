---
schema: 1
kind: pr
pr: 821
title: Plugin runner areas go to /tmp in tests, never the checkout
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [789]
---

## What happened
- `pipeline/run/runners/areas.json` had been committed with wave 3 (#789). It held a test run's areas, with this machine's paths (no secrets). It is removed, `pipeline/run/` is ignored, and the pipeline tests point `MAKTABA_RUNNER_DIR` at a short session folder under `/tmp`.

## What we learned
- A default path that is relative to the data folder (`MAKTABA_DATA_DIR`, else `.`) becomes the checkout when a test leaves the variable unset. Every new environment variable a worker writes under needs an autouse test fixture that points it at a temporary folder, like `MAKTABA_SECRETS_KEY` and `MAKTABA_NODE_CONFIG`.
- Look at `git status` after a full test run before committing everything with `git add -A`. A file the tests wrote shows up there.
- A unix socket path must stay under 100 bytes, so a runner folder for tests goes under `/tmp`, not pytest's temp folders.

## Open
- None.
