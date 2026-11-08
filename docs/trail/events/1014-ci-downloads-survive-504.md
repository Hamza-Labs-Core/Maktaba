---
schema: 1
kind: pr
pr: 1014
title: CI's remaining downloads survive github.com's 504s (golangci-lint, archives, caches, e2e, Gradle, WiX)
date: 2026-11-08
session: c68b2c67-3ef2-4639-8bce-54db0b3a8dbb
prs: [1003, 1007]
---

## What happened
- After #1003, main's CI run 42476830514 (397a33b5a) still failed on four downloads #1003's `tools/release/fetch-pinned.sh` did not reach, and main stayed red with no tag after v0.3.105:
  - lint: golangci-lint-action's own fetch of golangci-lint 2.12.2;
  - win-app: pgvector's `/archive/` tarball, 504 on five tries;
  - linux-app: `e2e.sh`'s plain curl of the latest release's payload;
  - desktop-player on Linux and Windows: the Gradle wrapper's single 10 s try at services.gradle.org.
- #1014 covers each of these:
  - golangci-lint: `tools/ci/golangci-lint.sh` fetches the pinned release through `fetch-pinned.sh`, cached by its pin; the action runs it with `install-mode: none`.
  - Archives: `fetch-pinned.sh` tries codeload.github.com first for a github.com source archive.
  - Download caches: Windows, Mac and Linux save what came when a download fails, under a `-partial-<run>-<attempt>` key.
  - Linux e2e: the release fetch moves to `tools/release/linux-app/previous-release.sh`. checksums.txt is pinned by the release's asset digest and the payload by its checksums.txt line, both through `fetch-pinned.sh`.
  - Gradle: `tools/ci/gradle-wrapper.sh` checks the jar, fetching the checksum with retries from services.gradle.org only. It also pre-fetches the distribution, checked against the new `distributionSha256Sum`, into the wrapper's own folder. The wrappers get `networkTimeout=60000` and `retries=4`. These steps also go into the Android apps and TV jobs, and the jar check into `desktop-release.yml` and `play-bundles.yml`.
  - WiX: the Windows Player's toolset is pinned and put where compose's `downloadWix` skips its own fetch.

## What we learned
- `/archive/` on github.com is a 302 to codeload.github.com, which serves the same bytes (pgvector 0.8.7's SHA-256 is the pin by both) and kept answering while github.com 504'd. The REST API's tarball (`/repos/O/R/tarball/REF`) is not the same file: its top folder is `O-R-<commit>`, so its SHA-256 never matches an `/archive/` pin.
- An all-or-nothing cache save never fills during an outage. The Windows downloads cache was saved only after every download succeeded, and main's never did. The one entry belonged to `refs/pull/986/merge`, which main and other pull requests cannot read.
- `actions/cache` derives an entry's version from its path list. A save whose paths differ from its restore's writes an entry the restore never finds, so adding an exclude (`!*.part`) means changing both steps (a test now checks every pair).
- setup-gradle's post step saved an empty Gradle User Home entry after the wrapper download failed. With `cache-overwrite-existing: false`, later good runs could not replace it until the job key changed; changing the wrapper properties changed it.
- The Gradle wrapper names its distribution folder by the MD5 of `distributionUrl` in base 36 (`8wepis63pzuhrlfaiy04bagwu` for 9.8.2-bin). A zip there with no `.ok` is unpacked, not downloaded. Compose 1.12.2 skips `downloadWix` when `$GRADLE_USER_HOME/compose-jb/wix311.zip` exists.
- An integrity check must not take its trusted source from the file it checks: the jar check now refuses a `distributionUrl` off services.gradle.org (the background security review caught the first version).

## Keep doing
- Send every CI download through `fetch-pinned.sh`, or a script built on it, with a pinned SHA-256 and a cache keyed on the pin. When a tool's own installer downloads, find the file or folder that makes it skip the download and put the pinned copy there.
- Save a download cache after a failed run too, under a key the exact match never hits.

## Open
- TinyGo and wasm-tools in `_plugins.yml` still use `curl --retry 3`. The step is `continue-on-error`, so it cannot turn the job red.
- CodeQL's default setup downloads its own tools; a rerun is still the remedy.
- The packs' recipes (`tools/release/packs/`) fetch with `curl --retry 3`. They run in no required pull request job.
