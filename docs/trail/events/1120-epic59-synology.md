---
schema: 1
kind: pr
pr: 1120
title: "Maktaba on a Synology NAS: the Container Manager project and maktaba.tv/install/synology (story 59.09)"
date: 2026-11-11
session: a02dd548-7558-4922-8576-9cb8ffe75e6c
agent: claude-code
---

## What happened
- #1120 (merged at da53322ca) adds maktaba.tv/install/synology and /ar/install/synology to the install site (`web/packages/install`, beside 59.11's /install/docker). The page has a model table (J4125 models with Quick Sync, Ryzen in software, 2 GB ARM as storage only), a form, five DSM steps, After setup and Troubleshooting. The form asks for the model, memory, the DSM user's uid, Quick Sync and the render group, the data volume, the shared folders and the time zone, then writes the Container Manager project's YAML in the page. It is compose.ts's fill-in plus `MAKTABA_PLATFORM=dsm`, and a test asserts no fetch.
- `deploy/nas/generate.py` makes `deploy/nas/synology/compose{,.j4125}.yaml` from the published compose with the same block rule (`tests/nas/install_files.py`'s `enable`: `[memory]`, plus `[dri]` for the J4125 file). `synology.test.ts` checks that the page's defaults give exactly those files, and `test_synology.py` runs `--check`. Release attaches both files, pinned to the tag (`--tag vX.Y.Z --out dist`). No SPK (§12 Q3 default).
- On DSM, a data folder the user cannot write stops the container with DSM's screens in English and Arabic (`container.write_dsm`). The shared folder is read from the mount's root (`/@syno/docker/maktaba` gives docker); a named volume falls back to the guide's folder. Settings → This machine words a missing render group in Container Manager's terms (`machine.video.group_add.dsm`).
- nas-shapes cells: `dsm` (1026:100, 6 GB, no Landlock, a read-only share at `/volume1/Movies` with real clips in `@eaDir`, `#recycle` and `#snapshot`, and a share it has no permission on), `synology-compose` and `dsm-data-denied`. `synology-compose` runs the committed file unchanged on Container Manager's Docker 24.0.2: `docker:24.0.2-dind`, the commit's image loaded under the file's image name, the file's host paths made inside it, `compose run --rm maktaba setup`, then `compose up`. All three passed on the merged head.
- The device checklist (`docs/nas/checklists/synology.md`) covers the DS425+ (steps 1 to 16, with 59.08's discovery step as 16), the DS923+ (17), the DS223 (18) and the guide's DSM screenshots (19 to 23). The page shows a screenshot only once it is marked ready in `synologyGuide.ts`.

## What we learned
- The guide was first built in `web/cloud` while 59.11 was open. When #1117 settled that maktaba.tv's install pages live in `web/packages/install`, it moved there, and the Synology files moved to #1117's block rule. Before building a page for a site another open story is shaping, check where that story puts it.
- Two features can share a cell key by accident. #1117's compose cells use `"compose": {"blocks": [...]}`, and the Synology cell had used `"compose": {"file": ...}`. `main` would have sent `compose-plain` down the dind path, and a `compose()` method shadowed the new `compose` property. The Synology cell is now `compose_file` (`DindComposeCell`). The merge applied with no textual conflict; only reading the code found it.
- `docker:dind`'s `ps` is busybox's, with no `uid` column, so `docker top -eo uid` fails there. `only_uid` falls back to the `user` column: busybox prints a number for a uid with no name, and a name (root) counts as a failure.
- A PR that conflicts with main runs no `pull_request` CI. Green checks shown for it are an older head's. After every push, check that the PR is MERGEABLE and that the run is for the pushed sha.
- CodeQL flagged an image `src` built from the page's language (DOM text reinterpreted as HTML). Building it only from a fixed list and a literal `en`/`ar` clears it.
- In Arabic text a path is reordered at its slashes ("/dev/dri." shows as "dev/dri/."). The page wraps each path in U+2066/U+2069. A slash after a letter or a colon ("Read/Write", `http://`) is not a path start, and a whole command or URL is isolated as one run. Measure the positions with a Range before judging a bidi screenshot: one "glitch" was a misreading.
- A Vite dev server can keep serving a stale module after an edit; restart it with `--force`. Prettier rewrites `⁦` escapes as the literal characters, so a later text replacement that searches for the escape matches nothing.
- In a container 59.08's `serve` runs every role, so a taken 8088, 8080 or 8081 moves into 8780-8790 and the log and Settings → This machine say so. A single role would stop instead.

## Keep doing
- Derive each NAS file from the published compose with the shared block rule, and test that the install page gives exactly the committed files.
- Run a NAS's own file on that NAS's Docker engine version in CI (dind), not just `compose config`.

## Stop doing
- Ending a turn while the PR is DIRTY and trusting the checks list: GitHub skipped the PR run twice here.

## Open
- Owner actions: take the guide's DSM screenshots on a DS425+ (checklist steps 19 to 23), add them under `web/packages/install/public/install/synology/` and mark them ready; record whether DSM has an Arabic UI; run the device checklist on a DS425+, a DS923+ and a DS223.
- cgroup v1 (DSM's 4.4 kernels) cannot be imitated on GitHub's cgroup v2 runners. 59.04's unit tests read it, and checklist step 13 records it on a device.
- The `plugins-sandbox` timing test `a_wait_for_a_slot_is_not_the_jobs_time` failed once here and on main (run 42921993906); it passed on re-run.
