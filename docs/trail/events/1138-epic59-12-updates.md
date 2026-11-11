---
schema: 1
kind: pr
pr: 1138
title: "Updates on a NAS: the image beside its runtime, and the update rehearsal's container legs (story 59.12)"
date: 2026-11-11
session: 223ddf86-86e1-4f1a-b23b-b70cb772ad49
agent: claude-code
---

## What happened
- #1138 (merged at d1a9ae0f2) makes the Docker image's self-update and a NAS catalog's moves of its tag (TrueNAS app updates, Unraid, Watchtower, a pull) live side by side. The entrypoint logs "The container image (N) is older than this home's runtime (N+1), so the runtime is running." and passes `MAKTABA_IMAGE_VERSION`/`MAKTABA_IMAGE_REFUSED` to the server. `GET /api/system/updates` gains a `container` block (runtime, image, running, reason) for admins only; the refused record is read live from `$MAKTABA_LAUNCHER_ROOT/updates/image-refused.json`. Admin → Updates shows it, English and Arabic.
- A container's update preflight checks the data volume for the runtime (1 GiB) and the dump (database × 1.2): short, the run fails `disk_short` with `need_bytes`/`free_bytes` and waits for a person. On Light and Standard the pipeline stops before the pre-update dump (`Machine.Lighten`) and starts again when the update goes no further (`Relight`).
- `update-rehearsal.yml` gains `nas-images` (per architecture: this commit's image, `next` with its version raised by `tests/nas/reversion.sh`, and `bad`, whose server exits at `serve`, both as small layers over `cur`) and `update-rehearsal-container`, five legs from `tests/nas/update_legs.json` run by `tests/nas/update_leg.py`: nas-568, nas-4g (20,000 videos), nas-arm64, nas-restart (restart at staged, moved, before confirm), nas-image-older. Each updates from the previous published image, rolls `bad` back under the guard (not tried again on the next start) and finds the home as it was. The verdict requires them. All five passed on the merged head; nas-4g peaked at 919-947 MiB of 4 GiB, no OOM kill.
- The compose comment, docs/updates.md ("Updates on a NAS"), docs/update-rehearsal.md, install-kinds.md, maktaba.dev and the maktaba.tv Docker page now say both update paths stay on. `tests/nas/test_install_files.py` checks no NAS file sets `MAKTABA_UPDATE_DISABLE`, the copied files name `:latest`, and release pins the compose, the Synology files and the TrueNAS app.

## What we learned
- **A real bug the nas-4g leg found:** the container keeps a newer image once its server has answered `/api/system/version` for 3 minutes, asking every 10 s on loopback. On a busy home those checks were refused 429 every few tries (the plugin runners' `/internal/plugins/invocations` calls share the loopback's rate limit: 560 refusals in one run), and each refusal restarted the wait, so the image was never kept and the guard would have gone back after 2 hours. A check with no answer now says nothing; only another version restarts the wait (`TestContainerConfirmKeepsThroughUnansweredChecks`).
- A harness that polls the API on a large library hits the per-user rate limit: paging `/api/videos` for 20,000 videos is 100 requests. cell.py's `under()` pages every library; the leg pages only the library that holds the folder, and reads the bulk count from `GET /api/libraries/manage` (`/api/libraries` carries no counts). Three CI rounds went to finding that.
- The rehearsal's test (`test_the_rehearsal_of_main_starts_after_ci_passes_on_the_same_commit`) required every job to need `kit`; a job carrying the kit's exact `if` now counts as gated, so the images build beside the kit rather than 45 minutes after it.
- CodeQL raised `actions/cache-poisoning/poisonable-step` (alerts 199-204) on `nas-images`. No step there saves a cache (go-cache and node-cache only restore, setup-uv `save-cache: false`, rust-cache `save-if: false`, `cache/restore`); dismissed as false positives with those facts, as with #737 and #842.
- A docker logs offset (the length read after a restart) keeps a restart test from matching a line an earlier start wrote.

## Keep doing
- Run a NAS shape with a large library in the update rehearsal: the 40-clip legs passed from the first run, and only the 20,000-video leg found the keep bug.
- Read each leg's `report.json` before trusting a fast green: every step's detail names what it saw.

## Stop doing
- Treating a probe with no answer as a negative answer in a wait that must see a steady state.

## Open
- The loopback's per-IP rate limit refuses the home's own plugin invocations (`/internal/plugins/invocations`, 560 × 429 under a 20,000-video scan): local processes probably should not share the clients' limit.
- The container legs drive the image path (a newer image at start). The in-window self-update inside a container (the release download, verified in process) is not rehearsed in a container; the headless runtime installer it shares is.
- `GET /api/system/updates` and its `container` block are not yet in `shared/api/openapi.json` (story 59.16).
- The kit and seed jobs stay amd64; the container legs build their own per-architecture images, so the story's "arm64 kit and seed" was not needed.
