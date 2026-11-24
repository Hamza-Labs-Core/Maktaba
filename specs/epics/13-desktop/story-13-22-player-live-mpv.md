# Story 13.22 — Live TV plays with libmpv, then JavaFX leaves the Player

> Epic 13 · Build · Follows 13.18 (PR #1591) · Depends on: 13.18 (the Player plays with
> libmpv); the removal also on 13.21 (the engine setting) · Size: M

## Why

After 13.18, videos play with libmpv but a channel still plays with JavaFX's `MediaPlayer`
(`live/ChannelScreen.kt` makes one per tune; `VideoSurface(MediaPlayer?)` draws it). The
Player therefore carries two engines for good, starts JavaFX on every launch, keeps the
Linux packages' dependency on the system's FFmpeg (`libavcodec`, which JavaFX's media plugin
loads), and opens libmpv with `RTLD_LOCAL` so its FFmpeg stays apart from JavaFX's. Story 13.18
said JavaFX leaves in the release after; releases are cut every 30 minutes, so that needs a
precise point, stated here.

## Description

**Part one: Live TV on libmpv** (its own pull request).
- `ChannelScreen` asks `Engine` for its players (13.21's choice and fallback, so the older
  engine still plays channels while it stays) and keeps a `Video` instead of a `MediaPlayer`;
  `VideoSurface(video)` draws it, as the watch screen does.
- `ChannelPlayback`'s `show` and `hide` stay its interface: `show(url)` opens the proxy's
  local address in the `Video` (`players.open(url, 0, autoplay = true, events)`), `hide()`
  disposes it. Its states (Tuning, Playing, Failed, Ended, Off, NotNow) are unchanged.
- mpv's options for a live HLS stream: no `start`, `keep-open=no` (the end of a live stream is
  the channel ending, not a frame to hold), `cache=yes` with `demuxer-readahead-secs=10`, and
  `hls-bitrate=max`. An `end-file` with an error is `ChannelStatus.Failed` (retryable); an
  `end-file` at EOF is `Ended`.
- **Changing channel** reuses the handle: `loadfile <url> replace`, not a new `MpvVideo`, so
  the screen shows the last frame until the new one starts. **Pause** sets `pause`; **Back to
  live** loads the same URL again from the live edge, as JavaFX's reopen did.
- Away from home the remote session's address still goes through the stream proxy, which
  writes the newest token into each relay request; a new token never reopens the player.
- The fallback: when libmpv cannot load, or the older engine is chosen, channels play with
  JavaFX as today.

**Part two: JavaFX leaves** (a second pull request, after part one has shipped).
- **Which release.** JavaFX leaves in the first release tagged from a main that contains the
  removal pull request. That pull request merges only when all of these hold, each checked in
  its description:
  - at least 30 days have passed since the first release tag that carried both 13.21's
    setting and part one;
  - no open issue reports a video or a channel that plays only with the older engine;
  - the Desktop Player job's real-libmpv tests (`MpvRealTest`, the live tests below) passed on
    Windows and Linux on every main run of the last 14 days.
  The CHANGELOG entry (English and Arabic) of that pull request says the Player has one engine
  from its version, and the help page's `#video-engine` section is rewritten to say so.
