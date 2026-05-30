# Epic 02 — Audio Extraction: Spec-vs-Implementation Gap Analysis

**Verdict:** The four audio modules (`probe`, `track_selection`, `extract`,
`accounting`) exist, are well-tested in isolation, and are largely
behaviorally correct *as libraries* — but **none of them is wired into any
runtime path**. The pipeline daemon dispatches every `probe`/`extract` job
to a no-op placeholder handler that just logs and marks the job `done`. The
entire epic is therefore **unwired**: a deployed Maktaba never probes,
selects, or extracts audio. Several secondary gaps (no gRPC `MediaService`,
no track-selection stage/fanout, no resume-discard, no temp-WAV cleanup, no
`audio_drift`/`transcoded_extract`) compound this.

Method note: the prior `specs/FULL_IMPLEMENTATION_AUDIT.md` was not trusted;
all findings below are verified directly against code.

---

## Cross-cutting (epic-wide) gap — affects every story

`pipeline/src/maktaba_pipeline/runtime.py:188-235` builds the dispatch table
with `_placeholder_handler` for **every** stage including `PROBE` and
`EXTRACT`. `_placeholder_handler` (`runtime.py:218-235`) only logs
`stage_handler_placeholder` and calls `mark_done`. `runtime.run`
(`runtime.py:274`) calls `build_default_dispatch(dispatch_overrides)` and the
sole production entry point `__main__._serve` (`__main__.py:117-118`) calls
`run(cfg, db=database)` with **no `dispatch_overrides`**. A repo-wide search
finds zero production callers that register the real handlers. Net effect:
`commit_probe`, `select_tracks`, `stream_pcm`, `extract_to_file`,
`ExtractAccountant` are dead code from the runtime's perspective; jobs are
drained as instant no-ops. This single gap demotes nearly every AC below to
*unwired* regardless of library-level correctness.

---

## Story 2.1 — Probe the audio tracks

Implementation: `pipeline/src/maktaba_pipeline/audio/probe.py`.
Plans claim a Go `MediaService.Probe` gRPC binding + Python stage that calls
it; reality is a pure-Python `run_ffprobe` subprocess + `commit_probe`.

