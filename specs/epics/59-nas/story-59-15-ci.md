# Story 59.15 — CI on NAS-shaped containers, device checklists, logs kept

> Epic 59 · Phase 1 (grows each phase) · Depends on: 59.01 · Needed by: every story's tests

## Why

Most NAS failures come from a handful of differences, and each one can be imitated in a
container on a GitHub runner:
- another uid;
- a read-only or foreign-owned share;
- a memory limit;
- arm64;
- a kernel without Landlock;
- a render device or none.

What cannot be imitated needs a short written checklist on a real device: DSM's ACL dialogs,
TrueNAS's app install, real Quick Sync, a real DSM kernel.

## Description

**The `nas-shapes` job** (`.github/workflows/nas.yml`):
- **When it runs:** on changes to the image, `cmd/maktaba-server`, `streaming/internal/ffmpeg`,
  the pipeline's runner, scanner or STT code, plugind, or the NAS compose and template files (CI
  runs only related jobs). It also runs on every release candidate.
- **What it does:** builds the server image for the PR, then runs the matrix below. Each cell
  starts the container with a fixture library (40 short public-domain clips in English and Arabic,
  one HEVC, one with an Arabic subtitle), runs setup headlessly, waits for ready, and checks the
  cell's assertions with the admin API.

| Cell | Runner | Shape | Asserts |
|---|---|---|---|
| `uid-1000` | ubuntu-24.04 | default | baseline ready; scan; thumbnail; transcode |
| `uid-568` | ubuntu-24.04 | `--user 568:568`, volume owned by 568 | ready; no process as another uid |
| `uid-unraid` | ubuntu-24.04 | `--user 99:100` | ready |
| `uid-dsm` | ubuntu-24.04 | `--user 1026:100`, volume group-writable by 100 | ready |
| `media-ro` | ubuntu-24.04 | media `:ro` | Read only; subtitles in data; convert 409 |
| `media-none` | ubuntu-24.04 | a second root mode 000 | No access; no scan for it |
| `mem-4g` | ubuntu-24.04 | `--memory=4g` | Light; Postgres PSS sum < 512 MB; `memory.current` under the limit |
| `mem-3.7g` | ubuntu-24.04 | `--memory=3700m` | setup continues on Light (the 3.5 GiB floor) |
| `mem-6g-stt` | ubuntu-24.04 | `--memory=6g`, speech on | Standard; a clip transcribed |
| `arm64` | ubuntu-24.04-arm | default + speech on (`base`) | ready; a clip transcribed (59.06) |
| `no-landlock-enosys` | ubuntu-24.04 | a seccomp profile returning `ENOSYS` for `landlock_*` and `EPERM` for `unshare` | isolation level Container; plugind `partial`; third-party plugin held (59.07) |
| `no-landlock-eperm` | ubuntu-24.04 | the same with `EPERM` for `landlock_*` (older Docker default profiles) | same |
| `uid-unnamed` | ubuntu-24.04 | `--user 4321:4321`, no passwd entry | `nss_wrapper` names it; Postgres ready (59.01) |
| `bridge` | ubuntu-24.04 | bridge network, published ports | ready; setup says mDNS is off |
| `offline` | ubuntu-24.04 | `--network none` after pull | ready without PyPI (59.01) |
| `vaapi-fallback` | ubuntu-24.04 | `MAKTABA_HWACCEL_PREFER=vaapi`, no device | test encode fails → libx264, reason recorded |
| `privileged` | ubuntu-24.04 | `--privileged` | refused, exit 78 |

- **arm64:** GitHub's arm64 runner runs the arm64 image natively, so there is no emulation.
- **The update legs** of 59.12 run on this harness, as `update-rehearsal-container`.
- **TrueNAS app checks:** `truenas/apps`' own app tests (its `ci` script, render and lint) run
  against `deploy/nas/truenas/ix-dev/community/maktaba` in our repo (59.10).
- **The Unraid template:** XML schema check plus a render test.

**Logs kept in every run:**
- `docker logs`, plus the server log, each worker's log and plugind's log from the volume;
- the setup report and `GET /api/system/machine`;
- all of these go to the job's artifacts and are linked in its summary, pass or fail.

**Device checklists** (`docs/nas/checklists/{synology,truenas,unraid}.md`):
- run by hand on the project's devices (README Q6) before a platform's first release and after
  each DSM or TrueNAS major;
- each step says what to do and what to see, with dark screenshots in English and Arabic;
- the result is a dated file in `docs/nas/checklists/results/` with `docker logs` and the setup
  report attached;
- **what they cover:** the install from the guide, the ACL fix flow, Quick Sync on a J4125, the
  disk-sleep check, a real update in the window, the DSM kernel's Landlock result, TrueNAS on
  25.04 and 25.10, and Unraid's cache-pool path.

## Acceptance criteria

- **Given** a PR touching `deploy/docker/server/Dockerfile`, **then** `nas-shapes` runs every
  cell, and a failing cell fails the PR.
- **Given** a PR touching only `web/`, **then** `nas-shapes` does not run.
- **Given** any run, pass or fail, **then** each cell's logs and setup report are in its
  artifacts.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | workflow | PR on the Dockerfile | open | `nas-shapes` runs all cells |
| T02 | workflow | PR on `web/` only | open | skipped |
| T03 | workflow | cell fails | run | artifacts uploaded; summary links them |
| T04 | docs lint | `docs/nas/checklists/*.md` | lint | each step has an expected result; screenshots exist in both languages |

## Strings

None in the product. The checklists are in English, with the Arabic UI shown in their
screenshots.

## Edge cases

- **arm64 runner queue times:** the arm64 cell is required only when pipeline STT, the image or
  streaming change.
- **A flaky cell:** fix the cause. A stale assertion is not a flake (tests ship with the code).

## Build plan

| File | Change |
|---|---|
| `.github/workflows/nas.yml` (new) | the matrix, paths filter, artifacts |
| `tests/nas/` (new) | the fixture library, cell scripts, assertions |
| `tests/nas/seccomp-no-landlock-{enosys,eperm}.json` (new) | the two profiles |
| `docs/nas/checklists/` (new) | device checklists and results folder |
