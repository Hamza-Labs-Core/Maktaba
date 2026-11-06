---
schema: 1
kind: pr
pr: 618
title: Only main saves the Actions caches
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge eacad4dea. Only a push to main writes the Actions cache; pull requests and tags restore main's entries.
- setup-go and setup-node run without their own cache, and `.github/actions/go-cache` and `node-cache` restore under the same keys those actions use, so main's existing entries still hit; each job ends with an `actions/cache/save` step that runs only on a push to main. uv, rust-cache and golangci-lint use their own switches; every other `actions/cache` became a restore step plus a main-only save; the apt keys lost `github.run_id`; play-bundles and desktop-release use setup-gradle read-only (desktop-release's Linux download keys now match CI's).
- The PR's own run restored main's Go, pnpm, uv, Rust and Gradle entries and left no `refs/pull/618` entries. 41 old PR entries (about 9.5 GB) were deleted, and the cache went from 10.7 GB to about 5 GB.
- Two Apple UI flakes fixed at the root: Continue on the permissions walkthrough is tapped only once the sheet has stopped moving, and the quality-reopen test checks the start between the positions read before and after the tap.
