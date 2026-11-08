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

## What we learned
- A move this big conflicts with every wave merged meanwhile (registry, graph, import_mediaintel, pyproject lists, guide, roles). Resolve by dropping both sides' moved entries, then rebuild the generated files (`bundle.py --write`, `roles.py sql`) instead of merging them by hand.
- Run the full Go suites alone. A real-ffmpeg timing test (`TestTwoStationsAligned_RealFFmpeg`) fails when pytest shares the machine, and passes alone.

## Keep doing
- Re-check main's newest migration just before the push and again before the merge (the watcher's slot guard), and take the next slot then, not when the branch starts.
- Run first-party tests as their own pytest run (`pytest ../plugins/first-party`). Passing them alongside `tests` changes the config pytest reads, which drops `faulthandler_timeout` and fails the conftest guard.

## Open
- The legacy handlers are removed one release after wave 9, in their own pull request.
- Rehearsal scenario 16.9 (story T06) waits for 16.1–16.8 (#804).
- Some writes by id still have no scope check: fp_gate's season row, link's YouTube rows, audio tracks by id (README §15.12). A scope kind per statement is the follow-up.
- `extract` has no ml runner yet. It runs in the transcription plugin's group.
- `categorize` stays declared until the owner decides.