- **What leaves:** `FxVideo`, `FxPlayers`, `Playback.fx` and `onFx`, `VideoSurface`'s JavaFX
  overload and its `JFXPanel`, `Platform.setImplicitExit` and `Platform.exit` in `Main.kt`, the
  `org.openjfx` dependencies and `javafxPlatform` in `build.gradle.kts` and
  `desktop.versions.toml`, `MpvLoad`'s `MAKTABA_PLAYER_ENGINE` branch, 13.21's row (a stored
  `javafx` reads as **Automatic**), the stream proxy's audio-group rewriting kept only for
  JavaFX (`StreamProxy.audio`, `rewriteMaster`'s audio filter), OpenJFX in
  `THIRD_PARTY_NOTICES`, and `libavcodec` from the `.deb` and `.rpm` dependencies
  (`--linux-package-deps`).
- `STREAM_PROFILE` (`desktop-player`) stays: a home without `desktop-mpv` still answers with
  it, and libmpv plays that profile's streams too.
- **When libmpv cannot load after that**, nothing plays: the watch and channel screens show
  **Videos can't play: the Player's engine did not start ({reason}).** with **Learn more**
  (13.21's help section) and **Report a problem**; the problem report carries the reason.
- The installers shrink by JavaFX's size; the release legs check that no `javafx` jar or
  native library is left in a package.

**maktaba.dev and help.** Part one: the Apps page's Player text says channels play with the
same engine (no new screenshot: the channel screen looks the same). Part two: the help page
and the Apps page drop the older engine, in English and Arabic.

## Acceptance criteria

- **Given** a channel on the fake home (a live HLS of H.264 and AAC), **when** it is opened,
  **then** it plays with libmpv and its first frame shows in under 2 s on the Linux runner.
- **Given** a channel playing, **when** **Next channel** is chosen, **then** the same handle
  loads the next channel and its first frame shows in under 2 s, the last frame held meanwhile.
- **Given** **Pause** then **Back to live**, **then** the picture resumes from the live edge.
- **Given** the stream ends, **then** the screen shows the channel ended; **given** the
  stream fails, **then** it shows **Try again**.
- **Given** the older engine chosen or libmpv missing (before part two), **then** channels
  play with JavaFX.
- **Given** part two, **then** the packages carry no JavaFX, the Linux packages do not depend
  on `libavcodec`, and the Player starts and plays a video and a channel on a Linux machine
  without the system's FFmpeg.
- **Given** part two and a libmpv that cannot load, **then** the screens say so and offer
  **Report a problem**.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (`ChannelPlaybackTest`) | a fake `Players` recording calls | open, next, pause, back to live, close | one `open`, then `loadfile replace`, `pause`, a reload of the same URL, `dispose` |
| T02 | integration (real libmpv, CI's Desktop Player job) | `test-media.sh` makes a live HLS (ffmpeg's `hls_list_size` window, appended while the test runs) served by the fake home | open the channel | first frame under 2 s on Linux (asserted), the time logged on Windows |
| T03 | integration (real libmpv) | two such channels | change channel | the same handle; first frame of the second under 2 s |
| T04 | integration (real libmpv) | the live stream stops being appended, then its playlist ends; another run answers 404 | wait | `Ended`; `Failed`, retryable |
| T05 | unit | `Engine` with the older engine chosen (before part two) | open a channel | `FxPlayers` |
| T06 | build (part two) | the Windows and Linux packages | inspect | no `javafx` jar or native library; the `.deb` and `.rpm` dependencies list no `libavcodec` |
| T07 | integration (part two) | an Ubuntu runner with the FFmpeg libraries purged (`apt-get purge 'libavcodec*'`), the `.deb` installed | start with `xvfb-run`, play the 1080p test file and a channel | both play |
| T08 | unit (part two) | `MpvLoad.Missing` | open the watch screen and a channel | the message, **Learn more**, **Report a problem** |
| T09 | screenshot (13.17) | the channel screen playing, en and ar | capture | matches its reference (the same as JavaFX's within the tolerance) |

## Edge cases

- A channel whose stream switches resolution mid-stream (an HDHomeRun tuner after a programme
  change): mpv reconfigures its output; the frame size given by `resize` is the screen's, so
  nothing else changes.
- A slow relay away from home: `cache=yes` buffers; a stall longer than 15 s is `Failed`,
  retryable, as JavaFX's stall was.
- The remote session ended by the home while paused: `ChannelPlayback.ended()` as today.
- A person on the older engine when part two ships: the update installs the Player with one
  engine; the help section explains, and the problem report shows the stored choice was read
  as **Automatic**.
- A release rolled back to one before part two: the previous installer carries JavaFX again,
  as any rollback does; nothing outside the package changed.

## Build plan

| File | Change |
|---|---|
| `apps/desktop-player/src/main/kotlin/…/live/ChannelScreen.kt`, `live/ChannelPlayback.kt` | a `Video` from `Engine`; `VideoSurface(video)` |
| `apps/desktop-player/src/main/kotlin/…/mpv/MpvVideo.kt` | the live options; `load(url)` reusing the handle; ended and failed for live |
| `apps/desktop-player/libmpv/test-media.sh`, `src/test/…/live/` | the live HLS and T01–T04 |
| Part two: `Playback.kt`, `VideoSurface.kt`, `Main.kt`, `StreamProxy.kt`, `mpv/LibMpv.kt`, `mpv/Engine.kt`, `profile/PlaybackSettingsPage.kt` | JavaFX, its engine and its setting removed |
| Part two: `apps/desktop-player/build.gradle.kts`, `gradle/desktop.versions.toml`, `THIRD_PARTY_NOTICES` | no OpenJFX; no `libavcodec` package dependency |
| Part two: `.github/workflows/desktop-release.yml`, `_desktop-player.yml` | T06–T07 |
| `web/src/help/content/{en,ar}/desktop-player.md`, `web/packages/devportal/scripts/apps.mjs`, `CHANGELOG.md`, `CHANGELOG.ar.md` | channels on libmpv; then one engine, from the version named |
