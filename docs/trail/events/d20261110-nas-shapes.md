---
schema: 1
kind: change
pr: 1091
title: "nas-shapes: the server image in NAS shapes on CI (story 59.15, phase 1)"
date: 2026-11-10
session: be65aad1-1a50-4093-8009-cecd09de4ca9
agent: claude-code
---

## What happened
- `.github/workflows/nas.yml` (`plan`, `image`, `cell`, `nas-shapes`) builds the commit's
  Linux payload into the server image for amd64 and arm64 and runs each cell of
  `tests/nas/cells.json`:
  - each cell gets its volumes, `server.toml`, headless setup and the server on the host network;
  - it then waits for ready, makes the libraries through the admin API and runs its assertions;
  - every cell's logs are an artifact, kept pass or fail, linked from the run's summary.
- The fixtures are 40 synthetic clips made on the runner (espeak-ng speech, Arabic file names,
  one HEVC clip, one Arabic subtitle); nothing is downloaded or committed. The device checklists
  for Synology, TrueNAS and Unraid are in `docs/nas/checklists` with a lint.
- Merged in 2e69e7501 with eight cells green: `uid-1000` (amd64 and arm64), `media-ro`,
  `media-foreign`, `mem-4g`, `mem-3.7g`, `mem-6g` and both no-Landlock profiles.
  - The tier checks ran against 59.04, which merged in 3fcb1c789: Light at 4096 and 3700 MiB,
    Standard at 6144 MiB, each limit read as Docker set it.
- Cells and assertions waiting for a story or a fix carry `needs` and turn on when it is added
  to `landed`:
  - 59.01 (#1090): the uid, refusal and unnamed-uid cells;
  - 59.03 (#1092): `media-none` and the access checks;
  - 59.02: Postgres PSS on the Light tier;
  - 59.07: the isolation level.

## What we learned
- The cells found four product faults, each now a pending check under a fix's name:
  - `scan-grant-race`: a scan asked right after a library is made walks nothing (the `light`
    runner's sandbox has no grant for the new root yet: EACCES, `files_walked=0`); the runner
    restarts with the grant a moment later;
  - `poster-queue`: in a fresh container no `content_hash` job is queued for scanned clips, and
    the poster stage follows only `content_hash`, so no poster is ever made;
  - `plugin-cache-grant`: the runners are refused `$HOME/.maktaba/cache`; in the image `HOME` is
    the volume, `/var/lib/maktaba`;
  - `pg-stale-lock`: after `docker kill` the bundled Postgres crash-loops on a stale
    `.s.PGSQL.54329.lock` in the data volume, its old PID reused in the new container's PID
    namespace.
- The tier's `shared_buffers` is not yet passed to Postgres (that is 59.02's wiring), so the
  Postgres memory check waits for 59.02, not 59.04.
- Container setup must run before the server container exists: `maktaba-server container` starts
  the launcher as soon as `server.toml` appears, and setup then says the machine is set up
  already. Write `server.toml`, run `setup --answers` in a throwaway container (as
  `docker compose run --rm maktaba setup` does), then start the server.
- `--answers-fd 0` means unset; pass `--answers /dev/stdin` with `docker run -i`.
- Mount a fresh volume at `/var/lib/maktaba` before chowning it. Docker copies the image's folder
  into an empty volume on its first mount there, owner 1000 included.
- Setup's roots only feed the folder picker: libraries are made by `POST /api/libraries`.
- The access token lasts minutes; a client in a long wait signs in again on a 401. The login
  route allows 10 a minute, so wait on the public health route first.
- On a developer's Mac, check that no other program listens on a port before publishing a
  container on it. Ports 18080 and 18088 belonged to another local Maktaba, and its 500s looked
  like a bug in the image.
- Python inside a single-quoted bash block cannot carry escaped quotes. The verdict's inline
  script failed with every cell green; keep such code in a script with its own test.

## Keep doing
- Imitate a NAS shape in a container before any device is bought, and keep the logs of every
  run, pass or fail.
- Read a sibling story's merged code before writing the assertion that targets it. The machine
  route moved to `/api/server/machine`, and the Postgres tier setting was not wired yet.

## Open
- Turn on, in follow-up pull requests, each pending check once its fix or story lands:
  - `first_scan` (#1095);
  - `restart-after-kill` (fix/epic59-pg-stale-lock);
  - `thumbnail` (fix/epic59-poster-queue);
  - `logs_clean` (fix/epic59-runner-cache-grant);
  - the 59.01 cells (#1090) and the 59.03 cells (#1092).
- Later phases add cells: `vaapi-fallback` (59.05), arm64 speech (59.06), the isolation level
  (59.07), `bridge` and `offline` (59.08, 59.01), and the update legs (59.12).
- The device checklists stay `draft` until their first run on a device (README Q6).
