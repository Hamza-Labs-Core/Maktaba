---
schema: 1
kind: pr
pr: 1591
title: "Story 13.18: the Windows and Linux Player plays with libmpv (LGPL, built in CI), and asks the home for desktop-mpv"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The fifth build story of the approved desktop design (D4).
- **Home:** a `desktop-mpv` profile (MKV, HEVC 10-bit, AV1, AC-3, E-AC-3, DTS, up to 8 channels, HDR10 and HLG tone-mapped by mpv). `GET /api/stream/capabilities` now lists `client_profiles`, so the Player asks for `desktop-mpv` only where the home has it; an older home gets `desktop-player`.
- **Player:** a JNA binding to libmpv's client and render APIs, loaded only by absolute path from the install folder and `RTLD_LOCAL`. `MpvVideo` draws with mpv's software renderer into three bitmaps in turn, which Compose shows.
  - A direct file whose picture never starts reopens once with `force_transcode`.
  - A direct file's audio track switches in place.
  - Without libmpv, JavaFX plays: the Player says so once, and the problem report names the engine and the hardware decoder.
- **Build:** `libmpv/build.sh` builds dav1d 1.5.5, FFmpeg 8.1.3 (LGPL), libplacebo 7.387.0 and mpv 0.42.0 (`-Dgpl=false`) from pinned sources. It runs on Linux and in MSYS2's UCRT64, is cached by its pins and saved from main, and goes into the installers through compose's app resources.
- **Measured (Linux CI, the real library):** T01's 1080p H.264 + AC-3 5.1 MKV played directly with its first frame at 226 ms; T02's 2160p HEVC 10-bit at 165 ms in software. T05's undecodable file reopened as a transcode.

## What we learned
- **An LGPL mpv has no X11 output.** mpv's `meson.build` requires `-Dgpl=true` for x11, so drawing into a native child window (`wid`) cannot work on Linux with an LGPL build. Compose does not share Skiko's OpenGL context either. That leaves mpv's software render API (`vo=libmpv`, `sw`): frames at the screen's size into the app's own memory.
- **The surface's memory cost hides in conversions.** `Image.makeFromBitmap` on a mutable bitmap copies, `toComposeImageBitmap` copies again, and neither is freed before a GC that tiny Java objects rarely trigger. Render into a small pool of bitmaps and show them with `asComposeImageBitmap`.
- **Static FFmpeg inside a shared libmpv:**
  - Fold each `.pc`'s `Libs.private` and `Requires.private` into `Libs` and `Requires`. Meson's `prefer_static` also pulls the system's non-PIC static archives (`libuuid.a`).
  - Link with `--exclude-libs,ALL` and load `RTLD_LOCAL`, so FFmpeg's symbols do not meet JavaFX's system FFmpeg in the same process.
- **mpv 0.42 needs libplacebo ≥ 7.387.0.** On Windows its D3D11VA decoding builds only beside its D3D11 output (shaderc and SPIRV-Cross).
- **A tag's SHA from `git/refs/tags` may be the tag object.** Pin `git ls-remote`'s `^{}` commit.
- **jpackage computes Linux dependencies from the runtime only,** not from the app's resources. Name them with `--linux-package-deps`, without spaces, since compose's args file splits on them.

## Keep doing
- Reading a dependency's build files for licence gates before choosing an architecture: the x11 gate settled the spike before any measuring.
- Real-library tests in CI that refuse to pass when skipped.

## Stop doing
- Pushing again while a long Windows leg runs: each push cancelled it, and its first real error came only after several pushes.

## Open
- Not done:
  - the watch screen's reference images (T08, with 13.17);
  - a `device_caps` report (the profile already lists what mpv plays in software, and `DeviceCaps` has no field for hardware decoders);
  - a file's own subtitle tracks (PGS) through mpv's track list, and the caption colour setting;
  - a fallback setting in the Player (it is `MAKTABA_PLAYER_ENGINE=javafx`);
  - Live TV still plays with JavaFX.
- The software renderer's CPU cost on a 4K screen is not yet measured on Windows.
