---
schema: 1
kind: pr
pr: 1211
title: "CI: the reference-images check never cancels a run"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1211 merged as aeb671047. `reference-images.yml` runs on `opened`, `edited`, `synchronize`
  and `reopened`; a push and a body edit seconds apart start two runs on one head, and its
  concurrency group (`cancel-in-progress: true`, added with the rule in #1160) cancelled one,
  leaving a CANCELLED check beside the green one. #1207 and #1208 sat red for about nine hours on
  that alone. The group is gone (each run reads the body and the files fresh and takes seconds),
  with a comment saying why and a test that the workflow has no concurrency group.

## What we learned
- On a check whose result is the same for every run on a head, a concurrency group only adds a
  way to fail.

## Keep doing
- Read a red check's own runs before assuming a test failed: here the head had a green run and a
  cancelled one.

## Stop doing
- `cancel-in-progress` on checks that gate a fail-closed merge.

## Open
- None.
