---
schema: 1
kind: pr
pr: 789
title: Transcription and semantic search run as Maktaba's built-in plugins (Epic 54 wave 3, 54.30)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [773, 786]
---

## What happened
- `transcribe` and `second_listen` moved into `tv.maktaba.transcription`, run in an `ml` runner beside each transcription worker (`plugin-runner/ml-<i>`, the worker's slot and knobs). `index`, `chapter_infer`, `topic_assign` and `topic_recluster` moved into `tv.maktaba.semantic-search`, run in an `index` runner beside the query server. Roles: migration 0202.
- Runners start offline (`HF_HUB_OFFLINE`). `index` and `chapter_infer` use the plugin only with the Postgres vector store.

## What we learned
- A plugin job that falls back to the worker writes the same rows, so a parity proof passes without testing the plugin path. Now the worker counts fallbacks (`native.FALLBACKS`), and parity fails on any.
- Pipeline functions imported inside a stage's functions are invisible to the statement scan. Two of them (`word_review.vocabulary.load`, `cluster.share`'s roots) catch every exception, so in the runner they would have silently returned nothing. Before each wave, list every `maktaba_pipeline` module the moved code imports, at any depth, that runs SQL.
- `sql += " AND …"` in a branch is a second statement. The scan now registers both forms.
- Run-time uuids make two sides' rows incomparable. `SameIds` reset before each side fixes the drain; after N16's bench (reruns in turns) only timing is judged.
- MLX on Metal works inside Seatbelt once the profile opens the GPU's user clients; without them, `import mlx.core` fails with "No Metal device".

## Keep doing
- Read each moved stage's lazy imports (`grep -n "from maktaba_pipeline" stages/*.py`) before writing `statements.toml`, not after the first parity failure.

## Open
- The legacy `extract` and `thumbnail`, with their hand chaining turned into edges.
- A stage's `refresh` still runs from the worker importing the plugin's code; the removal PR needs a runner call for it.
- `language_packs/ar/test_pack.py::test_without_the_analyser_nothing_is_unknown` fails wherever camel_tools is installed (`--all-extras`): the pack falls back to the real analyser.
