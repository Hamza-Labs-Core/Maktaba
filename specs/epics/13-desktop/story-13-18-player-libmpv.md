# Story 13.18 — The Player plays with libmpv: direct play, reported to the home

> Epic 13 · Build · Decided in story 13.9 (D4, approved 2026-11-24) · Depends on: 13.15 (the
> Player on the JetBrains Runtime) · Size: L

## Why

The Player for Windows and Linux plays through JavaFX's media stack, which decodes H.264 and
AAC in MP4 or HLS and nothing else. Its sessions open with the streaming service's
`desktop-player` profile (`streaming/internal/capability/matrix.go`: H.264, AAC, stereo,
1080p), so the home remuxes or transcodes every MKV, every HEVC file and every AC-3 or 5.1
track before the first frame. That is the slow start the Android Player had before #1521: the
first frame waits for ffmpeg's first segment, the home's processor works for every viewer,
and the sound is folded to stereo.

libmpv (mpv's player as a library, on FFmpeg's decoders) plays those files as they are, with
the machine's hardware decoders where it has them, and draws text and picture subtitles
itself. The Player stays Kotlin and Compose, so `:client` stays shared with the Android apps;
libmpv is called through JNA.

## Description

**Playback engine.** A `MpvPlayer` behind the Player's existing playback interface, replacing
the JavaFX `MediaView` (`VideoSurface.kt`, `Playback.kt`, `Watch.kt`):
- one libmpv handle per watch screen, created and destroyed with it; every call off the UI
  thread; mpv's events (`playback-restart`, `end-file`, `property-change`) mapped to the
  Player's state;
- `hwdec=auto-safe`, so a machine without hardware decoding still plays in software;
- subtitles drawn by mpv (libass: ASS, SRT, WebVTT, PGS), with Maktaba Text as the subtitle
  face so Arabic shapes and joins; the Player's subtitle size and colour settings set mpv's
  `sub-*` options;
- audio and subtitle tracks chosen through mpv's track list; the HLS proxy's audio-group
  rewriting is no longer needed for mpv and stays only for the fallback engine.

**Drawing the video under Compose's controls.** The first task is a spike that settles it,
measured on Windows 11 and Ubuntu (Wayland and X11) with a 4K HEVC 10-bit file:
- (a) mpv's render API into an OpenGL texture that Compose draws (Skiko on OpenGL), the
  controls drawn over it as any Compose content; or
- (b) mpv drawing into a native child window (`wid`: an HWND, an X11 window; XWayland under
  Wayland), the controls in a transparent child window above it.
Picked by time to first frame, frame drops, CPU and how the controls look; recorded in the
pull request.

