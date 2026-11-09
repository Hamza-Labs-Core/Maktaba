---
schema: 1
kind: pr
pr: 1036
title: One Go cache entry per runner and Go version, free cache cuts, and a prune of stale entries
date: 2026-11-08
session: 189fe2c3-f792-4847-a749-bfb7385c2a71
prs: [1014, 1033, 1034]
---

## What happened
- After #1014 the repository's Actions caches were over GitHub's 10 GB limit: the usage API read 18.6 GB in 92 entries, and between 12.4 and 18.6 GB as entries were written and evicted. golangci-lint's pinned entry was evicted within the hour (d20261108-ci-cache-eviction-linuxdeploy).
- One green main run (42486732449, 20ce00786) wrote about 20 setup-go keys, one per job's go.sum set, runner image and Go version, of 0.2-0.9 GB each. Most of the run's caches had been evicted already, so most jobs in it ran uncached.
- #1036 (merged as 5f07b94e4, from head 8dfa81a8a):
  - **Go:** `.github/actions/go-cache` keys on runner OS, arch, Go version and every go.sum in the checkout, with no runner image, and falls back to the newest entry for the same runner. Four jobs write one entry each on main, after `tools/ci/go-cache-fill.sh` put every module in it: unit (Linux x64), the Linux app's aarch64 leg, win-app and mac-app. Every other job restores only, including the jobs that were still on setup-go's own cache.
  - **Rust:** the plugind job, the sandbox's ubuntu-24.04 leg and the rehearsal kit share one entry, which plugind writes. bundle-size restores mac-app's entry. The desktop-shell unit jobs keep no Rust cache, and the sandbox's ubuntu-22.04 and macos-15 legs write none.
  - **The prune:** `tools/ci/cache_prune.py`, every 30 minutes (`cache-prune.yml`). Its one job has `actions: write` and `pull-requests: read`. It deletes only entries on the refs of closed pull requests, and main entries of a known family (setup-go, Rust, rehearsal) that a newer one replaced, once they have been idle for an hour. A test pins down that selection.
- **The numbers in the PR body:** about 21 GB written by main before, about 11.8 GB projected after. setup-go went from ~7.8 GB in 19 keys to ~2.4 GB in 4. The Rust desktop-shell entries (2.5 GB) are gone, and the plugin-host entries went from 2.2 to 1.35 GB.
- **Decision:** accept a working set above the 10 GB limit, with bounded churn plus the prune. The limit is not raised (no billing change), and no cache that measurably speeds a job is dropped. The reasons:
  - Since #1003/#1014 an evicted pinned tool only means a re-fetch through `fetch-pinned.sh` (API first, retried, SHA-256 checked): seconds, never a failed job.
  - The caches left as they are save real time. The Linux app compiles 9 crates with its Rust cache and 383 without, about 2.2 min a run. unit took 15.3 min uncached and 8.7 min warm.
  - Every cut belongs to a job that ran uncached on main anyway, at the same time.

## What we learned
- An eviction-churned cache reports little loss: a job that finds nothing re-saves its entry, which evicts the next one, and the run still goes green. The way to see it is a main run's "No cache found" and "Cache saved" lines per job: run 42486732449 showed most jobs re-creating their entries. That same run gives each job's uncached time, the cost of dropping its cache.
- The PR's table missed the rehearsal seed's `full` profile: `rehearsal-seed-v0.3.106-full` is 2.7 GB, beside `restore` (1.26 GB) and `small` (0.17 GB). The rehearsal family is about 4.1 GB per release, not 1.42, so the projection after #1036 is about 14.5 GB, not 11.8. The prune keeps one release's set; the set itself is the largest single family.
- A cache budget counts every profile and matrix leg a family writes. A snapshot of the listing under churn shows only what survived, so take the sizes from the jobs' own "Cache Size" lines as well.
- Go's build cache is addressed by its inputs, the C compiler's identity among them, so one entry serves several Ubuntu images; the runner image does not need to be in the key.
- With the repository over its limit, an entry can be evicted minutes after it is saved: #1034's tauri-tools entry, saved in a run's first attempt, was gone by the rerun 40 minutes later.

## Keep doing
- Before trimming a cache, measure each family from the listing and from the jobs' own save lines, and price each cut by the job's uncached time on main.
- One writer per cache entry on main, and every other job restores only.

## Open
- Compare job times on main's second run after the merge, once the four Go entries and the shared plugin-host entry exist: unit should be near 8.7 min; plugind, the sandbox legs and the app jobs against run 42486732449.
- Re-measure cache usage after that run. The rehearsal seeds (about 4.1 GB a release, `full` 2.7 GB) are the largest remaining family: whether the `full` seed needs a cache is a question for its job's time.
- The old per-job setup-go keys go once the new ones exist: by the prune after an hour idle, or by eviction.
