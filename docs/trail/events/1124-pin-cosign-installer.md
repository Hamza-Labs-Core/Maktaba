---
schema: 1
kind: pr
pr: 1124
title: "ci(packages): cosign-installer pinned to its commit in the release-privileged jobs"
date: 2026-11-10
session: ca5b5f5a-daa4-4edf-94fd-eb52e3852eeb
---

## What happened
- #1118 (59.10, merged at 6a2630590) added the `truenas-catalog` job to
  `.github/workflows/packages.yml`. It runs on `workflow_run` with a token that can open pull
  requests on the TrueNAS catalog fork, and used `sigstore/cosign-installer@v3.10.0`, a tag that
  can be moved.
- The 59.08 child noticed it while merging main into #1119 and left it out of that pull request.
  #1124 (merged at 087b995c7) pins the three cosign-installer steps in `packages.yml` to the
  commit of v3.10.0, `d7543c93d881b35a8faa02e8e3605f69b7a1ce62`, as `play-bundles.yml` already
  pins its actions.

## What we learned
- A job that holds a write token for another repository is privileged even when it runs no repo
  code; every action it uses should be pinned by commit, not by tag.

## Keep doing
- A finding that belongs to another pull request's change gets its own small pull request, not
  a ride in an unrelated story.

## Stop doing
- Adding actions by tag to jobs that hold a token for another repository.

## Open
- Other workflows still use cosign-installer by tag (`relay.yml`, `release.yml`); they run on
  this repository's own events. Pinning them is optional hygiene, not done here.
