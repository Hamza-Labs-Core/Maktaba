# Story 59.05 — Hardware video on NAS chips: VA-API, Quick Sync and the render device

> Epic 59 · Phase 2 · Depends on: 59.01, 59.04 · Needed by: 59.09, 59.10, 59.11

## Why

Streaming tries NVENC, then QSV, then libx264, keeping each only after a test encode
(`streaming/internal/ffmpeg/hwaccel.go`). Most NAS chips give a container only `/dev/dri`:
- the J4125 in Synology Plus models;
- the N100 and N5105 in UGREEN, QNAP and ASUSTOR boxes;
- AMD APUs in some TrueNAS builds.

On those devices VA-API is the path that works most reliably. QSV in a container also needs
Intel's media driver, and the image does not carry it. So today every NAS transcodes in software.
A J4125 manages perhaps one 1080p software transcode, and an ARM NAS none at real time.

## Description

**The image carries the drivers.**
- **amd64:** `intel-media-va-driver-non-free` (iHD, Broadwell and newer: J4125, N100) from Debian
  non-free, `i965-va-driver` (older Intel) and `mesa-va-drivers` (AMD).
- **arm64:** nothing, since ARM NAS chips expose no VA-API to containers.
- `vainfo` is included for diagnostics.
- **Licence check before merge:** iHD's non-free package is redistributable. Its licence is
  recorded in `THIRD_PARTY_NOTICES`.

**VA-API in streaming.**
- `hwaccel.go` gains `vaapi`, tried after `nvenc` and `qsv`. The order can be changed with
  `MAKTABA_HWACCEL_PREFER`.
- The device is the first `renderD*` that `vainfo --display drm --device <d>` opens. Its
  profiles decide what is offered:
  - H.264 encode: `VAProfileH264*` with `VAEntrypointEncSlice*`;
  - HEVC encode when listed;
  - decode per profile.
- The pipeline is:
  `-hwaccel vaapi -hwaccel_output_format vaapi -vaapi_device <d>` → `scale_vaapi` →
  `h264_vaapi`, with a software decode fallback when the source codec's decode profile is
  missing (10-bit H.264, AV1 on older chips).
- Burned-in subtitles download frames to the CPU for the overlay
  (`hwdownload,format=nv12 … overlay … hwupload`). Otherwise text subtitles are kept as WebVTT
  renditions, as today.
- The **test encode** (a 2-second lavfi source, 1080p) must pass before VA-API is kept, exactly
  as for NVENC and QSV. The result and the reason for a failure are stored and shown in Settings
  → This machine:
  - "Video: Quick Sync (VA-API), H.264 and HEVC";
  - "Video: software. The render device /dev/dri/renderD128 could not encode: {error}".

**QSV stays** for setups where it works (oneVPL/libmfx with the iHD driver). Where both pass,
VA-API is preferred on NAS platforms because it uses less memory. QSV stays first on desktops.

**Permissions for the device.**
- `/dev/dri/renderD128` is owned by group `render` (or `video`), and that group's gid differs per
  NAS.
- The compose files pass `devices: [/dev/dri:/dev/dri]` and `group_add: ["<render gid>"]`, with
  the install guide's command to find it (`stat -c %g /dev/dri/renderD128`).
- When the device exists but cannot be opened, Settings shows "The render device is there, but
  user {uid} can't open it: add group {gid} to the container (group_add)."

**Software transcoding limits.**
- With no usable GPU, the tier's limits apply (59.04): on Light, one transcode at ≤ 720p; on
  Standard, two at ≤ 1080p.
- Players are told "this home transcodes in software" through the existing capabilities, so they
  prefer direct play and remux (Epic 58's device-reported capabilities).
- A transcode beyond the limit waits with "Another video is being converted on this home"
  rather than stuttering.

## Acceptance criteria

- **Given** an Intel N100 host with `/dev/dri` passed and the render gid added, **then** Settings
  shows VA-API with H.264 and HEVC, and a 1080p HEVC → H.264 transcode runs at ≥ 2× real time.
- **Given** `/dev/dri` passed without the render gid, **then** video falls back to software, and
  Settings shows the group_add fix.
- **Given** no device, **then** the VA-API test encode is skipped with "no render device", and
  libx264 is used.
- **Given** the Light tier with no GPU, **when** a second transcode is requested, **then** it
  waits with the message, and the first is not slowed.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | fake `vainfo` output (iHD, J4125) | parse | H.264 enc, HEVC enc, decode list |
| T02 | Go unit | fake `vainfo` (mesa, AMD APU) | parse | H.264 enc |
| T03 | Go unit | profiles without the source's decode | build args | software decode, VA-API encode |
| T04 | Go unit | burned-in subtitle | build args | hwdownload/overlay/hwupload chain |
| T05 | Go unit | device present, open EACCES | detect | software + group_add reason |
| T06 | CI container (59.15) `vaapi-fallback` | no device, prefer vaapi | start | libx264, reason recorded |
| T07 | Go unit | Light, one transcode running | request a second | queued with message |
| T08 | Device checklist | DS425+ (J4125), N100 box | play 1080p HEVC needing transcode | VA-API in Settings, smooth playback, speed logged |

## Strings

| Key | English | Arabic |
|---|---|---|
| `machine.video.vaapi` | Video: hardware ({driver}), {codecs} | الفيديو: عتادي ({driver})، {codecs} |
| `machine.video.software` | Video: software | الفيديو: برمجي |
| `machine.video.device_failed` | The render device {device} could not encode: {error} | تعذّر على جهاز العرض {device} الترميز: {error} |
| `machine.video.group_add` | The render device is there, but user {uid} can't open it: add group {gid} to the container (group_add). | جهاز العرض موجود، لكن المستخدم {uid} لا يستطيع فتحه: أضف المجموعة {gid} إلى الحاوية (group_add). |
| `stream.wait_transcode` | Another video is being converted on this home. Yours will start when it finishes. | يجري تحويل فيديو آخر على هذا الجهاز الرئيسي، وسيبدأ الفيديو الخاص بك عند انتهائه. |

## Edge cases

- **Two render devices** (iGPU + discrete): the first whose test encode passes. An admin can set
  `[streaming] render_device`.
- **The J4125 HEVC on DSM 7.2.2+** (research.md §2.3, unverified): the device checklist records
  whether HEVC decode works in the container. When it does not, VA-API encodes H.264 from a
  software decode.
- **NVIDIA on TrueNAS 25.10** with a dropped card (Pascal and older): NVENC's test encode fails,
  and VA-API or software takes over with the reason shown.

## Build plan

| File | Change |
|---|---|
| `streaming/internal/ffmpeg/hwaccel.go` | `vaapi` backend, profile parsing, test encode |
| `streaming/internal/ffmpeg/args.go` | VA-API filter chains, subtitle overlay path |
| `streaming/internal/session/` | software transcode limits per tier, wait message |
| `deploy/docker/server/Dockerfile` | VA drivers, vainfo (amd64) |
| `THIRD_PARTY_NOTICES` | iHD licence |
| compose files (59.09–59.11) | `/dev/dri`, `group_add` |