| AC | Text (abbrev.) | Status | Evidence | Gap |
|---|---|---|---|---|
| 2.1-a | media_info populated; one audio_tracks row per stream with index/codec/channels/sample_rate/language/title/is_default | partial / unwired | `probe.py:108-167` parse; `probe.py:245-317` UPSERTs | Parse + persist logic is correct (`language` defaults to `und` at `probe.py:151-152`; `is_default` from `disposition.default==1` at `probe.py:161`). But never called at runtime (see cross-cutting). Also `index` = audio-rank (`probe.py:144-165`), correct for `-map 0:a:N`. |
| 2.1-b | State → PROBED exactly once; `extract` job enqueued `pending` | partial / unwired | `probe.py:343-356` advances + `enqueue(... Stage.EXTRACT, priority=100)` | Logic correct & idempotent, but unreachable at runtime. Enqueue uses `priority=100` (consistent with Story 2.4). |
| 2.1-c | Zero audio → PROBED then READY_NO_AUDIO; no extract job | partial / unwired | `probe.py:327-341` (`NO_AUDIO` outcome, no enqueue) | Correct in isolation (test `test_commit_probe_no_audio...` `test_probe.py:152-159`), but unreachable. |
| 2.1 inv. | ffprobe invoked exclusively via `MediaService.Probe`; Python never shells out | **missing** | `probe.py:170-216` shells out via `asyncio.create_subprocess_exec`; no proto `MediaService` exists (`grep shared/proto` → none); no `api/.../grpcserver/media.go` (dir absent) | The entire Go gRPC binding from `plan-02-01-audio-probe.md §3` and `plan-02-01-ffprobe-binding.md` is **absent**. Python shells out directly, violating the stated cross-language single-parser invariant. Also a second independent ffprobe shellout exists at `subtitle/extractor.py:111`. |
| 2.1 inv. | Single transaction for persist + FSM + enqueue | partial | `probe.py:292-317` wraps UPSERTs in `db.transaction()`, but `fetchrow`/`advance_after_stage`/`enqueue` at `probe.py:323-356` run **outside** that block | FSM advance + extract enqueue are not atomic with the media_info/audio_tracks writes — a crash between commit and enqueue leaves a PROBED video with no extract job (the spec's "one BEGIN/COMMIT" invariant, plan §1 step 4, is not met). |
| 2.1 EC | `-analyzeduration 100M -probesize 50M` applied unconditionally | complete | `probe.py:57-58,184-188` | Flags present. (`--` end-of-options sentinel from plan §2.2 is **absent** at `probe.py:180-194` — minor injection-hardening gap; mitigated by exec-mode, no shell.) |
| 2.1 op | `/healthz` probe.binary_present sub-check; OTel `ffmpeg.probe`/`pipeline.stage.probe` span; `pipeline_probe_total` metrics | missing | no refs found | None of the operational ACs (health sub-check, spans, counters) implemented. |

Schema note: spec/plans refer to column `index`; actual schema and code use
`track_index` (`shared/db/migrations/0009_audio_tracks_extensions.sql:41,53`;
`probe.py:264,267`). Internally consistent, divergent from plan prose only.

---

## Story 2.2 — Track selection

Implementation: `pipeline/src/maktaba_pipeline/audio/track_selection.py`
(spec/plan say `media/track_selection.py` + a `select_track` stage; the
algorithm lives in `audio/` and there is **no stage**).

| AC | Text (abbrev.) | Status | Evidence | Gap |
|---|---|---|---|---|
| 2.2-a | One row via priority: pref-lang → ara → is_default → first index | partial / unwired | `track_selection.py:64-102` | Priority order correct. `select_tracks` has **zero callers** anywhere in the codebase (grep) — never invoked from probe, a stage, or runtime. Pure dead function. |
| 2.2-b | `multi_audio=true` → return ALL non-commentary; enqueue one `transcribe` job per track | **missing** | `track_selection.py:81-82` returns all tracks, but **no** code enqueues per-track transcribe jobs | The fanout half of the AC (one `transcribe` job per selected track) is entirely unimplemented; there is no `select_track` stage. Stage enum (`db/jobs.py:71-77`) has no `select_track`. |
| 2.2-c | `und` + pcm still selected over nothing for Arabic-preferring library | complete (in isolation) | `track_selection.py:92-102` — falls through to default/first; never refuses | Behaviorally satisfied by the pure function; unreachable at runtime. |
| 2.2 EC | Identical-language dup: more channels wins, then is_default, then index | complete | `track_selection.py:114-119` `_break_ties` key `(-channels, not is_default, index)` | Matches spec. |
| 2.2 EC | Descriptive/SDH excluded via disposition or title regex | partial | `track_selection.py:34-37,105-111` regex `(audio description|described|sdh|cc|commentary)` | Regex narrower than plan-02-02 §filters (`hearing impaired`, `audio[- ]?descri(ption|bed)`); checks `disposition.descriptions`/`commentary` but **not** `disposition.hearing_impaired` (plan-02-02 `is_descriptive` requires it). |
| 2.2 — | `langid` probe for `und` tracks (plan §7: sample 30 s, whisper-cpp detect, write `detected_language`/confidence) | **missing** | no `langid_probe.py`; `iso639.py` absent; `_normalise_lang` (`track_selection.py:122-128`) is a 9-entry hardcoded dict, not real ISO 639-1/2B/2T→3 normalization | Whole language-detection subsystem (plan-02-02 §7, schema columns `detected_language*` exist in migration `0009:49-52` but are never written) is unimplemented. |
| 2.2 — | User `track_override` short-circuit; Go `GET/PUT /api/videos/{id}/tracks` preview/override | **missing** | no `api/internal/tracks/` dir; `select_tracks` has no `track_override_id` param (signature `track_selection.py:64-67`) | Override path (plan-02-02 §0/§2.3) entirely absent on both Go and Python sides. |
| 2.2 — | `track_selection_decisions` audit row | **missing** | no such table/insert | Plan-02-02 audit surface unimplemented. |

---

## Story 2.3 — Stream extraction

Implementation: `pipeline/src/maktaba_pipeline/audio/extract.py`.

| AC | Text (abbrev.) | Status | Evidence | Gap |
|---|---|---|---|---|
| 2.3-a | Spawn exact `ffmpeg -hide_banner -nostdin -threads 1 -i {file} -map 0:a:{idx} -ac 1 -ar 16000 -sample_fmt s16 -f s16le pipe:1`; yield 64 KiB PCM chunks | partial / unwired | `extract.py:68-120` `build_ffmpeg_args`; `extract.py:179-223` `stream_pcm`; `DEFAULT_CHUNK_BYTES=64*1024` (`extract.py:45`) | argv **adds `-fflags +genpts` unconditionally** (`extract.py:100-102`) — not in the AC's exact command (spec puts genpts only in the TS-reset edge case). Chunk size & flags otherwise match. Never invoked at runtime (cross-cutting). |
| 2.3-b | Backend requiring a file → write `~/.maktaba/cache/audio/{hash}.wav`; remove on `done`/`failed`/`cancelled` | partial | `extract.py:233-289` `extract_to_file` writes the WAV | **No cleanup/removal logic exists** anywhere — no unlink on terminal job state (grep `unlink`/`cleanup` → only a doc comment `extract.py:237`). The "file is removed when the job reaches done/failed/cancelled" half of the AC is unimplemented. No `requires_file` backend wiring (only a comment in `stt/mlx.py:76`). |
| 2.3-c | Bad input → job `failed` with `error{kind:"ffmpeg_decode",returncode,stderr_tail}`; no partial PCM | partial / unwired | `extract.py:50-65` `ExtractError`/`to_envelope`; raised at `extract.py:213-222`, `extract.py:277-288` | Envelope shape correct. But error→job-state mapping is unwired (no extract stage handler); "no partial PCM delivered" is **not** guaranteed — `stream_pcm` yields chunks as they arrive and only raises after EOF (`extract.py:210-222`), so a consumer can receive partial PCM before the failure. |
| 2.3-d | On pause: SIGTERM, drain ≤5 s, then SIGKILL; no zombie ffmpegs | complete (in isolation) | `extract.py:136-158` `StreamHandle.terminate` (SIGTERM → `wait_for(5s)` → SIGKILL) | Logic correct & idempotent. Not invoked by any runtime pause check (no extract stage); the worker-side pause integration is absent. |
| 2.3-e | Resume uses `-ss start_sec` input seek; first byte ≥ start | partial | `extract.py:103-104` emits `-ss max(0, start-0.5)` before `-i` | Seek emitted, but the spec's mandatory **discard-the-lead-in loop** ("discard until first PCM sample whose PTS ≥ requested") is **not implemented** — code comment at `extract.py:88-93` explicitly says "The discard loop lives in the worker, not here", and no such worker exists. Resume offsets are therefore *not* exact (off by up to 0.5 s). |
| 2.3 EC | VFR: seek 0.5 s early + discard lead-in | partial | `extract.py:103-104` does the −0.5 s | Discard half missing (see 2.3-e). |
| 2.3 EC | Concatenated TS: `-fflags +genpts` for monotonic PTS | complete | `extract.py:100-102` | Applied (though unconditionally, see 2.3-a). |
| 2.3 EC | Broken duration → stream until EOF; refresh `total_duration_seconds` from decoded length | **missing** | no duration-refresh code; `processing_jobs.total_duration_seconds` never updated by extract | Unimplemented. |
| 2.3 EC | Decoder undercount → EWMA `decoded/declared`; fail with `error.kind=="audio_drift"` if <0.95 | **missing** | grep `audio_drift`/`decoded_samples`/`0.95` → none | Entire drift-detection safeguard absent. |
| 2.3 — | `transcoded_extract=true` retry on mid-file codec change (Story 2.1 EC handoff) | **missing** | grep `transcoded_extract` → none | Unimplemented. |

---

## Story 2.4 — Audio resource accounting

Implementation: `pipeline/src/maktaba_pipeline/audio/accounting.py`.

| AC | Text (abbrev.) | Status | Evidence | Gap |
|---|---|---|---|---|
| 2.4-a | `concurrency.extract=2` → ≤2 simultaneous extracts/process; rest stay pending | partial / unwired | `accounting.py:40-73` `ExtractAccountant` semaphore (default cap 2, `accounting.py:36`) | Correct as a primitive. **Not used anywhere** — no extract stage acquires `.slot()`. `runtime.run` builds its own per-stage `asyncio.Semaphore` (`runtime.py:263-265`) from `cfg.stage_concurrency`, which `__main__` never populates (defaults to 1), and that path only no-ops jobs anyway. The disk-aware cap is unwired. |
| 2.4-b | Priority-50 user job preempts queued priority-100 as a slot frees | partial | `accounting.py:16-18` defers to `db.jobs_claim.claim_one` ordering by priority | Depends on Epic 6 claim loop; not verified here, but the accounting module itself adds nothing and is unwired. |
| 2.4-c | Optional CPU throttle: load_avg_5m > N×cores → next claim `not_before = now()+30s`; toggled by `pipeline.cpu_throttle_enabled` | partial / unwired | `accounting.py:76-97` `cpu_throttle_not_before` pure function | Pure function correct (default delay 30 s, `accounting.py:37`). **No caller** wires it into the claim loop, and no `pipeline.cpu_throttle_enabled` setting plumbing exists. |
| 2.4 EC | Worker dies holding slot → reaper flips job to paused; slot reusable | partial | reaper exists (`pipeline/reaper.py`) but no extract slot is ever held (semaphore unused) | Slot-recovery is moot because slots are never taken. |

---

## Top gaps (ordered by impact)

1. **Epic-wide: audio modules are completely unwired.**
   `runtime.py:188-235` + `__main__.py:117-118` route all `probe`/`extract`
   jobs to a logging no-op (`_placeholder_handler`, `runtime.py:218-235`).
   `commit_probe`, `select_tracks`, `stream_pcm`, `extract_to_file`,
   `ExtractAccountant` have zero production callers. A deployed Maktaba
   never probes, selects, extracts, or transcribes audio — the epic
   delivers no end-user behavior despite green unit tests.

2. **`MediaService.Probe` gRPC binding entirely missing.** No
   `shared/proto` `MediaService`, no `api/.../grpcserver/media.go`. The
   "single canonical ffprobe parser, three callers" architecture invariant
   is violated; Python shells out directly (`probe.py:170-216`) and a
   second independent ffprobe shellout lives in `subtitle/extractor.py:111`.

3. **Track-selection has no stage and no transcribe-job fanout.**
   `select_tracks` is an uncalled pure function; Stage enum
   (`db/jobs.py:71-77`) has no `select_track`. AC 2.2-b (one `transcribe`
   job per track under `multi_audio`), user `track_override`, the Go
   `/api/videos/{id}/tracks` endpoints, langid-probe, and the
   `track_selection_decisions` audit row are all unimplemented.

4. **Probe persistence is not atomic with FSM advance + enqueue.**
   `probe.py:292-317` commits media_info/audio_tracks, then advances state
   and enqueues extract *outside* the transaction (`probe.py:323-356`),
   breaking the spec's single-transaction invariant; a crash mid-sequence
   yields a PROBED video with no extract job.

5. **Extraction safeguards & lifecycle missing.** No temp-WAV cleanup on
   terminal job state (AC 2.3-b), no resume lead-in discard so resume is
   inexact (AC 2.3-e), no `audio_drift` EWMA / `transcoded_extract` retry /
   duration-refresh edge cases (Story 2.3 ECs). `stream_pcm` can also emit
   partial PCM before raising, contradicting "no partial PCM delivered".

6. **All operational ACs absent across the epic:** no `/healthz`
   probe sub-check, no OpenTelemetry spans (`ffmpeg.probe`,
   `pipeline.stage.probe`, extract), no Prometheus counters/histograms.
