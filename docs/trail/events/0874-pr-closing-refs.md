---
schema: 1
kind: pr
pr: 874
title: A PR body must not close another open pull request
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [872]
---

## What happened
- `tools/ci/pr_closing_refs.py` finds GitHub's closing keywords before `#N`, `owner/repo#N` or this repository's issue and pull URLs, and asks GitHub whether N is an open pull request (`/pulls/N` answers 404 for an issue). It fails when one is.
- It runs in the PR body check job, on pull request events. Closing issues stays allowed; a GitHub error fails the check.

## What we learned
- Asking `/pulls/N` tells a pull request from an issue with only `pull-requests: read`, which the CI workflow already grants.
- CI runs on a pull request's pushes, not on edits of its body: an edited body is checked at the next push.
- Checked live: the real body of the merge for PR 846 fails, naming 795; the body of PR 795, which closes an issue, passes.

## Keep doing
- Run a new check on the real case that caused it before relying on it.

## Open
- None.
