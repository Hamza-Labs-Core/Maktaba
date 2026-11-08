---
schema: 1
kind: pr
pr: 998
title: Wave 9f moves the core stages into tv.maktaba.core-media
date: 2026-11-08
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- The core stages (scan, content_hash, probe, thumb_hash, link, fingerprint, style_sample, fp_gate, storage_canonicalize, access_check) now run as the first-party plugin `tv.maktaba.core-media` (story 54.36 §1). `thumbnail` joined `tv.maktaba.thumbnails` and `extract` joined `tv.maktaba.transcription`, because the catalog needs a plugin's stages to belong to its component.
- The writes only the engine may hold (`advance`, `requeue_failed`, `storage_canonicalize`) go through the worker as relay ops, checked against the plugin's own job. Tests refuse each op outside the job. `core.pipeline` still can't be switched off.
- Its roles took slot 0217 (`0217_fp_roles.sql`, SchemaRev 217). The dizqueTV plugin (0214), wave 4's classify (0215) and the Plex removal (0216) merged first, the last of them while this branch's full suite was running. So the roles body was generated again from today's manifests: Plex Live TV only, no dizqueTV.
- A parity proof runs each moved stage through both the old handler and the plugin. While it was being built, convert's parity raced the clock on fingerprints that hash a file's time; that was fixed here. An independent review found the scope-check uuid hole, which was fixed on its own (#937).

- CI found two defects that local runs missed:
  - `maktaba-server stages impl` would have flipped a core stage, because `probe` is now a built-in plugin's. It now refuses a stage whose component isn't switchable, as the API does, and its test covers probe, scan and fp_gate.
  - The core-media parity took 21 s on CI, over the hard cap. Probe's edges queued `transcribe`, which ran ffmpeg on the stand-in files; with no ffmpeg on CI, each job failed and was retried. Transcription is switched off in that world, as on a home without it: 12.0 s to 4.1 s with no ffmpeg on the machine.
- Every by-id write became scope-checked on main while this branch waited (#1005). Running `statements_build.py sync` gave the moved statements their checks: fp_gate's season, link's YouTube rows, the candidate's library and video, and extract's audio rows. The hand-set scope tables of core-media and thumbnails are gone, because the build now derives a write of a video by id.
- CI's Core footprint check (`mac-app / Bundle size (Core payload)`) failed: 6 processes and 763 MB against exactly 4 and 700 MB. Core stages such as scan now run in a runner, so the test home's scan of its empty library woke two runner groups, and a runner stayed up for ten idle minutes (#859), past the 120 s idle window. A runner now exits after one idle minute (`RunnerIdleSec = 60`), and a test pins it within half that window. The update rehearsal had already passed on that head.

## What we learned
- A move this big conflicts with every wave merged meanwhile (registry, graph, import_mediaintel, pyproject lists, guide, roles). Resolve by dropping both sides' moved entries, then rebuild the generated files (`bundle.py --write`, `roles.py sql`) instead of merging them by hand.
- Run the full Go suites alone. A real-ffmpeg timing test (`TestTwoStationsAligned_RealFFmpeg`) fails when pytest shares the machine, and passes alone.

- First-party plugin tests run on their own (`pytest ../plugins/first-party`) don't load the pipeline's pytest config, so the soft-cap hook never judges them. A test that's over the cap passes locally and fails on CI. Time them the way CI runs them, together with `tests/plugins/firstparty`.
- A parity world runs every stage the graph queues, including other plugins'. Switch off what isn't under test, or the world depends on the machine's tools.
- A move that turns core stages into plugin stages changes every check that asks whether a stage is a plugin's. Run `cmd/maktaba-server`'s tests too.
- Moving always-on work into an on-demand process changes when that process runs. Check Core's footprint whenever core stages change where they run.

## Keep doing
- Re-check main's newest migration just before the push and again before the merge (the watcher's slot guard), and take the next slot then, not when the branch starts.
- Run first-party tests as their own pytest run (`pytest ../plugins/first-party`). Passing them alongside `tests` changes the config pytest reads, which drops `faulthandler_timeout` and fails the conftest guard.

## Open
- The legacy handlers are removed one release after wave 9, in their own pull request.
- Rehearsal scenario 16.9 (story T06) waits for 16.1–16.8 (#804).
- Some writes by id still have no scope check: fp_gate's season row, link's YouTube rows, audio tracks by id (README §15.12). A scope kind per statement is the follow-up.
- `extract` has no ml runner yet. It runs in the transcription plugin's group.
- `categorize` stays declared until the owner decides.
