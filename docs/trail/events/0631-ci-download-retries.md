---
schema: 1
kind: pr
pr: 631
title: CI artifact and Go module downloads retry and name what never arrived
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge 3eba2e4ad. A single artifacts-API timeout failed an iPad Player UI shard on #626 before any test ran.
- Every artifact download in `.github/workflows` retries twice with a short wait and, if all three attempts fail, stops with an error naming the artifact.
- Every Go job's module download tries up to three times with short waits (`.github/actions/go-mod-download`), and when all three fail it stops with an error naming the module. Jobs that a pull request runs then read the Go module cache only.
- Tests in `tools/ci/tests` (`test_artifact_download.py`, `test_go_mod_download.py`) keep every download in that shape.
