# Story 13.19 — The desktop Player reports what it plays (`device_caps`)

> Epic 13 · Build · Follows 13.18 (PR #1591) · Depends on: 13.18 (the Player plays with
> libmpv) · Size: M

## Why

Story 13.18 opens libmpv sessions with the `desktop-mpv` profile alone. That profile lists what
mpv decodes in software on any machine: H.264, HEVC (10-bit too), VP9, AV1 and MPEG-2 up to
2160p, and eight audio channels. It sent no `device_caps` report, because `DeviceCaps` has no
field for hardware decoding, and the decoder in use went to the problem report instead.

The profile alone is right for a fast machine and wrong for a slow one. A laptop with no
hardware AV1 or HEVC decoder and a modest CPU gets a 4K AV1 or HEVC 10-bit file directly, and
mpv drops frames all the way through; the home would have sent it a smooth 1080p H.264
transcode had it known. The other way round, a transcode the home makes for this Player (a
forced one, a bitrate cap over the relay) folds the sound to stereo even when the machine has
5.1 speakers. The Android Player solved both with `device_caps` (#1521: `DeviceCaps.kt`,
`DeviceCapsReader.kt`); the desktop Player does the same with what mpv can tell it.

## Description

**The probe.** At start, off the UI thread, the Player runs a probe on its own libmpv handle
(`vo=null`, `ao=null`, `untimed=yes`, `audio=no`), never the watch screen's:
- It plays short clips the Player carries (`resources/probe/`, 24 frames of 2160p each, each
  under 100 KB, made once by `libmpv/probe-media.sh` and committed): H.264 High, HEVC Main and
  Main 10, VP9 profiles 0 and 2, AV1 Main 8-bit and 10-bit, MPEG-2.
