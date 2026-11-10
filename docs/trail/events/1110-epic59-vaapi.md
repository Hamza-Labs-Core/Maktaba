---
schema: 1
kind: pr
pr: 1110
title: "VA-API on NAS chips, the tier's transcode limits, Settings' video line (story 59.05)"
date: 2026-11-10
session: bfbe1fc1-fe5c-4d94-b0ba-d4194c63c967
agent: claude-code
---

## What happened
- #1110 (merged at 4f595fec1) adds VA-API to streaming (`streaming/internal/ffmpeg/vaapi.go`). It parses vainfo (the driver from the library libva opened; H.264 and HEVC from the EncSlice entry points; decode profiles with their 10-bit ones). The render node is probed in order: opened read-write (EACCES or EPERM gives `group_add`), then vainfo, then a 2-second 1080p `h264_vaapi` test encode. The probe order is NVENC, QSV, VA-API, with VA-API before QSV on a NAS or in a container (`MAKTABA_HWACCEL_VAAPI_FIRST`, set by the supervisor from hostinfo's platform). `[streaming] render_device` picks a node.
- Pipeline: `-vaapi_device` and `-hwaccel vaapi -hwaccel_output_format vaapi`, then `scale_vaapi` (FitScale's expressions, to nv12), then `h264_vaapi`. The CPU decodes, and each rung uploads, when the device lacks the source's decode profile or the source is tone-mapped. libx264 rungs download after the device's scaler. `EncodePlan.Burn` carries a burned-in overlay through `hwdownload … hwupload`; no caller sets it yet.
- 59.04's transcode defaults are now applied: the supervisor passes `MAKTABA_TRANSCODE_{SOFTWARE_MAX,SOFTWARE_MAX_HEIGHT,HARDWARE_MAX}` and the allocator's cap follows the live verdict. A software ladder is capped at the tier's height. A full slot set answers 503 `transcode_busy` (Retry-After 10), and the web player waits with `stream.wait_transcode` and asks again. Capabilities carry `software_only`.
- Streaming writes its verdict to `<state>/streaming/video.json`. The supervisor's `/config/machine` includes it as `video`, and Settings → This machine shows the video line and why it is software, in English and Arabic.
- Image (amd64): Debian `non-free` is added for `intel-media-va-driver-non-free` alone, plus `i965-va-driver`, `mesa-va-drivers` and `vainfo`. The iHD licence is Expat; it is non-free because Intel's prebuilt GPU kernels ship without source (`THIRD_PARTY_NOTICES`). Checked in a `debian:12-slim` amd64 container before pushing: the drivers land and ffmpeg lists `h264_vaapi` and `hevc_vaapi`.
- NAS cells `vaapi-fallback`, `vaapi-fake-device` and `vaapi-no-group` (`landed` += 59.05). The device checklists' J4125 (Synology 8, 9) and N100 (TrueNAS 8) steps were updated. The native apps' wait (Android, Apple, desktop player) went in a parallel PR, #1111.

## What we learned
- `affected-tests` blocked the first run: `api/internal/router/adapters.go`, `streaming/internal/config/config.go` and `streaming/main.go` had no test changed. Tests were added for each; the gate run locally (`cd tools/affected-tests && go run . gate -C ../.. --range origin/main...HEAD`) passed before the push.
- The three VA-API cells failed their first run. Their video assertions passed, but `transcode_auto` found no HEVC clip: the cells had no `scan`. `vaapi-no-group` also saw `device_failed` instead of `group_add`: `/dev/null` is in Docker's default device allow list, so `--device /dev/null:…:r` stayed openable read-write. The host's `/dev/kmsg` (0644 root, `:r`) gives EACCES to the container's user.
- The owner's Mac ran out of disk mid-session (ENOSPC on the temporary directory: no command could start). The session's own build outputs (a second worktree, two node_modules folders, a pulled image) were removed, freeing about 6 GB. Most of the volume's use is not this session's.

## Keep doing
- Run `tools/affected-tests` locally before the first push of a change that touches wiring files (`main.go`, adapters, config): it names each file that needs a test.
- A NAS cell that transcodes asserts `scan` first, and a device cell picks a node outside Docker's default allow list (`/dev/null`, `/dev/zero`, `/dev/random`, `/dev/tty` are always rwm).
- Check a new apt source and its packages in a throwaway container of the image's base before pushing a Dockerfile change.

## Open
- Real VA-API (the J4125's and N100's encode speed, HEVC decode in the container on DSM 7.2.2+) is untested until the device checklists run.
- `EncodePlan.Burn` has no caller: the API's `burn_subs` reaches no transcode today.
- The Android TV app shows the new sentence on its error screen but does not retry by itself.
