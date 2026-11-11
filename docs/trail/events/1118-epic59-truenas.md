---
schema: 1
kind: pr
pr: 1118
title: "The TrueNAS app, its Custom App YAML and the catalog's own CI (story 59.10)"
date: 2026-11-10
session: 27e0d39f-8860-47e8-8e91-9cd1f9c9742e
agent: claude-code
---

## What happened
- #1118 (merged at 6a2630590) adds Maktaba's TrueNAS app in `truenas/apps`' own source layout (`deploy/nas/truenas/ix-dev/community/maktaba`), plus a Custom App YAML (`deploy/nas/truenas/custom-app.yaml`) for TrueNAS 25.04+ until the app is in the catalog. The app's defaults:
  - runs as 568;
  - data on a host path (a dataset with the Apps preset) or an ixVolume, which the catalog's permissions step hands to 568;
  - each media folder at its own path, with a read-only toggle;
  - host network by default, with an optional Host IP that 59.08 announces on;
  - 8 GB memory;
  - the catalog's GPU question;
  - `MAKTABA_PLATFORM=truenas`, and never `MAKTABA_UPDATE_DISABLE`.
- Server changes:
  - `maktaba-server healthcheck`: the image has no curl. It is healthy while the container waits for setup, then only when `/api/system/health` answers 200.
  - The not-set-up hint names the app's Shell on TrueNAS.
  - The preflight's write-denied text adds the TrueNAS fix (dataset ACL for 568 with Modify, or the Apps preset). 59.03's fix text already worded TrueNAS; it was checked and left unchanged.
- `nas.yml` gains `truenas-lint` and `truenas-deploy` (`tests/nas/truenas_app.py`), running truenas/apps' tooling at a pinned commit (`tests/nas/truenas/catalog.env`):
  - the catalog's metadata check, port check and render of each test values file, with each render's shape checked;
  - renders a runner can't start (GPUs, host-path data);
  - T06: this PR's image as the ixVolume install. The permissions step gives the volume to 568, setup runs from the "Shell" (`docker exec`), the server is ready as 568, and `/api/server/machine` reads truenas and the app's memory limit;
  - then the catalog's own deploy of each test values file. The `uid-568` cell gains `healthcheck`; `landed` += 59.10.
- Releases: a release never commits a new pin to main. That commit would be the next release candidate, and every release would make another.
  - `release.yml` renders `maktaba-truenas-app-<version>.tar.gz` and `maktaba-truenas-custom-app.yaml`, pinned to the server image's `tag@digest` and signed by `checksums.txt` (`tools/release/truenas_app.py`).
  - `packages.yml`'s `truenas-catalog` opens the catalog bump PR once `vars.TRUENAS_APP_IN_CATALOG` is set.
- The guide is on 59.11's install site: `/install/truenas` and `/ar/install/truenas` (`web/packages/install`). TrueNAS screenshots are marked placeholders. The device checklist covers 25.04 and 25.10.

## What we learned
- The catalog's renderer (its `apps_validation` image) needs `openat2`, which Docker Desktop's amd64 emulation on Apple silicon lacks (ENOSYS). It only runs on Linux amd64, so the app was iterated in CI.
- The catalog's tooling rejected four things in turn:
  - a README with lists or code: only a title and `a b br em i p strong` are shown;
  - default port 8081: another catalog app has it, and default ports are unique across the catalog. The live TV port now defaults to 30575;
  - metadata its generator writes differently (`lib_version_hash`, the description's wrapping, its own catalog source link);
  - a test-values media host path that doesn't exist: host paths aren't created unless `create_host_path` is set, which only the test values set.
- `docker top <id> -eo uid,comm` fails ("Couldn't find PID field"): the format needs `pid`.
- CodeQL flagged the first `truenas-catalog` job (critical, untrusted checkout TOCTOU). It checked out the tag and ran repository code in a `workflow_run` job with a token and a secret. It now checks out nothing and runs nothing: it verifies the release's signed archive (cosign, `sha256sum`, regular files under the app's folder only) and copies the files as data. `tools/ci/tests/test_truenas_app.py` pins this.
- `TestSupersedeOnlyWhenTheNewerRunStarts` (api/internal/updates) hard-breached the unit budget (1.27 s alone on CI) on a PR that doesn't touch `api/`. It stepped the driver 40 times where it needed only the steps until its machine waited on its app; with `stepUntil` it takes 0.24 s under `-race` locally, down from 0.53 s.
- A local `uv run` at the repository root left a `uv.lock` that was committed; `lint / repo`'s ci-paths guard caught it as an unmatched file.
- `pull_request` workflows don't start while a PR conflicts with main. Only `CI (branch head)` ran until main was merged in.

## Keep doing
- A job started by `workflow_run` (or any job holding a token or a secret) takes a release's signed assets as data. It never checks out or runs the repository's code: anything such a job needs is built by the tag's own workflow.
- Run the catalog's own tooling (`tests/nas/truenas_app.py`) in CI for any change to the app, and copy `metadata.diff` back from the lint artifact instead of hand-editing generated fields.
- Use `uv run --no-project` (or the scratchpad) for one-off tools, and check `git diff --name-only origin/main...HEAD` before pushing.

## Open
- Owner actions:
  - run the TrueNAS device checklist on 25.04 and 25.10 (N100 and CPU-only), including the guide's TrueNAS screenshots;
  - submit the app to truenas/apps' community train under the project account (steps and PR text in `deploy/nas/truenas/README.md`);
  - then set `vars.TRUENAS_APPS_FORK`, `secrets.TRUENAS_APPS_TOKEN` and `vars.TRUENAS_APP_IN_CATALOG`.
- On a bridge, the live TV port's default differs from Maktaba's own 8081. The server doesn't yet learn a published port that differs from its own.
- The app's `icon` URL (media.sys.truenas.net) only exists once the catalog's reviewers host it.
