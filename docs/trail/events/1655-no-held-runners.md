---
schema: 1
kind: pr
pr: 1655
title: "A Player UI shard is queued after its device's build; no Mac or Windows runner waits for another job"
date: 2026-11-24
session: 054302db-e715-4759-9531-2aa5cd7a607e
agent: claude-code
---

## What happened
- **Why.** Since #539 the iPhone and iPad shards started beside their build and asked the API for
  its artifact for up to 30 minutes, each on an `xcode-27` runner. On 2026-11-24, with about 16
  pull requests queued, shards held the runners their own builds were queued for. In run
  45414213909 (#1616) iPhone 1/6 and `shots` each held a Mac for about 40 minutes and failed with
  no test run; the iPhone build started 8 minutes after the last of them gave up.
- **The fix.** `_apple-apps.yml` has a build job and a test job per device
  (`player-ui-build-iphone` → `player-ui-tests-iphone`, the same for iPad). A shard `needs:` its
  own device's build, so an iPad build failure leaves the iPhone's shards be. The devices share
  their env, shards and steps by YAML anchors. Job names are unchanged.
- **Re-runs.** The build artifact is kept 7 days instead of 1.
- **The guard.** `tools/ci/tests/test_no_held_runners.py` fails a job on a macOS, Windows or
  Xcode runner that asks GitHub about another job in a loop.

## What we learned
- The overlap was for the simulator's settling (130ba244b), which is needed, but it only saved
  the build's 3 to 4 minutes: a simulator takes 6 to 14 to boot and settle. On an idle fleet a
  pull request's Player UI results now come about 3.5 minutes later; on a busy one it queues
  twice.
- `needs:` on a matrix waits for every leg, and one failed leg skips every dependent. Two jobs
  keep the legs apart without renaming a check (a reusable workflow per device would add a
  segment to each name, which the tag guard and the website's screenshots match).
- An artifact belongs to the run: attempt 2 of run 45411409243 passed on attempt 1's build, and
  a later attempt may upload the same name again.
- What still keeps obsolete Apple jobs alive: a cancelled shard's `always()` log reading (1 to 4
  minutes), a closed pull request's run, and the older head's run when a push makes a pull
  request conflict (no `pull_request` run starts, and `ci-branch.yml` has its own group).

## Keep doing
- Pass another job's output with `needs:` and an artifact. A queued job holds no runner.

## Stop doing
- Waiting in a step, on a macOS or Windows runner, for another job.

## Open
- #1616 (universal apps) edits the same build matrix: one scheme line conflicts.
