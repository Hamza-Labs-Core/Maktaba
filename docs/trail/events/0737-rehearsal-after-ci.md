---
schema: 1
kind: pr
pr: 737
title: On main the update rehearsal runs after CI passes, on the same commit
date: 2026-11-07
session: 4865f802-3686-437d-b08d-1a61e9509c69
prs: [730]
---

## What happened
- Nothing could be tagged after v0.3.82. `ci.yml` and `update-rehearsal.yml` each kept one main concurrency group that finished its running run and replaced its pending one. With merges every few minutes the two finished on different commits. From 2026-11-07 00:54 no commit had both green: CI's last green run was on 3b441e98a, and the rehearsal's last green run was on 7803e00f0, whose CI run was cancelled while pending.
- On main, `update-rehearsal.yml` is now a `workflow_run` of CI. When CI's push run of a main commit succeeds, it starts the rehearsal on that commit (every job checks out `github.event.workflow_run.head_sha`). Main's rehearsals share one group, not cancel-in-progress. A CI run that did not pass also starts a run: it runs no job, in a group of its own. The pull-request trigger, the nightly run, the dispatch and the `release/**` push are unchanged.
- The release session now runs `next_tag.py --ci-runs ci.json --rehearsal-runs rehearsal.json` (docs/updates.md, "How a release is made").

## What we learned
- A `workflow_run` run's own `head_sha` is the default branch's head at the moment it starts, not the triggering run's commit. Coverage-map's run created at 01:35:43 had 310f0e2d9, while the CI run that started it, finished two seconds earlier, was for 3b441e98a. So `gh run list --commit` finds such a run under the wrong commit. The rehearsal names its run `Update rehearsal of <sha>` and sets that commit's `update-rehearsal` status. `tag-guard.sh` and `next_tag.py` read the commit from the name, and never count a `workflow_run` run for its `head_sha`.
- Its checks therefore show on a newer commit than the one it rehearsed; read the run name or the commit status.

## Keep doing
- Chain a workflow that must agree with CI on a commit after CI (`workflow_run` on success) instead of running both beside each other on main.

## Open
- The first commit with both green after this change is reported in the PR.
