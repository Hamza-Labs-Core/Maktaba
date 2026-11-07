---
schema: 1
kind: incident
title: CodeQL flagged the rehearsal's venv cache as cache poisoning; dismissed as before
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
prs: [842]
---

## What happened
- The rehearsal waves PR (842) failed the `CodeQL` check with one new high alert, number 170 (`actions/cache-poisoning/poisonable-step`). It was on the kit job's step that reads `versions.env` before the previous release's pipeline environment is saved to the cache.
- The same rule's alerts on this workflow, numbers 151 to 158, had been dismissed as false positives with PR 737.
- Three facts were checked before dismissing it the same way:
  - the kit job runs on `workflow_run` only for CI's successful push runs on main of this repository, and the cache save only on `workflow_run`;
  - `versions.env` is written in the same job by `build.sh` from the checked-out main commit, not from a pull request or an artifact;
  - the cache key names the release (its tag).
- It was dismissed on the PR's analysis through the code-scanning API, citing those facts. The check turned neutral and PR 842 merged.

## What we learned
- A new step in a job that already saves caches from `workflow_run` raises the alert again, even when the job's gating is unchanged: a known false positive is dismissed per alert, with the reason.

## Keep doing
- Before dismissing a security alert as a false positive, check its facts on the code as it is, and write them in the dismissal (280 characters at most).

## Open
- None.
