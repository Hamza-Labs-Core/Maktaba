---
schema: 1
kind: pr
pr: 546
title: CI Go modules from a verified cache; root-store test on macOS
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge b34dd6f2. node-packaging `wheel + archive` had setup-go `cache: false`, so `gen-release-json` fetched releaseverify's about 40 modules live each run. Now cached on `tools/release/go.sum` + `shared/go/releaseverify/go.sum` with `go mod download && go mod verify`. release.yml `sign-and-release` uses the same go-version-file, runner and key (a tag run restores main's cache), and verifies. nfpm is a cached static binary (key `nfpm-v2.46.3-static-<os>-<arch>`). coverage-map keeps no cache (CodeQL #108).
- `TestRootStoreRefreshKeepsLastGood` failed only on macOS: `RootInfo.Source` resolves symlinks (`/private/var/...`) while `t.TempDir()` gives `/var/...`. Not a time bomb. The test compares the resolved path; `TestVerifyExpiredRoot` no longer uses the wall clock. Product logic unchanged.

## What we learned
- On macOS, `t.TempDir()` is under `/var`, a symlink to `/private/var`; compare resolved paths.

## Keep doing
- Bump the nfpm cache key in release.yml and _launcher.yml together.