- **Hardware:** with `hwdec=auto-copy-safe` (13.18's setting), each clip's `hwdec-current`
  after its first `playback-restart` says whether a hardware decoder took it. Anything but
  `no` is hardware.
- **Software speed:** each codec the hardware does not take is timed from the first
  `playback-restart` of its play (software from its first picture) to `end-file`, after one
  untimed warm-up of libmpv; a clip is played once. The frames per second at 2160p give
  the tallest picture it plays smoothly, scaled by pixel count: 2160 when software decodes
  2160p at 45 fps or more (1.5 times 30 fps), else 1440 at 45 fps scaled by 9/16, else 1080
  at 45 fps scaled by 1/4, else 720.
- **Cache:** the results are cached in the Player's own folder (`probe.json`), keyed by the
  GPU (its adapter name and driver version: DXGI's adapter description and the display
  driver's version on Windows; `/sys/class/drm/card*/device` vendor and device ids and the
  loaded kernel driver's version on Linux), the CPU model and libmpv's version. A key that
  changed (another GPU, a new driver, a new libmpv in an update) probes again. The probe
  takes under 5 s on the CI runners and is bounded at 20 s: a clip out of time counts as
  software at 720p, codecs not reached are listed without limits, and an incomplete probe is
  retried after a back-off.
- A video opened before the probe ends opens with the profile alone, as 13.18 does.

**The audio device's channels.** Measured with a 0.3 s silent 7.1 clip played to the system's
default output (`audio-channels=auto-safe`, volume 0) on the probe's handle, read from
`audio-out-params/channel-count`. Measured at start and again in the background each time a
watch screen closes, so a headset or HDMI receiver connected since counts at the next video,
never in the middle of one (13.18's edge case). A device mpv cannot open leaves the field out.

**The report.** `mpv/DeviceCaps.kt` builds the shared client's `DeviceCaps` (the Android
`:client` class, `toJson()` as Android sends it) from the probe:
- `video`: one entry per codec that decoded, with `max_height` (2160 in hardware, the measured
  height in software), `bit_depth` (10 when the 10-bit clip decoded) and the new `hardware`
  flag; a codec that did not decode at all is left out;
- `max_audio_channels`: the device's channel count;
- `containers`, `audio`, `hdr`: left out (the profile's lists stand: mpv reads every container
  and decodes every audio codec of the profile, and tone-maps HDR on any display).

It is sent with `desktop-mpv` only: not with `desktop-player` (an older home, or the JavaFX
engine), and not on a `force_transcode` reopen, as on Android.

**A home that refuses the report.** The shared `ApiClient.openStream` already retries once
without `device_caps` when a home's strict body reader answers 400 `json: unknown field
"device_caps"`, and stops sending it to that home (`homeTakesDeviceCaps`). The desktop Player
passes the report through the same call and gets the same behaviour: no new code, one test.

**What the report changes on the home** (`streaming/internal/capability/`):
- **Direct play is narrowed per codec.** `desktop-mpv` has no `Ceiling`, so `Apply` only
  narrows; `decide` already reads `heightFor` and `VideoBitDepths`. A 2160p AV1 file on a
  machine whose report says AV1 up to 1080p is transcoded to H.264 at 1080p (H.264's own
  reported height or less). No change to `decide` is needed; tests pin it for this profile.
- **`hardware` on `VideoCap`.** A new optional boolean in `devicecaps.go` (`json:"hardware,
  omitempty"`), parsed and kept in the report's `Key`, used by no decision. The verdict's
  `Reason` names it ("AV1 decoded in software up to 1080p"), so the session's detail on the
  home and the Player's problem report say why a file was transcoded. An older home ignores
  the key (`ParseDeviceCaps` ignores keys it does not know).
- **Channels in a transcode.** A transcode or audio re-encode for a `desktop-mpv` session keeps
  up to the report's channels: 6 or more give E-AC-3 5.1 at 640 kbit/s (as `hlsremux.go` does
  for the TV profiles), fewer give stereo AAC at 192 kbit/s; no report (or no channel count) keeps every
  transcode's audio as it is today, the encoder's AAC at the source's channels; E-AC-3 only for
  a source of 6 or more channels.
  Direct play is never narrowed by channels: mpv folds 5.1 and 7.1 down for stereo speakers.
- OpenAPI (`device_caps`' `VideoCap`, English and Arabic) documents `hardware` and that
  `desktop-mpv` takes a report.

**The problem report** carries the probe's results (each codec's hardware or software height),
the channel count and the cache key, beside 13.18's engine line.

**maktaba.dev.** The Apps page's Windows and Linux Player text says the Player tells the home
what the machine plays smoothly, in English and Arabic (no new screenshot: no screen changes).

## Acceptance criteria

- **Given** a machine whose probe finds no hardware AV1 decoder and software AV1 at 2160p
  below 45 fps scaled to 1080p, **when** a 2160p AV1 file is opened, **then** the session
  opens with a report giving AV1 a `max_height` of 1080 or less, and the home transcodes it.
- **Given** a machine with hardware HEVC 10-bit, **when** a 2160p HEVC 10-bit file is opened,
  **then** the report says `hevc`, 2160, `bit_depth` 10, `hardware` true, and it plays directly.
- **Given** a 5.1 output device, **when** the home transcodes a 5.1 file for this Player,
  **then** the transcode carries 5.1 audio; with a stereo device, stereo AAC.
- **Given** a home whose session body reader refuses `device_caps`, **then** the stream opens
  without it on the same call, and the Player sends no report to that home again.
- **Given** the cached probe and the same GPU, driver, CPU and libmpv, **then** the Player
  does not probe again; **given** a new driver version, **then** it probes again.
- **Given** the JavaFX engine or an older home (`desktop-player`), **then** no report is sent.
- **Given** a report, **then** the problem report lists each codec's result and the channel
  count.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `DeviceCapsTest` (desktop): recorded probe answers (hardware HEVC and VP9; software AV1 at 20 fps at 2160p; 6 channels) | build the report | `hevc` and `vp9` 2160 `hardware`; `av1` 1080 `bit_depth` 10; `max_audio_channels` 6; no `containers`, `audio`, `hdr` keys |
| T02 | unit | the height from software fps: 50, 30, 12, 5 fps at 2160p | compute | 2160, 1440, 1080, 720 |
| T03 | unit | the probe cache | same key; a changed driver; a changed libmpv version | no probe; probe; probe |
| T04 | integration (real libmpv, CI's Desktop Player job, as `MpvRealTest`) | Linux and Windows runners; the committed probe clips | run the probe | every codec decodes; `hwdec-current` read for each (`no` on the runners); the report built; under 5 s; the times logged |
| T05 | integration (real libmpv) | the runner's audio (a null sink on Linux, the default device on Windows) | measure channels | a count, or the field left out with no error |
| T06 | unit (`PlaybackMpvTest`) | the fake home lists `desktop-mpv` | open a video | the session body carries `device_caps`; not on the `force_transcode` reopen; not with `desktop-player` |
| T07 | unit (`ApiClientTest`) | the fake home answers 400 `unknown field "device_caps"` | open twice | the first open retries without it and plays; the second sends none |
| T08 | unit (Go) | `desktop_mpv_test.go`: `Apply` with a report of AV1 up to 1080 | decide a 2160p AV1 file; a 1080p one | transcode, its reason naming software AV1; direct |
| T09 | unit (Go) | a `desktop-mpv` transcode with reports of 6 and of 2 channels, and none | build the ffmpeg arguments | E-AC-3 6 channels 640k; AAC 2 channels; the default AAC with no `-ac` |
| T10 | unit (Go) | `ParseDeviceCaps` with `hardware` | parse, `Key` | read; a report differing only in `hardware` has another key |
| T11 | unit | the problem report with a probe | build it | the codecs' results, the channels and the cache key in its log |

## Edge cases

- A machine with no GPU driver, a VM or a remote desktop: every codec in software; the report
  still narrows heights by the CPU's speed.
- A hardware decoder that claims a codec and then fails on the real file: 13.18's
  `force_transcode` reopen covers it; the probe's next run (new key) or the clip's own failure
  leaves the codec out or in software.
- A laptop on battery decoding slower than the probe measured on mains: the probe is not run
  again for power; a later story may.
- The probe and a video at the same time: the probe uses its own handle and yields (it stops
  between clips while a watch screen is open, and resumes after).
- An audio device that reports more than 8 channels: the report sends 8 (the profile's).
- A home with `desktop-mpv` but from before `hardware`: the key is ignored; heights still narrow.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/…/mpv/Probe.kt` (new) | the probe on its own handle, the channel measurement, the cache keyed by GPU, driver, CPU and libmpv |
| `apps/desktop-player/src/main/kotlin/…/mpv/DeviceCaps.kt` (new) | the probe's answers to the shared `DeviceCaps` |
| `apps/desktop-player/src/main/resources/probe/`, `apps/desktop-player/libmpv/probe-media.sh` | the committed probe clips and how they were made |
| `apps/desktop-player/src/main/kotlin/…/Playback.kt`, `mpv/Engine.kt` | `deviceCaps` passed to `openStream` with `desktop-mpv` only; the problem report's lines |
| `apps/android/client/…/playback/DeviceCaps.kt` | `VideoCap.hardware` (optional, sent when known) |
| `streaming/internal/capability/devicecaps.go`, `matrix.go`, `desktop_mpv_test.go` | `hardware`; the verdict's reason; tests of narrowing for `desktop-mpv` |
| `streaming/internal/ffmpeg/` (the transcode's audio arguments) and its tests | channels by the report for `desktop-mpv` |
| `shared/api/openapi.yaml`, `openapi.json`, `openapi.ar.json` | `hardware`; `desktop-mpv` takes `device_caps` |
| `.github/workflows/_desktop-player.yml` | T04–T05 in the real-libmpv step |
| `web/packages/devportal/scripts/apps.mjs`, `docs/developers/contributing/playback.md` (and `.ar.md`) | the report, in English and Arabic |