**Telling the home what it plays (as #1521 did on Android).** Sessions open with a new
`desktop-mpv` profile and a `device_caps` report:
- `matrix.go` gains `desktop-mpv`: containers MKV, MP4, MOV, TS, WebM and HLS; video H.264,
  HEVC (8 and 10 bit), VP9, AV1, MPEG-2; audio AAC, AC-3, E-AC-3, DTS, FLAC, Opus, Vorbis,
  MP3, up to 8 channels; HDR10 passes when the display does (else tone-mapped by mpv);
- the report names what this machine decodes in hardware (mpv's `hwdec-current` after a probe
  at start, cached per GPU and driver) and the audio device's channel count, so the home
  sends 5.1 to a 5.1 device and a stereo device gets the same file, folded down by mpv;
- a home older than `desktop-mpv` gets `desktop-player` and transcodes as today;
- a direct file that fails to open or decode opens once more as `force_transcode`, as on
  Android.

**Bundling per OS** (no system libmpv is used):
- **Windows x86_64:** `libmpv-2.dll` with its FFmpeg, from a pinned build, its SHA-256
  checked by `tools/release/fetch-pinned.sh`, inside the Player's install folder;
- **Linux x86_64 and aarch64:** a pinned `libmpv.so.2` with its FFmpeg, built in CI from the
  pinned sources, inside the `.deb` and `.rpm` (distributions' own libmpv differ in which
  codecs they enable);
- JNA loads the library by its absolute path in the install folder, never from `PATH` or the
  working directory.

**Licences.** libmpv built with `-Dgpl=false` (LGPL-2.1 or later) and FFmpeg without
`--enable-gpl` and `--enable-nonfree` (LGPL-2.1 or later), loaded dynamically, so the Player's
own licence is unchanged. Each build's configure line, licence texts and source location go in
`THIRD_PARTY_NOTICES` and the installers; a check fails the build when a bundled library
reports a GPL or non-free configuration. Codecs are the same FFmpeg software decoders the home
already ships.

**Update and rollback.** libmpv ships inside the Player's package, so it updates with the
Player (`latest-player.json`, signed with the apps' key, checked before it runs). For one
release the JavaFX engine stays inside, behind a setting the support page can name: when
libmpv cannot load (a missing DLL, a CPU or driver it cannot use) the Player says so once,
opens with JavaFX and `desktop-player`, and its problem report records why. Going back a
version is the previous release's installer, as today; nothing outside the package changes,
so it needs nothing else. The JavaFX engine and dependency leave in the release after.

**maktaba.dev.** The Apps page's Windows and Linux Player text says what plays directly, in
English and Arabic, with new screenshots.

## Acceptance criteria

- **Given** a 1080p H.264 MKV with AC-3 5.1, **when** it is played from the test home, **then**
  the Player asks for no playlist, opens the file directly, and its first frame shows in under
  1.5 s on the Linux CI runner (T01).
- **Given** a 4K HEVC 10-bit MKV, **then** it plays directly; with no hardware decoder it plays
  in software and the report says so.
- **Given** a home without `desktop-mpv`, **then** the session opens with `desktop-player` and
  plays the transcode.
- **Given** a direct file that fails to decode, **then** it opens once as `force_transcode`
  and plays.
- **Given** libmpv cannot load, **then** the Player plays with JavaFX, says so once, and the
  problem report records the reason.
- **Given** the Windows and Linux packages, **then** each carries the pinned libmpv and FFmpeg,
  their checksums match the pins, their configuration is LGPL, and the notices list them.
- **Given** Arabic subtitles, **then** they are drawn joined and right to left in Maktaba Text.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration, time to first frame | Linux and Windows CI; a 120 s 1080p H.264 / AC-3 5.1 MKV made by ffmpeg in the job; the fake home | open, wait for mpv's first `playback-restart` | direct URL, no playlist request, first frame < 1.5 s (Linux), the time logged for both OSes; the JavaFX + transcode time logged beside it |
| T02 | integration | the same with HEVC 10-bit | open | direct; `hwdec-current` reported |
| T03 | unit | `matrix.go` | resolve `desktop-mpv` against MKV/HEVC/AC-3, against DTS-HD MA 7.1 | direct; the second within the report's channels |
| T04 | unit | an old home's capability answer | open | `desktop-player` |
| T05 | integration | a file mpv cannot decode | open | one `force_transcode` reopen, then playing |
| T06 | unit | libmpv absent | start the watch screen | JavaFX engine, one notice, the reason in the report |
| T07 | build | the packages | inspect | pinned checksums; `mpv --version` and FFmpeg's configuration show no GPL or non-free |
| T08 | screenshot | the watch screen, en and ar, with subtitles | capture | matches its reference (13.17) |

## Edge cases

- A remote desktop session or a VM without OpenGL: mpv's software output; the spike states
  what option (a) needs there.
- A laptop switching GPUs, or a driver update: the hardware probe's cache is keyed by GPU and
  driver, so it probes again.
- Bluetooth or HDMI audio connected during playback: the channel count is read again at the
  next session, not mid-file.
- Very large files over the relay: direct play streams them; seeking uses ranges as today.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/build.gradle.kts`, `gradle/desktop.versions.toml` | JNA; the pinned libmpv per OS as a packaged resource; JavaFX kept for one release |
| `apps/desktop-player/src/main/kotlin/…/mpv/` | the JNA binding, `MpvPlayer`, the video surface chosen by the spike, the hardware probe |
| `apps/desktop-player/src/main/kotlin/…/Playback.kt`, `Watch.kt`, `VideoSurface.kt` | the engine choice, `desktop-mpv` and the report, the `force_transcode` reopen |
| `streaming/internal/capability/matrix.go` and its tests | the `desktop-mpv` profile |
| `tools/release/` (fetch and pins), `.github/workflows/_desktop-player.yml`, `desktop-release.yml` | fetch or build, check and package libmpv; T01–T02 in CI |
| `THIRD_PARTY_NOTICES`, the installers' licence page | libmpv and FFmpeg |
| `apps/desktop-player/README.md`, `web/packages/devportal/scripts/apps.mjs` | what plays directly, en and ar |
