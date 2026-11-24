---
schema: 1
kind: pr
pr: 1644
title: "Story 13.20: subtitles in the Windows and Linux Player drawn by mpv, a file's own tracks, size and colour"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
Story 13.20, stacked on 13.21 (#1639), both after 13.18 (#1591).
- **The menu in direct play:** the home's subtitles (`external` and `generated`, served at the session's `subs/sidecar/{lang}.vtt` through the stream proxy with `?sig=<stream_sig>`), then the file's own tracks from mpv's `track-list` (**In the file**, and **Picture** for PGS, DVD or DVB). mpv draws all of them:
  - a file's track by `sid`;
  - a home's file by an asynchronous `sub-add`, found again by its `external-filename`;
  - off by `sid=no`.
- **Reopens:** a reopen that turns a file into HLS or back reads the menu again.
- **Shared `:client`:** `PlaybackValues.subtitleColour` (white, yellow), `VideoSubtitle` with `videoSubtitles()`, and `StreamSession.streamSig`. Android's modules compile with them.
- **Profile → Playback:** Subtitle size and Subtitle colour. They are applied through mpv's `sub-*` options and the on-screen caption's `\fs` and `\1c`; picture tracks are not scaled.
- **Test media:** `test-media.sh` muxes an MKV with an Arabic SRT, an Arabic green ASS whose style names Arial, and a committed PGS. The PGS is made by `libmpv/make-pgs.java`, because ffmpeg has no PGS encoder. `MpvRealTest` T05 and T06 draw each track with the real libmpv in CI.
- **An independent review of e7a24a523** found these problems, fixed in 360217174 and 703b442e9:
  - **The home's transcription never reached the menu.** The code kept `auto`, a source the database never writes: `subtitle_files.source` is `embedded`, `generated` or `external`.
  - **A failed `sub-add` recorded the wrong track:** `sid` was read after the add.
  - **Home URLs could name a dead session:** they were captured before a 15-second wait.
  - **The menu was built once per video**, so a quality reopen as HLS kept the file's tracks.
  - **`sub-add` blocked mpv's command thread**, so pause and seek waited behind a slow download.
  - **A forced picture track mpv chose itself was scaled.**

## What we learned
- **Read a schema's CHECK constraint before matching on its values.** The spec said `auto`, and so did the code and the fixtures, all agreeing with one another and none with `shared/db/migrations/0015_subtitle_files.sql`.
- **A session's subtitles live under its own path.** `GET /api/videos/{id}/subtitles` returns a `/stream/subtitles/…` URL that nothing serves. The served paths are `/stream/{session}/subs/sidecar/{lang}.vtt` (the file beside the video, else the stored copy, so a transcription too) and `subs/auto.vtt`, signed with the session's `stream_sig`.
- **An address made for one session goes stale once a reopen opens another.** Build it from the session in use at the moment it is used, and again at each reopen.
- **`mpv_command_async` replies as an event:** `MPV_EVENT_COMMAND_REPLY`, with the error at offset 4 and `reply_userdata` at offset 8 of `mpv_event`. With the `auto` flag the track is not shown until the Player chooses it, so a late reply never overrides a later choice.
- **A pretend libmpv works as a JNA `Proxy` over `LibMpv`.** It records options and commands, answers properties and hands out events, so `MpvVideo`'s calls are unit-tested without the library.
- **ffmpeg has no PGS encoder.** A PGS `.sup` is short to write by hand: PCS, WDS, PDS, ODS (RLE) and END segments. Java2D shapes Arabic for its bitmap.
- **Java's `Locale` does not name every three-letter code** (`fra` stays `fra`). Map ISO 639-2 to its two letters, plus the bibliographic codes (`fre`, `ger`), before naming a file's track.
- **`SwingPanel` (JavaFX's surface) cannot run in `runComposeUiTest`** ("LocalInteropContainer not provided"). Test the watch bar, not the whole Watch screen.
- **A pretend home must serve ranges**: mpv reads a track chosen later by seeking back, and on a stream it cannot seek it draws nothing and says nothing.
- **mpv's `bgr0` leaves the fourth byte undefined**, 0 where it draws a subtitle, and the frame read it as alpha. Frames are `rgb0` in a pixel format with no alpha.
- **libass sizes a font by its line** (the Windows ascent and descent, 1.729 of Plex's em), so a size meant as a 22nd of the picture was drawn at a 38th, 13.18's captions too.
- **Looking at the first reference found the last two**: the numbers passed, the picture was wrong.

## Keep doing
- Measuring pixels in real-library tests rather than only comparing references: colour (yellow and green), the line's height ratio, and "the previous track's line is gone".
- Rendering a test fixture with ffmpeg locally and looking at it before committing (the PGS frame, Arabic joined).

## Stop doing
- Nesting the test's blocking main-dispatcher call inside the `until` helper: one local run hung until it was stopped.
- Running Gradle through `timeout` on macOS. There is no such command there, and a pipe through `grep` hid the error, so a run looked green with no tests run.
- Recording a track id by reading `sid` after an action that may fail.

## Open
- **T05's Linux references** (`mpv-subs-*.png`) are written by the first CI run and committed after a look. Until then the Linux leg fails on them by design.
- **The spec** names `auto` where the source is `generated` (`story-13-20-player-mpv-subtitles.md`, lines 23 and 99).
- **Forced tracks** show only when a video opens. After the person turns subtitles Off, `sid=no` hides them too.
- **13.17's Compose references** (T07) and **Live TV with libmpv** come later.
