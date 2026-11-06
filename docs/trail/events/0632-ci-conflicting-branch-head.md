---
schema: 1
kind: pr
pr: 632
title: CI runs on a conflicting pull request's branch head
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge 8178f6d7a. CI also runs on a pull request's branch head when the pull request conflicts with its base, through `ci-branch.yml` calling `ci.yml` with path filters measured from the merge-base, so a branch is tested even while main has moved on.
- A mergeable pull request still gets only its `pull_request` run, and the required `ci-success` check and the tag guard read only `ci.yml`'s own runs.


## Open
- Two Player UI shards stayed in progress after their run had completed (#649).
