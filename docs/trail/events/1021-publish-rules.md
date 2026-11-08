---
schema: 1
kind: pr
pr: 1021
title: The worker checks each event a plugin's runner asks it to publish
date: 2026-11-08
session: af74a0e0-e608-4010-a539-aeb6da2b743b
---

## What happened
- The engine relay's `publish` op (`engine_handler` in `pipeline/src/maktaba_pipeline/plugins/native.py`) inserted any kind and any payload on the `jobs` channel that every web client and app reads. A runner could publish `job.finished` for another job, or a payload of any size.
- `plugins/publish_rules.py` now allows only `job.progress` (`{"job": JobRef}`) and `job.log`, each with a closed schema, at most 64 KiB of compact JSON, about the running job only (`topic`: its id and stage), with the ref's subject kind, subject id and library id the job's (`scope`, compared as uuids like `sameID` from #1005). A payload that does not decode is a `schema` refusal.
- Each refusal is an error back to the runner (`plugin_publish_refused: <reason>: <detail>`), publishes nothing, and logs `plugin_publish_refused` with the minted plugin id, the kind, the reason and the size, never the body.
- A trace on every relayed publish across the whole parity suite recorded none: `RunnerContext` sends progress and log lines through their own ops. The positive test covers both kinds for every first-party stage, and a `run_native` test publishes the job's real `job_ref` from Postgres. Merged as ef7997ca1 (head 179707dff).

## What we learned
- The third-party path for a plugin's own events is `events/emit` (`api/internal/plugins/events.go`). It already checks the manifest's `emits`, the event schema, a 16 KB cap and the subject's scope. The native relay is a separate back channel to the live feed, so its rule is "own job only", not "manifest-declared kinds".
- To find out what a relay op really carries, trace it across the parity suite before choosing an allow-list: an empty trace means the strictest rule breaks nothing.
- `plugins / first-party` failed once on `detect p50: plugin 51.4 ms > 1.10 x in-process 25.2 ms`, on code that never reaches the changed op. It passed 3 of 3 locally and on a re-run. The N16 timing gate on a shared runner can trip on noise.

## Open
- `events/emit` refusals in the plugin API are not logged with the plugin, event and reason, as relay refusals now are.
