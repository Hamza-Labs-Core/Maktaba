---
schema: 1
kind: pr
pr: 1636
title: "Story 13.22, part one: Live TV in the Windows and Linux Player plays with libmpv, with the same fallback"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
Part one of story 13.22, on top of 13.18 (#1591). Part two (JavaFX leaves) is gated on 30 days of releases and 14 days of green real-libmpv runs, so it was not built.
- **Player:** `ChannelScreen` asks `Engine.players`, as the watch screen does: libmpv's when it loads, JavaFX's otherwise. `live/ChannelVideo.kt` keeps the `Video` behind `ChannelPlayback`'s `show` and `hide`.
  - The first stream opens a player (`Players.openLive`). Another channel and Back to live load into the same handle (`Video.load`: `loadfile <url> replace`), the frame on screen held until the new stream's first.
  - Live options: no `start`, `keep-open=no`, `cache=yes`, `demuxer-readahead-secs=10`, `hls-bitrate=max`. `end-file` at EOF is the channel ended (new text); with an error, Failed with Try again; 15 s without progress once the picture moved is a stall, also Failed.
  - JavaFX cannot load in place, so it opens a new player per change, as before; its errors and stalls now also reach the screen.
- **CI:** `test-media.sh` makes two live HLS channels (red and blue, 1 s segments); the fake home lists them in a window of six that moves on each second. `ChannelMpvRealTest` (5 tests) runs on the real libmpv in the Desktop Player job.
- An independent review of a81cde41c found five problems, fixed in 7cc835267:
  1. Next channel set Tuning, which removed the surface; on Playing the old channel's held frame showed under the new channel's name. The surface now stays under the spinner while tuning.
  2. The reused handle forwarded every `end-file`: an old stream's error or end arriving as tuning completed failed or ended the new channel, and its position events turned a slow start into a stall.
  3. Back to live worked behind Ended and Failed, and a stop left the pause set.
  4. The stall clock started at `ready()`, before any frame.
  5. JavaFX players could leak: overlapping shows, a hide during a suspended open, a cancelled caller.
  It also found that T03's `frame != null` could never fail and that both test channels served the same picture.
- **CI, first run (f4a3dabba), failed on Linux and Windows alike:**
  - The "first frame" at 20 ms was a black frame mpv draws before any picture, so T03 compared the held frame with black, and the stalled-home test never started its stall clock. Fixed in c0bb53c87: no frame is shown until the stream's playback restarts, for the first stream too. The real-library tests now write mpv's log per handle into `build/reports/mpv-logs` (uploaded with the job's reports on failure) and print mpv's state when a wait runs out.
  - The playlist-end test then showed Try again: mpv's log said `finished playback, audio output initialization failed (reason 4)`. The runners have no sound card, and mpv ends a file that played video only with `AO_INIT_FAILED`. Fixed in 8c953f51e: `audio-fallback-to-null=yes`, and an `AO_INIT_FAILED` end of an entry whose picture started counts as its end.
- **Measured (CI run 45431639078, the real library, 5 tests, none skipped on either):**
  - Linux: first frame 262 ms; next channel 171 ms in the same handle, channel one's frame held meanwhile; Back to live 129 ms from the live edge; playlist end → ended 889 ms after; 404 → failed 55 ms (open) and 51 ms (loaded into the handle); a home that stops answering → failed after 17.9 s.
  - Windows Server 2025: 284 ms; 148 ms; 176 ms; 892 ms; 23 and 88 ms; 17.9 s.
  - 13.18's T01 now measures the real first frame too: 398 ms on Linux, 353 ms on Windows (the 226 ms recorded for #1591 was the black frame).

## What we learned
- **A reused mpv handle hears the replaced stream.** `loadfile … replace` ends the old entry with `STOP`, but an error, an EOF or a position already on its way still arrives after the load. Read `playlist/0/id` right after each `loadfile` (the command returns once the playlist holds the entry) and count only that entry's `end-file` (`playlist_entry_id` at offset 8), `start-file`, `file-loaded`, `playback-restart` and `time-pos`. Ends heard before the id is read are kept and checked once it is.
- **Without `keep-open`, `eof-reached` is cleared as soon as it is set:** a live stream's end is `end-file` with reason EOF.
- **Holding a frame with a pool of bitmaps:** while a load waits for its first frame, keep rendering (mpv waits for the render) into the same slot, never the one on screen, and publish nothing.
- **A test that proves a held frame needs pictures that differ:** two solid colours and the centre pixel tell the channels apart; a non-null check proves nothing when the frame never goes null.
- **The Desktop Player job read only the first matching report** (`report[0]` of `TEST-*MpvRealTest.xml`): a second report would have hidden the first. It now checks every report and that both exist.
- **mpv's software renderer draws a black frame before the picture starts.** A frame existing is not a picture: wait for `playback-restart` before showing or timing one.
- **No audio device is not "no playback":** mpv plays the picture and then reports the file's end as `AO_INIT_FAILED`. `audio-fallback-to-null=yes` keeps the timing and the end clean; CI runners have no sound card, and some users' machines (remote desktops) neither.
- **mpv's `log-file` is the fastest way to read a CI-only failure:** one option, set from a system property the tests own, gave the cause on the first look.
- **`withContext(NonCancellable)` throws on return when the caller was cancelled:** decide inside the block whether the made player is shown or disposed, or it leaks.
- **Stacked on #1644**: channels ask the engine setting (13.21) at each opening, count as open for the probe (13.19), and keep the stream's own subtitles off (13.20).
- **mpv draws a paused stream's first frame before it says the picture started**: a frame drawn while held is shown when the hold ends, or it stays black.
- **An id read on another thread belongs to the load that asked for it**: each load is numbered, and only the newest keeps its id.

## Keep doing
- Wire the screen and the real-library test through the same function (`channelPlayback`), so CI tests the screen's own wiring.
- Make fakes of native libraries (a Kotlin `LibMpv` with JNA `Memory` events) to test event ordering that a real library rarely shows.
- Give each channel or file in a real-media test something the test can see.
- Make real-library tests leave the library's own log in the uploaded reports and say the library's state when a wait times out.

## Stop doing
- Checking only the first file a glob matches in CI.
- Asserting "a frame exists" where the question is "which frame", or timing a first frame without knowing it is the picture's.

## Open
- **A 404 in the middle of a playing stream reads as the channel ended, not Try again:** FFmpeg's HLS demuxer ends on a failed playlist reload and mpv reports `end-file` with reason EOF, the same as a playlist with `#EXT-X-ENDLIST`. Telling them apart needs the last playlist's end tag, or a check with the home before saying it ended.
- A home that answers 503 while a channel warms up fails the first open at once (Try again); there is no automatic retry yet.
- 13.21's engine setting is not on main: "the older engine chosen" is `MAKTABA_PLAYER_ENGINE=javafx`.
- The channel screen uses the test tag `channel-next` twice (the next programme's line and the Next channel button), from before this story.
- Part two (JavaFX leaves) waits for its conditions in the spec.
