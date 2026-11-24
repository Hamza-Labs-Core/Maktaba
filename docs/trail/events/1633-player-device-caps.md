---
schema: 1
kind: pr
pr: 1633
title: "Story 13.19: the Windows and Linux Player reports what the machine plays (device_caps)"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The follow-up to 13.18 (#1591): the desktop Player on libmpv sends a `device_caps` report with `desktop-mpv`.
- **Player:** a probe at start on a libmpv handle of its own plays committed 24-frame 2160p clips of each codec (H.264, HEVC 8/10, VP9 0/2, AV1 8/10, MPEG-2 at 1080p). `hwdec-current` says hardware; a software clip's speed, scaled to 2160p, gives its height (45 fps: 2160, × 9/16: 1440, / 4: 1080, else 720). The results are cached in `probe.json` by GPU and driver (Linux adds the VA-API driver files), CPU and libmpv version. The audio device's channels come from a silent 7.1 clip on a short-lived handle, at start and after each watch screen closes.
- **Report:** the shared `DeviceCaps` (its lists now nullable, left out of the JSON when null) with `video` (height, bit depth, the new `hardware`) and `max_audio_channels` only; never with `desktop-player` nor on a `force_transcode` reopen.
- **Home:** `VideoCap.hardware` in streaming and the API, kept in the report's key and named in the verdict's reason. A desktop-mpv transcode keeps the report's channels (E-AC-3 5.1 for a 6+ channel source on a 6+ channel device, else stereo AAC; no channels said: as every transcode) and its ladder stops at the report's H.264 height, for reused sessions too.
- An independent review of 5a7294f2e found: software speed timed from file-loaded (hwdec attempts and start-up counted, so fast CPUs reported low); an incomplete probe dropping whole codecs and rerunning 20 s at every start; a timeout recorded as "does not decode"; a video opened mid-clip corrupting a measurement; no-report transcodes folded to stereo; a libmpv crash in the probe repeating at every start. All fixed in 834e86a62.
- Found while fixing: `ReportRunner.decoder()` called itself instead of its factory (its parameter was shadowed), a StackOverflowError caught and kept as `probeError`: the probe never ran in the first commit, and no test ran the runner. A test now does.
- **Measured (CI, the real library, 4-vCPU EPYC 7763 runners, no GPU decoder):** the probe took 1483 ms on Linux and 1278 ms on Windows. Software speeds at 2160p ran from 131 fps (HEVC 10-bit) to 382 fps (VP9), so every codec reported 2160p. No audio device on either runner: the channels left out.
- The first CI run failed on Windows only: two unit tests of the Linux key used symbolic links and POSIX paths. They run on Unix only since 7f102045b.
- Merged as 6832e8f18; its spec (#1629, with 13.20 to 13.22) as 2afa96447.

## What we learned
- The API re-encodes `device_caps` through its own struct: a new report field needs `api/internal/handlers/streaming/devicecaps.go` too, or it is dropped before the streaming service sees it.
- A present `video` list is read as the whole list (`Apply`): leaving a codec out transcodes every file of it, so a partial report must list what it did not measure without limits.
- Every file under `apps/desktop-player/libmpv/` keys the libmpv build cache (a 30–50 min rebuild on a miss); scripts that are not libmpv's build go elsewhere.
- ffmpeg's `gradients` source is random unless `seed=` is set; a 2160p MPEG-2 frame's macroblocks alone are over 100 KB.
- A Kotlin function named like a constructor parameter of function type calls itself, not the parameter.
- Clips small enough to commit (under 100 KB a 2160p second, about 0.2–0.7 Mbit/s) decode several times faster than real 4K files: a speed probe needs clips whose bitrate and detail look like what it predicts for.
- A test of Linux-only readers must say so: Windows' runner runs every unit test.
- In an isolated worktree, heredocs and `python - <<EOF` are refused; scripts in the scratchpad and the Edit tool work.

## Keep doing
- Follow a new field through every layer it passes (client, API handler, gRPC, streaming) and test each.
- Test a background runner end to end with a fake, not only its parts.
- Fail safe in a probe: a timeout, a crash or an interruption gives a cautious answer, never a missing one.

## Stop doing
- Timing a decoder from file-loaded: time from the first picture, after a warm-up.
- Chaining a `sleep` before a command to wait; use a background command or a monitor.

## Open
- The committed clips are too easy to decode, so nearly any CPU reports 2160p and the narrowing rarely fires; a file then plays as it did before. Realistic clips, a correction factor, or counting dropped frames: #1652.
- The report has one height per codec (the slower of its 8 and 10-bit clips); a per-bit-depth height, and probing again on battery, are left for later.
