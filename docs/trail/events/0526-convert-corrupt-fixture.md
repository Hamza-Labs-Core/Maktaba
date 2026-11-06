---
schema: 1
kind: pr
pr: 526
title: A corrupt-input test got done: random bytes are not always corrupt
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- CI failed `test_convert.py::test_a_corrupt_input_fails_at_once` on unrelated PRs: on [postgres] in run 41476408173 (PR #523) and on [sqlite] on PR #525. `assert job["state"] == "failed"` got 'done'.
- Cause: the fixture, not the stage. The "corrupt" input was `os.urandom(200_000)`. ffprobe 8.1 accepted 3 of 300 such files: 2 as a raw H.263 video stream, 1 as an LRC subtitle. When probing works, `plan_for` skips the file (`needs_reencode` with re-encoding off, `no_video` for LRC) and the job ends done/unchanged with the file untouched. Captured one such file and reproduced 'done' on both dialects.
- Fix: the fixture is a real Matroska EBML header (DocType "matroska") plus a 200 kB body from a seeded RNG. The matroska demuxer claims the file and always rejects it. The test also checks the error says "the original cannot be read". Passed 20/20 runs on both dialects.
- Same PR: `test_cancel_kills_ffmpeg_and_leaves_nothing` was over its 5 s soft cap on CI (6.9 s). About 2.8 s of the 3.95 s local run was making the 60 s 720p VP9 source (8.9 s of CPU). The source is now MPEG-4 Part 2 (0.6 s, 4.5 s of CPU); it still plans a re-encode and the cancelled x264 encode is the same. Now 1.8 s locally.
- Merged at b9e2efcc.

## What we learned
- Random bytes are not reliably corrupt media: ffprobe guesses raw streams and text formats from junk. Build corrupt fixtures from a real container header plus seeded junk.

## Open
- With `allow_reencode` on, junk that ffprobe reads as raw H.263 would re-encode into a 1-frame MP4 whose frame count matches; the original goes to the trash (fixed in #537).
