---
schema: 1
kind: pr
pr: 773
title: Thumbnails and subtitle files run as Maktaba's built-in plugins (Epic 54 wave 2, 54.29)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [760, 769]
---

## What happened
- `thumb_small` and `chapter_thumbs` moved into `tv.maktaba.thumbnails`, and `subtitle_gen` into `tv.maktaba.subtitles`, both in the `light` runner (roles: migration 0201). These are the first stages that read media, run ffmpeg and write files.
- The small copy's paths moved to `maktaba_pipeline.thumbnail.small`, because the convert, the review pages and the mediaintel import read them too.
- The worker writes its file areas for the runners (`areas.json`). `library.subtitles` allows only subtitle names: a Seatbelt regex on macOS, and an audit hook on both systems (`plugin_scope`). A sandbox refusal of any other kind runs the job in the worker (`sandbox_refused`).
- Parity now compares files. Each side starts from the same areas, and what each leaves is compared by sha256.

## What we learned
- Under Seatbelt's `(deny default)`, a child the runner starts (ffmpeg, even `/usr/bin/true`) aborts in its loader unless the profile allows reading `/` itself (`literal "/"`, not what is under it) and mapping `/usr/lib` and `/System`. A Homebrew tool also needs its whole prefix, because its libraries live in other packages.
- Seatbelt's regex has no `{m,n}`, and a bracket expression must start with its `-` (`[-A-Za-z0-9_]`). A profile that does not compile fails the whole sandbox, so the runner exits 70.
- Importing a module after the sandbox is applied can fail on a path the profile does not cover. Import everything the guard needs before `apply`.
- A parity seeder that calls `monkeypatch.undo()` undoes the test's own patches too (the cache root). Give it its own `MonkeyPatch.context()`.
- Restore every file area between the two sides, not only the ones compared: a stage the wave did not move (`thumb_small` in the subtitles proof) adopts the files the first side left behind.
- Two Python test files with the same basename in folders without `__init__.py` cannot collect in one pytest run, hence `test_<plugin>_parity.py`.

## Open
- The legacy `thumbnail` stage, with the §2 conversion of its hand chaining into edges, in its own pull request of 54.29.
- Waves 3-9 and the removal.
