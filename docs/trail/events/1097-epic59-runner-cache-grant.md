---
schema: 1
kind: pr
pr: 1097
title: "The worker makes its own data folders before it grants them to the runners"
date: 2026-11-10
session: ed998674-873f-405b-80e7-2be095d4cfa6
agent: claude-code
---

## What happened
- Found by the nas-shapes cells (#1091, need `plugin-cache-grant`, check `logs_clean`).
- Cause: a runner's sandbox grants only folders that exist when it starts (Landlock opens each
  granted path; `sandbox._landlock.rule()` skips one it cannot open). In the server image `HOME`
  is the data volume, and on a fresh volume nothing had made `~/.maktaba/cache` yet.
- Impact: on a fresh install the first report (`review_pages`) or thumbnail a runner wrote was
  refused (`plugin_sandbox_refused`, `/var/lib/maktaba/.maktaba/cache`). The stage then ran in the
  worker instead, outside its sandbox, and `server.log` recorded a permission error. The same
  held for the audio cache, the download work folder and the search index.
- Fix: `areas_doc` (`pipeline/src/maktaba_pipeline/plugins/native.py`) makes Maktaba's own data
  folders (`data.thumbnails`, `data.cache`, `data.index`, `data.review`) before it names them,
  each by its real path (the thumbnail cache was not realpath'd, which Seatbelt needs when `HOME`
  is a symlink). Library roots and their trash are never made.
- The fix was finished on 2026-11-10 but stalled: main's nas-shapes was red from #1096
  (quiet hours held first scans) until #1104. Its first child went idle, and another session
  took it over. That session merged main in twice (after #1109 and after #1112), keeping every
  `landed` entry plus `plugin-cache-grant`. #1109 (59.07) touched the runner sandbox but not
  `areas_doc`, so nothing in it duplicated this fix. `logs_clean` ran and passed in every cell
  (`no permission error in server.log`). Merged in 04448f5a4.

## What we learned
- A sandbox grant for a path that does not exist yet is silently no grant. A grant list must
  be made real (folders created) before the sandbox applies, or the first write on a fresh
  install falls back out of the sandbox.
- Only a fresh volume shows this. A developer's home always has `~/.maktaba/cache` already.

## Keep doing
- When a fix's PR waits behind a red main, keep its `landed` key in the PR and re-merge main
  once main is green. Do not merge it without its NAS check having run.

## Open
- None for this need. `tests/nas/README.md`'s waiting-for-a-fix table is empty after #1098.
