---
schema: 1
kind: pr
pr: 673
title: After the merge of #673 — e2e tests that read a role's text log, and a keeper close that did not wait
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- #673 makes the supervisor's children log JSON (`MAKTABA_LOG_FORMAT=json`). The replica, two-machine and rehearsal e2e tests looked for `why=refused_moved`, `level=error` and `path=/api/stream/sessions ` in a role's output, and failed on `"why":"refused_moved"`. They now use `logField`/`countField`, which accept either form.
- `Keeper.Close` returned while an hour was still being compressed in the background, which could leave a half-written `.zst` on a stop. The log index's tests saw it as `TempDir RemoveAll cleanup: directory not empty`. Close now waits for the compressions (fixed in #686 and #687, with `TestCloseWaitsForCompression`).

## What we learned
- A log format change reaches every test that greps a process's output, not just the parsers. Search the e2e tests for `key=value` matches before switching formats.
- A flaky TempDir cleanup is worth following to its source: here it was a real shutdown bug.
