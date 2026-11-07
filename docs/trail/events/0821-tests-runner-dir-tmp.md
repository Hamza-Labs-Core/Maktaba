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
- `pipeline/run/runners/areas.json` had been committed with wave 3 (#789). It held a test run's areas, with this machine's paths (no secrets). It is removed, `pipeline/run/` is ignored, and each pipeline test points `MAKTABA_RUNNER_DIR` at its own `tmp_path`. The tests that made folders under a literal `/tmp` (the parity harness and two runner tests) now use `tempfile`'s default folder.

## What we learned
- A default path that is relative to the data folder (`MAKTABA_DATA_DIR`, else `.`) becomes the checkout when a test leaves the variable unset. Every new environment variable a worker writes under needs an autouse test fixture that points it at a temporary folder, like `MAKTABA_SECRETS_KEY` and `MAKTABA_NODE_CONFIG`.
- Look at `git status` after a full test run before committing everything with `git add -A`. A file the tests wrote shows up there.
- Never write a literal `/tmp` in a test: it doesn't exist on Windows (CI's `pipeline-cluster-windows` failed with `[WinError 3]`). Use `tmp_path`, or `tempfile`'s default folder where a unix socket path must stay under 100 bytes (about 48 characters on macOS).

## Open
- None.
