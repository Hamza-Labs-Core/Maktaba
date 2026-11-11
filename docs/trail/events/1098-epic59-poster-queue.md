---
schema: 1
kind: pr
pr: 1098
title: "A scanned video's first probe asks for its identity; probe saves the length"
date: 2026-11-10
session: ed998674-873f-405b-80e7-2be095d4cfa6
agent: claude-code
---

## What happened
- Found by the nas-shapes cells (#1091, need `poster-queue`, check `thumbnail`).
- Cause: in the graph, `thumbnail` (the poster), `thumb_small` and `transcribe` follow
  `content_hash`. Nothing reached `content_hash` from `scan` or `probe`; only the mediaintel
  import and repair's `may_request` queued it. The scanner hashes each file during its walk,
  so the step was only ever needed for imported videos. When the poster moved behind it (#69)
  and `probe -> extract -> transcribe` was cut on 2026-11-02, every video a scan or a
  download found was left without a poster or a transcript. Also, `probe` read
  `format.duration` and never saved it, so a scanned video's length was 0: the poster fell
  back to a 1 s seek, the sprite to 1 fps, and players and channels saw no length.
- Impact: on a fresh install (every NAS) no clip got a poster, a small thumbnail, a
  transcript or a length. The owner's live home was built by an import, so it never showed
  there.
- Fix: `probe` asks for `content_hash` once, only when the video never had one, so a probe
  after an MP4 conversion does not hash and transcribe again (Epic 47). An edge
  `probe -> content_hash` would have broken that. A reconciler sweep (`_identity_sweep`)
  queues it once for each video probed before this change, so existing installs catch up in
  the background. `commit_probe` saves `videos.duration_sec`.
- Like #1097, this stalled behind main's red nas-shapes (#1096 until #1104), and another
  session took it over from its idle child. That session merged main in twice. The second
  merge came after #1097, which emptied `tests/nas/README.md`'s waiting-for-a-fix table, so
  the table became a sentence. `landed` kept every entry plus `poster-queue`. It regenerated
  `firstparty.json` with `tools/plugins/bundle.py`, since the digest conflicted. #1095,
  #1109 and #1112 did not touch this path. `thumbnail` ran and passed in every cell (a poster
  served). Merged in 4c61ef985.

## What we learned
- A test home built by an import hides any step that only an import queues. The NAS cells
  start from an empty volume and a real scan, so they catch what the live home cannot.
- A conflict in a generated bundle (`firstparty.json`'s digest) is resolved by taking main's
  copy and regenerating, never by picking a side.

## Keep doing
- A stalled PR keeps its own `landed` key. Re-merge main and confirm that its check ran
  (not just "green") before merging.

## Open
- On upgrade, the sweep queues `content_hash`, the poster and transcription for every
  scanned or downloaded video that never had them. A large library sees a burst of
  background work after this release, queued at sweep priority.
