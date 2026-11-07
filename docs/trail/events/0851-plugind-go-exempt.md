---
schema: 1
kind: pr
pr: 851
title: The plugind job's Go setup is exempt from the module-download rule
date: 2026-11-07
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [848]
---

## What happened
- After #851 was retargeted to main, `lint / repo` failed on `tools/ci/tests/test_go_mod_download.py`. The `plugins / plugind` job sets up Go with no `go mod download` step.
- That Go only builds a plugin that `mkp new` generates outside the checkout, with TinyGo, best effort (story 54.12 T02). It has no module of the repository to download, so the job got an `EXEMPT` entry that gives the reason.

## What we learned
- A PR stacked on another runs main's repo-wide lint rules only after the retarget. Run `tools/ci` tests right after the retarget, not at merge time.
