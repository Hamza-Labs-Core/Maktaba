---
schema: 1
kind: pr
pr: 1597
title: "Why no test caught the frozen release catalog: CI now reads GitHub's real release list as a home does, and a failing check is reported, not only logged"
date: 2026-11-24
session: 263054ab-a779-459f-8a4c-37197ee37073
agent: claude-code
---

## What happened
- **The incident** (d20261124-stale-catalog-step-down, #1592):
  - From 2026-11-23 11:02Z every home's update check failed. The list of the newest 30
    releases was larger than the 8 MiB a home read (`catalog.go`, e9e281f27, 2026-10-19).
  - The failures were only logged, for over a day.
  - The live home chose v0.3.157 from a frozen catalog and rolled back again.
- **Why no test or check caught it:**
  - `TestListAllocatesLessThanTheList` built a made-up list of 30 releases with 150 assets
    each: about 7 MB, some 85% of the cap. Nothing ever compared it with GitHub's real answer.
  - Assets per release grew from 155 (v0.3.150) to 188 (v0.3.158), which crossed the cap.
  - No CI job read the live list the way a home does.
  - The update rehearsal served its releases from a short fake list.
  - `update_check_error` was a log line: the Updates page said nothing until the 14-day
    staleness warning (`StaleAfter`), and System health had no item for it.
- **Measured on 2026-11-24 with a home's own request** (unauthenticated,
  `Accept: application/vnd.github+json`): GitHub answers minified JSON, 3,197,782 bytes for
  10 releases, about 325 KB a release. So 30 releases are about 9.6 MB.
  - The 10.5 MB measured during the incident was the pretty-printed answer GitHub gives to a
    generic `Accept` (curl's `*/*`), which no home asks for.
  - A token with push access lists the drafts first.
- **#1597 (head 1d20705d43164457ad24ede47d804ddc165e29dd, not merged here):**
  - **The check:** `api/cmd/release-list-check` and `updates.ProbeReleaseList` read the live list
    through the home's own code. It runs hourly (`github-release-list.yml`, which opens an issue on a
    failure) and in `release.yml` and `desktop-release.yml` after they publish
    (`tools/release/release-list-check.sh`, not `continue-on-error`). It fails when any answer is over
    70% of its cap, or when the newest stable release can't be selected.
  - **The data:** a recorded real page (`tests/fixtures/github-releases`), with a worst-case
    page of releases twice the largest recorded one.
  - **The cap:** the list page cap is 16 MiB, so the worst case is 39%.
  - **The home:** the failure streak is saved in the check's state. From 2 failures the Updates
    page says "Can't check for updates since …" with the reason, System health's new
    **Update check** item turns degraded, and admins get `update.check_failing`.
  - **The rehearsal:** scenario 20 serves the list at twice its real size and shape, then
    past its cap.
  - **The sweep:**
    - The About check and `selfupdate` read the list with the same cap. They used to truncate
      with `LimitReader`, so a too-long list showed up as a decode error.
    - Every other cap on GitHub release data has a real-size test.
- **The live probe on 2026-11-24 ~05:50Z** found v0.3.154–v0.3.160 turned into prereleases on
  GitHub since 05:43Z. The catalog correctly passes over them.

## What we learned
- A test of a cap is only as good as the data it is sized from. A made-up fixture at 85% of
  the cap proves the parser, not the headroom. The headroom has to come from the real answer
  times a growth factor.
- A response that grows with every release (releases × assets) needs a check against the
  live source, not only a fixture. The fixture is a snapshot; the list keeps changing between
  releases.
- An error a home only logs is an error nobody sees. The 14-day stale warning was the only
  alarm, and it was built for a freeze attack, not for a broken read.
- A cap enforced through `io.LimitReader` turns "too large" into "cannot decode". An explicit
  over-cap error (read cap+1) names the real problem.
- How a response is measured matters: the incident's 10.5 MB was the pretty-printed answer to a
  request no home makes.

## Keep doing
- Measure an external answer with the exact request the product makes: URL, headers,
  authentication and User-Agent.
- Run the same code path in CI that runs in the product (the probe calls `listPage` and
  `candidate`), not a re-implementation of it.

## Stop doing
- Sizing a cap's test from synthetic data alone.
- Letting a background check that fails stay log-only.

- **Scenario 20's first run** on #1597's CI passed end to end:
  - The update to the next release was found and installed through a list page of 6,439,436
    bytes, twice the recorded one.
  - Two checks against the list past its cap failed with `too_large`.
  - System health then showed `update_check_failing`.
  - One check against the list back at its size cleared it.

## Open
- Naming: a parallel pull request adds Maktaba's own signed release feed on maktaba.tv, so
  everything here calls GitHub's list "GitHub's release list" (`github-release-list.yml`,
  `release-list-check`, `ProbeReleaseList`). Once that feed lands, the hourly check can verify it
  first.
- The hourly check reads anonymously from GitHub's shared runner addresses and falls back to
  the workflow token when rate-limited. If that token can list drafts, the pages it reads differ
  from a home's (the report counts drafts).
- The plugin store catalog (`api/internal/plugins/catalog`, 60 MiB) still truncates silently
  with `LimitReader`. It is not GitHub data, and its host is not live yet.
- A home whose catalog check has failed for hours still installs from its stale catalog. Holding
  automatic installs after N failed checks is still undecided (also open in
  d20261124-stale-catalog-step-down).
- The Android and Apple apps don't show the new Update check state yet.

Caps on GitHub release data (2026-11-24, v0.3.161), each with its real size and the share at 2× growth:

| Where | Reads | Cap | Real | 2× |
|---|---|---:|---:|---:|
| updates `listPageLimit` | list page of 10 | 16 MiB | 3.2 MB | 39% |
| updates `tagReadLimit` | one release by tag | 8 MiB | 325 KB | 8% |
| updates `smallFileLimit` | signed files, components index | 4 MiB | 47 KB at most | 2.2% |
| system/updater.go `releasesLimit` (About) | list page of 10 | 16 MiB (was 8, silent) | 3.2 MB | 39% |
| selfupdate `listLimit` / `releaseLimit` / `smallFileLimit` | list, tag, signed files | 16 / 8 / 4 MiB | 3.2 MB / 325 KB / 15 KB | 39% / 8% / 0.7% |
| ghasset `releaseLimit` | one release by tag | 4 MiB | 325 KB | 16% |
| packs `smallLimit` | signed files, components index | 16 MiB | 47 KB | 0.6% |
| releaseverify `maxSmallFile`, install `maxSmall`, `maxBundle` | signed files, bundle | 4 / 4 / 1 MiB | 15 KB / 15 KB / 9 KB | under 2% |
| updates/mirror.go `mirrorFileLimit` | mirrored release file | 4 GiB | 1.99 GB | GitHub caps an asset at 2 GiB |
| updates/tufmirror.go `tufFileLimit` | Sigstore TUF metadata | 16 MiB | 5 KB | under 0.1% |
