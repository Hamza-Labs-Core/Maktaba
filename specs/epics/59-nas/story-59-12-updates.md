# Story 59.12 — Updates on a NAS: self-update beside catalog updates, the NAS leg of the rehearsal

> Epic 59 · Phase 4 · Depends on: 59.01, 59.10, 59.11, 59.15 (the container harness) · Needed by: 59.16

## Why

The image updates itself on its volume. The steps are:
- the release is verified in process;
- a new runtime is installed;
- `current` is moved under the launcher's guard, which confirms within 3 minutes or switches
  back;
- a pre-update dump is taken first.

Catalogs move the image too:
- TrueNAS app updates;
- Unraid's update check;
- Watchtower;
- a household pressing "pull" in Container Manager.

The image's rule makes the two safe: a newer image goes on the volume at its next start, and an
older one never does. But that rule was never rehearsed on NAS shapes. Those shapes are a uid
other than 1000, a 4 GB limit where the dump and the new runtime compete for memory and disk,
arm64, and a container restarted by the NAS in the middle of an update.

## Description

**One update path, two triggers.**
- **The self-update** runs in the household's update window (Epic 28), with the guard and the
  dump. It does not change the image tag; the next image pull is simply already applied.
- **A newer image** (catalog, pull) is put on the volume at its next start through the same
  install-and-guard path. An image older than the volume's runtime starts the volume's runtime
  and logs "the image is older than this home's runtime; running the runtime".
- **Settings → Updates on a NAS platform** shows the runtime version, the image version, and
  which one is running, with the reason.

**Tags.** No new tag.
- `:latest` already means the highest stable release (`release.yml`).
- Release already rewrites the published Docker compose to the pinned `:<version>`. The Synology
  files derived from it (59.09) carry the same pin, and the Unraid template uses `:latest`.
- The TrueNAS app pins `:<version>` and is bumped through the catalog (59.10).

**Disk and memory during an update.**
- **Disk:** the pre-update check needs free space on the data volume for the new runtime
  (≈ 1 GB) plus the dump (the database's size × 1.2). On a NAS the dump goes to the data volume,
  as today.
  - When space is short, the update waits with "Not enough free space on the data folder for a
    safe update: {need} needed, {free} free."
  - The update never proceeds without the dump.
- **Memory:** on Light and Standard, the update stops speech and transcode workers before the
  dump, and the launcher's guard starts them again after confirmation, so the update does not
  OOM under the memory limit.

**A restart in the middle.**
- A NAS can stop the container mid-update: DSM's "stop all containers" before a DSM update,
  TrueNAS app restarts.
- On the next start, the launcher finds the half-done state (a runtime staged, `current` not
  moved, or moved but unconfirmed) and resolves it as the guard would: an unconfirmed `current`
  goes back to `previous`. This is today's guard rule, proven here under NAS restarts.

**A container rehearsal with NAS shapes.** Today's `update-rehearsal.yml` runs Go end-to-end
tests of the Mac app's layout, not the Docker image. This story adds a new job,
`update-rehearsal-container`, on 59.15's container harness. It is required for tagging alongside
the existing rehearsal, and has these legs:

| Leg | Shape |
|---|---|
| `nas-568` | `--user 568:568`, media `:ro`, data group-writable |
| `nas-4g` | `--memory=4g`, Light, a 20k-item fixture database |
| `nas-arm64` | arm64 runner; the rehearsal's kit and seed jobs, amd64-only today, gain arm64 builds |
| `nas-restart` | `docker restart` at each step of the update (staged, moved, before confirm) |
| `nas-image-older` | volume on N+1, image N started (N+1 is a build of the commit under test with its version raised, as the existing rehearsal does for its forced bad release) |

- Each leg passes: update N−1 → N, then a forced bad release that the guard rolls back, then
  restore. These are the existing rehearsal's assertions, run against the image.
- The rehearsal stays the gate for tagging. No release path is tested on the live home (updates
  are proven in CI first).

## Acceptance criteria

- **Given** each NAS leg, **then** the update, the rollback and the restore pass, with logs kept.
- **Given** a home on runtime N+1 and the image N started, **then** N+1 runs, and Settings says
  why.
- **Given** free space below the need, **then** the update waits with the message, and nothing
  is changed.
- **Given** a container restart between staging and confirm, **then** the next start resolves to
  a working runtime, with no manual step.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | rehearsal `nas-568` | as table | update, rollback, restore | pass |
| T02 | rehearsal `nas-4g` | as table | update | no OOM kill; workers back after confirm |
| T03 | rehearsal `nas-arm64` | arm64 | update, rollback | pass |
| T04 | rehearsal `nas-restart` | restart at each step | next start | working runtime; state resolved |
| T05 | rehearsal `nas-image-older` | volume N+1, image N | start | N+1 runs; message |
| T06 | Go unit | free < need | pre-check | waits, message, no change |
| T07 | web unit | Updates panel on NAS | render | runtime, image, which runs, why |

## Strings

| Key | English | Arabic |
|---|---|---|
| `update.disk_short` | Not enough free space on the data folder for a safe update: {need} needed, {free} free. | لا توجد مساحة حرّة كافية في مجلد البيانات لتحديث آمن: المطلوب {need} والمتاح {free}. |
| `update.image_older` | The container image ({image}) is older than this home's runtime ({runtime}), so the runtime is running. | صورة الحاوية ({image}) أقدم من بيئة تشغيل هذا الجهاز الرئيسي ({runtime})، لذا تعمل بيئة التشغيل. |
| `update.runtime_vs_image` | Runtime {runtime} · Image {image} | بيئة التشغيل {runtime} · الصورة {image} |

## Edge cases

- **Watchtower with `MAKTABA_UPDATE_DISABLE=1`** (the household's choice): the image path only.
  The guard still applies on start.
- **The household pins an old tag:** the self-update keeps the volume current. Settings shows the
  pinned image as older, which is harmless.
- **A catalog update during the update window:** the launcher's lock serialises them. The second
  finds the work done.

## Build plan

| File | Change |
|---|---|
| `.github/workflows/update-rehearsal.yml` | the `update-rehearsal-container` job with the five legs; arm64 kit and seed |
| `tests/nas/` (from 59.15) | the update legs' scripts |
| `cmd/maktaba-server/cmd_container.go` | image-older message; Settings data |
| `cmd/maktaba-server/internal/updater/`, `internal/selfupdate/` | disk pre-check with the dump size; stopping heavy workers on Light/Standard |
| `api/internal/handlers/systemstatus/` | `GET /api/system/updates` stays public; the runtime and image fields go only to admins (59.16) |
| `web/src/i18n/{en,ar}.json`, `web/` | strings; Updates panel lines |
