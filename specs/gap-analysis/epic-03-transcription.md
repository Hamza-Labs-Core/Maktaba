# Epic 03 — Transcription: Spec-vs-Implementation Gap Analysis

**Verdict:** The STT building blocks (3 backends, registry, segment-commit, pause helpers, diarization) exist as well-tested *isolated library functions*, but **there is no transcribe stage orchestrator**, so none of them run in any real pipeline path — `Stage.TRANSCRIBE` is a no-op placeholder that logs and marks jobs `done`. Approximately a third of ACs are partial-but-unwired and the keystone correctness ACs (3.6–3.8) are not exercised end-to-end.

## Method

Read README + 9 stories + skimmed plans. Located code under `pipeline/src/maktaba_pipeline/stt/`, `runtime.py`, `pipeline/runner.py`, `db/jobs_claim.py`, `db/jobs_reaper.py`, `pipeline/shutdown.py`, and migrations `0011–0014`. Verified runtime reachability by tracing the dispatch table and cross-referencing every Story 3.x function for non-`stt/` callers.

### Cross-cutting structural finding (affects every story)

- Spec (`README.md:10-12`) mandates a stage orchestrator at
  `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py`. **That
  directory/file does not exist.**
- `runtime.py:188-196` `build_default_dispatch` maps
  `Stage.TRANSCRIBE: _placeholder_handler("transcribe")`.
  `_placeholder_handler` (`runtime.py:218-234`) only logs
  `stage_handler_placeholder` and calls `mark_done`. No override is
  registered in `__main__.py` (no `dispatch_overrides` passed;
  `__main__.py:35-42`).
- `pipeline/runner.py:34,72,86` references `BackendRegistry` only for
  `health_map()` on `/api/system/health`. No code claims a transcribe
  job and runs a backend.
- Grep for callers of `commit_segment`, `pick_backend`,
  `flip_active_transcript`, `should_refuse_claim`, `load_resume_point`,
  `is_pause_due`, `install_shutdown_handlers`, `assign_speakers`,
  `ReorderBuffer`, `build_resume_prompt` outside `stt/` →
  **zero hits**. Every Story 3.x deliverable is dead code w.r.t. the
  pipeline runtime.

Consequence: every "orchestrator …" / "the worker …" / "before claim"
AC is **unwired** even where the helper it would call is implemented
and unit-tested.

---

## Story 3.1 — STT backend protocol

| AC | Text (abridged) | Status | Evidence | Gap |
|----|------------------|--------|----------|-----|
| 3.1-1 | `STTBackend` Protocol matches architecture §3.4 signature | partial | `stt/protocol.py:116-149` | Protocol defined. Deviations: spec method `transcribe(...) -> AsyncIterator[Segment]` and `Segment` uses `start`/`end` per spec text; impl uses `start_sec`/`end_sec` (`protocol.py:62-64`) and adds non-spec fields (`supports_word_timestamps`, `warmup`). Defensible but not a literal match; `cost_per_minute` present. |
| 3.1-2 | Every backend yields `Segment`; non-streaming yields all at end | complete | `mlx.py:66`, `faster_whisper.py:59`, `openai_api.py:95` | All implement async-iterator `transcribe`. OpenAI buffers then yields. |
| 3.1-3 | `BackendHealth{ready,model_loaded,version,device,last_check_at}` used by `/api/system/health` + preflight | partial | `protocol.py:98-107`; `pipeline/runner.py:86` | Shape correct and surfaced on health endpoint. "Preflight before claiming a job" path does not exist (no claim integration). |
| 3.1-4 | Pytest fixture `stt_conformance_suite(backend)` shared suite, CI-gated per backend | **missing** | `tests/stt/` listing; `protocol.py:5` | `protocol.py` docstring claims suite lives in `tests/stt/test_conformance_suite.py` — **file does not exist**. No conformance fixture anywhere. None of the 7 spec conformance test cases (`test_transcribe_short_arabic`, `_monotonic`, `_cover_audio`, `_word_timestamps`, `_language_detection`, `_pause_between_segments`) exist. Tests are ad-hoc fakes with synthetic segments. |
| 3.1-EC | Out-of-order reorder before commit | partial | `segment_commit.py:217-268` `ReorderBuffer` | Implemented + unit-tested, but never invoked by an orchestrator. |
| 3.1-EC | Empty-text segments dropped pre-commit, still counted via gap accounting | **missing** | `segment_commit.py:155-164` | `commit_segment` inserts whatever text it's given; no drop of empty-text segments; no caller does it either. |
| 3.1-EC | `backend.warmup()` called before flipping job to `running` | partial | `protocol.py:143-149`; `mlx.py:125-131` | `warmup()` exists (no-op stubs). No orchestrator calls it before `running`; grep shows no `warmup` caller in `pipeline/`. |

## Story 3.2 — Whisper MLX backend

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.2-1 | Wraps `mlx-whisper.transcribe`, yields per segment, no buffering | partial | `mlx.py:200-215` `_default_transcribe_fn` | Real path collects `result["segments"]` into a list and iterates — `mlx.py:92,213` runs the whole decode in an executor then iterates; it does **not** surface segments as `mlx_whisper` produces them (no incremental streaming despite `supports_streaming=True`). |
| 3.2-2 | `cost=0.0`, `supports_streaming=true`, `requires_file=false` | complete | `mlx.py:38-41` | Matches. |
| 3.2-3 | Respects `hints.initial_prompt` | complete | `mlx.py:84` | Passed through to kwargs. |
| 3.2-4 | Respects `hints.language`; when None, autodetect on first 30 s | partial | `mlx.py:82-92,110-114` | `language` forwarded. `detect_language` is a hardcoded `return "ar"` stub (`mlx.py:114`); no first-30 s detection; orchestrator that would call it is absent. |
| 3.2-5 | Text NFC-normalized, trailing whitespace trimmed | complete | `mlx.py:141-145` `_normalize` | NFC + strip; bidi marks correctly left to renderer. |
| 3.2-EC | Out-of-VRAM → release GPU lock, fail w/ backoff, degrade model size, record in metadata | **missing** | `mlx.py` (no handler) | No `RuntimeError`/OOM handling, no GPU lock, no `degrade_on_oom`, nothing written to `transcripts.metadata`. |
| 3.2-EC | Hallucination loop (≥3 Levenshtein≤2) forces new decode window, records `metadata.hallucination_breaks` | partial/stub | `mlx.py:148-163` | Detects loop but only appends a zero-width sentinel char to text; no decode-window restart; never increments `hallucination_breaks` (no orchestrator strips/records it). Self-described as "stub" in docstring. |
| 3.2-test | `test_mlx_runs_on_apple_silicon_only` | partial | `test_backends.py:28-32` | Tests forced-off health only; does not assert registry skips it. |
| 3.2-test | `test_mlx_initial_prompt_used`, `test_mlx_language_autodetect` | **missing** | `tests/stt/test_backends.py` | Neither spec test exists. |

## Story 3.3 — Faster-Whisper backend

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.3-1 | `FasterWhisperBackend(name="whisper-cuda"|"whisper-cpu")`, device at construction, shared base | partial | `faster_whisper.py:28-57` | Single class handles both via `device` param (acceptable interpretation of "share a base class"). cuda readiness = `shutil.which("nvidia-smi")` (`:119-122`) — crude. |
| 3.3-2 | Streaming: yields each Segment as faster-whisper emits | partial | `faster_whisper.py:80-90,146-155` | `_default_transcribe_fn` materializes the whole generator into a list before yield — not incremental streaming. |
| 3.3-3 | Conformance suite passes for both variants; CPU mandatory in CI | **missing** | (no conformance suite) | Suite absent (see 3.1-4). |
| 3.3-test | `test_faster_whisper_word_timestamps_match_segment` | **missing** | `tests/stt/test_backends.py` | Not present. |
| 3.3-EC | compute_type default float16/int8; fallback to float32 once and record | partial | `faster_whisper.py:50,142-145,104-107` | Defaults correct; fallback try/except → float32 exists but `_compute_type_fallback_used` is **never set True** (`:51` init only); health always reports `False`. Recording is broken. |

## Story 3.4 — OpenAI API backend

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.4-1 | Calls Whisper endpoint; `cost_per_minute` from live price list at build time | partial | `openai_api.py:52-61,240-266` | Calls SDK. `default_cost_per_minute` is a hardcoded `0.006`; no build-time price-list ingestion ("the build pipeline" is asserted in docstring, not implemented). |
| 3.4-2 | `supports_streaming=false`, `requires_file=true`; orchestrator writes temp WAV | partial | `openai_api.py:68-69` | Flags correct. Orchestrator temp-WAV writer absent (no orchestrator). |
| 3.4-3 | Chunk to 24 MB; re-timestamp per original timeline; verified by 90-min integration test | partial | `openai_api.py:104-121,183-208` | `compute_chunk_offsets` is a **byte-offset estimator**, not a real ffmpeg `-f segment` split — comment admits "Production splits the file with ffmpeg" but no such code exists; chunk `.partNNN` paths are never created. Offsets approximated from byte ratio (`:206-207`), not actual audio. No 90-min `test_openai_chunking_preserves_timestamps` integration test. |
| 3.4-4 | Per-library budget cap enforced **before claim**; sums month; refuses w/ `not_before=first of next month` | partial | `openai_api.py:214-230`; `db/jobs_claim.py` | `should_refuse_claim` is a pure projection function only. **Never called** from the claim path; `jobs_claim.py` has no budget logic and no `stt.backends.openai.max_usd_per_month` lookup. Monthly-sum query and `not_before` advancement do not exist. Migration `0011_stt_usage` exists but is unused by claim. |
| 3.4-test | `test_openai_chunking_preserves_timestamps` | **missing** | tests | Absent. |
| 3.4-test | `test_openai_budget_cap` (refuse 30 min, push next month, reason `budget_cap`) | partial | `test_backends.py:119-158` | Tests only the pure boolean; no enqueue/claim/`not_before` assertion. |
| 3.4-test | `test_openai_retry_on_429` exponential backoff 0.5/1/2/4/8 ±25%, 5 attempts | partial | `openai_api.py:141-168`; `test_backends.py:97-98` | Retry loop + jitter implemented; only the constant tuple is asserted in tests, the actual 429-retry behavior is untested. |
| 3.4-EC | Pre-strip silences >5 s via `ffmpeg -af silenceremove` + silence map, original timeline | **missing** | `openai_api.py` | Module docstring describes it; no `silenceremove` code, no silence-map structure, no fixture test. |
| 3.4-EC | Confidence None tolerated downstream | complete | `openai_api.py:118` | `confidence=s.get("confidence")` → None; `protocol.Segment.confidence` Optional. |

## Story 3.5 — Backend registry, transcript history, per-library selection

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.5-1 | `registry.list()` returns backends with `health.ready==true` now | complete | `registry.py:81-90` `list_ready` | Implemented (named `list_ready`, not `list`); awaits `health()` each call. |
| 3.5-2 | Per-library `stt.backend`; preflight; walk `stt.fallback`; else fail `error.kind="no_backend_ready"` | partial | `registry.py:109-129` `pick_backend`; `NoBackendReady` | `pick_backend` walks `[primary,*fallback]` correctly and raises `NoBackendReady`. But it is **never called from a job/claim path**; no library-config (`stt.backend`/`stt.fallback`) lookup wires into it; the `error.kind` is the exception class name, not persisted to a job error envelope. |
| 3.5-3 | Persist `(backend,model,backend_version)`; any re-run creates new row; old `is_active=false` | partial | `registry.py:145-206` `flip_active_transcript` | SQL is correct and atomic. Never invoked by any runtime path; no diff/comparison consumer wired. |
| 3.5-4 | Migration adds `is_active`, drops old UNIQUE, partial-unique index, backfill in one txn | complete (variant) | `migrations/0012_transcripts_is_active.sql:14-49` | Migration **creates** `transcripts` fresh with `is_active` + `transcripts_active_unique` partial index baked in (intentional per its header comment, since no prior `transcripts` table). The "drop old constraint + backfill latest-per-key" steps are moot because the old constraint never shipped. End-state matches AC; `test_migration_backfills_is_active` scenario is N/A. |
| 3.5-5 | Flip-active path = one txn UPDATE+INSERT, partial-unique enforces correctness under concurrent flips | partial | `registry.py:188-206` | Correct SQL in a `db.transaction()`. Concurrent-flip retry on unique-violation is described in docstring but **the retry loop is not implemented** in `flip_active_transcript` (caller would need to retry; no caller exists). |
| 3.5-test | filters_unhealthy / fallback_walks / reprocess_*/ partial_unique_blocks_double_active / migration_backfills | partial | `tests/stt/test_registry.py` (4 tests) | Only 4 tests; concurrent-double-active and migration-backfill behavioral tests absent. |
| 3.5-EC | All unhealthy → requeue `not_before=now()+60s` up to max_attempts then fail | **missing** | (no claim integration) | Not implemented anywhere. |
| 3.5-EC | Subtitle re-enqueue + artifact invalidation on flip | **missing** | grep | `flip_active_transcript` does no `subtitle_gen` enqueue/invalidation. |

## Story 3.6 — Real-time per-segment durable commit

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.6-1 | Single txn: insert segment + update job progress (all 7 fields) | complete | `migrations/0013_segment_commit_function.sql`; `segment_commit.py:100-211` | PG `commit_segment()` PL/pgSQL fuses INSERT + progress UPDATE (last_segment_end_sec, processed_seconds, segments_completed, realtime_factor EWMA α=0.2, ETA, progress_updated_at, last_heartbeat_at). SQLite path mirrors it. Solid. |
| 3.6-1.3 | Optional `transcript_words` insert when word timestamps enabled | **missing** | `0013`; `segment_commit.py` | `commit_segment` never inserts `transcript_words`; `migration 0012` has the table but commit path ignores `segment.words` entirely. |
| 3.6-2 | Both writes committed together; rollback + retry on failure | complete | `0013` (single function in txn); `segment_commit.py:124-148` | Atomic; ON CONFLICT(transcript_id,seq) DO NOTHING makes retry idempotent (`test_pg_commit_idempotent_on_replayed_seq`). |
| 3.6-3 | After commit, check `pause_requested`/`cancel_requested` same connection, exit cleanly | **unwired** | `pause_resume.py:48-66` `is_pause_due` | Pure decision fn exists; no worker loop calls it after `commit_segment`. No transcribe worker. |
| 3.6-4 | Emit `LISTEN segments.committed` notify with `{transcript_id,last_segment_end_sec,seq}` | partial | `0013` trigger; `segment_commit.py:202-210` | PG trigger fires `pg_notify('segments.committed', …)`; SQLite uses in-proc bus. Payload key is `end_sec`, **not** `last_segment_end_sec` as the AC specifies — schema mismatch for the live indexer contract. |
| 3.6-5 | Post-commit invariant `last(segments.end_sec)==jobs.last_segment_end_sec` | partial | `0013` `GREATEST(last_segment_end_sec,p_end_sec)` | Holds for monotonic input; out-of-order/clamped cases rely on absent orchestrator. Not asserted by a test. |
| 3.6-test | atomic / progress-by-audio-time / ewma / eta / pause-after-commit | partial | `test_segment_commit.py` (6 tests) | Progress/idempotent/notify/reorder covered. `test_realtime_factor_ewma`, `test_eta_uses_smoothed_factor`, `test_pause_request_observed_after_commit` (the keystone) **absent**. |
| 3.6-EC | Clamp `end_sec` to `min(end_sec,audio_duration)` | **missing** | `segment_commit.py`, `0013` | No clamping; AC requires orchestrator clamp — orchestrator absent. |

## Story 3.7 — Pause and resume to the exact second

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.7-1 | API pause → `pause_requested=true`; worker commits current seg, flips `paused`, sets `paused_at_sec`/`paused_reason='user'`, releases GPU lock | partial | API `api/internal/handlers/jobs/jobs.go:62,144-165`; `pause_resume.py` | API endpoint sets `pause_requested` (Epic-6 owned). **Worker side absent**: no loop observes the flag mid-transcribe, no `paused_at_sec` write from a transcribe worker, no GPU lock release (no GPU lock exists). |
| 3.7-2 | Resume → claimable; worker `resuming`, seek decoder to `last_segment_end_sec`, rebuild prompt from last K=3, flip `running`; next seg `start>=last_segment_end_sec` | partial | `pause_resume.py:94-136` `load_resume_point`/`apply_resume_seek`/`build_resume_prompt` | All helpers implemented + unit-tested. **None invoked**; no resuming worker; no decoder seek wired (extract stage integration claimed in docstring, not present). |
| 3.7-3 | Resume w/ unavailable original backend → fallback; record `metrics.resumed_with_different_backend` | **missing** | grep | No code writes `resumed_with_different_backend`; no resume-with-fallback path. |
| 3.7-4 | `?force=true` → immediate `paused`, discard in-flight, signal subprocess abort | partial | `jobs.go:144-165` | API force path sets state/clears claim. No worker subprocess-abort signalling (no transcribe subprocess). |
| 3.7-test | resume_from_last / across_restart / after_backend_change / force_drops_inflight / double_resume_idempotent | **missing** | `tests/stt/test_pause_resume.py` | None of the 5 behavioral resume tests exist; only pure-helper tests. |
| 3.7-EC | Audio moved → resolve via content_hash; gone → fail to pending `audio_missing` `not_before=+5m` | **missing** | — | Not implemented. |
| 3.7-EC | Reuse pre-pause `transcripts.language`, disable autodetect on resume | partial | `pause_resume.py:99,107-112` `pinned_language` | `ResumePoint` carries `pinned_language` but nothing consumes it (no resuming decode path). |

## Story 3.8 — Crash recovery & graceful shutdown

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.8-1 | SIGTERM/SIGINT → pause all held jobs, `paused_reason='shutdown'`, commit current seg, exit within grace (120 s) | partial | `pipeline/shutdown.py:79-92,175-237`; `stt/crash_recovery.py:76-114` | Epic-6 `pipeline/shutdown.py` implements generic SIGTERM→pause-all with `paused_reason='shutdown'` and grace. But "commit the current segment" requires a transcribe worker loop that does not exist. `stt/crash_recovery.py` is a **second, unwired** implementation (`install_shutdown_handlers` has zero callers). |
| 3.8-2 | Second signal aborts immediately, DB still consistent | partial | `shutdown.py:157-161`; `crash_recovery.py:93-100` | Epic-6 shutdown handles second signal. DB consistency holds via `commit_segment` atomicity (but unreached). |
| 3.8-3 | Reaper flips stale (`last_heartbeat_at < now()-90s`) to `paused`, `paused_reason='crash'`, `paused_at_sec=last_segment_end_sec`, claimable | partial | `db/jobs_reaper.py:60-90` | Reaper sets `paused_reason='crash'`. Need to verify `paused_at_sec=last_segment_end_sec` is set — generic reaper; `crash_recovery.py:52-56` `REAPER_STALE_PREDICATE` is a duplicated literal (CI grep-guard claimed) but unused by the actual reaper. |
| 3.8-4 | Chaos pytest randomly SIGKILLs worker; transcript matches no-crash baseline byte-for-byte | **missing** | `tests/stt/test_crash_recovery.py` (3 tests) | No chaos/SIGKILL fixture; the keystone `test_chaos_kill_yields_consistent_resume` does not exist. |
| 3.8-EC | Reaper-vs-recovering-worker race resolved by heartbeat predicate | partial | `jobs_reaper.py` WHERE clause | Predicate present in reaper SQL; transcribe-worker side of the race is untestable (no worker). |

## Story 3.9 — Diarization (opt-in)

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 3.9-1 | `diarize=true` enables pyannote; default false | partial | `diarization.py:129-144` `run_pyannote` | Lazy pyannote import implemented. No library-setting read of `diarize` and no orchestrator gating it on/off (no transcribe stage). |
| 3.9-2 | Runs before STT, produces `(start,end,speaker)`, assigns by segment midpoint | partial | `diarization.py:46-97` `assign_speakers` | Assignment logic implemented (uses strict-overlap + split, slightly richer than midpoint). Never called in a pipeline; "before STT" ordering unimplemented. |
| 3.9-3 | Speaker IDs local (`Speaker 1`…); matching deferred v1.1 | partial | `diarization.py:142-143` | Uses pyannote's raw label string, **not** normalized `Speaker N` form the AC requires. |
| 3.9-4 | Process-global `diarization_lock` semaphore default 1 | complete | `diarization.py:105-126` `DiarizationGate` | Semaphore default 1; tested (`test_diarization_gate_serialises_runs`). Not instantiated process-globally anywhere (no consumer). |
| 3.9-test | off_by_default / assigns_speakers / disabled_skips_pipeline (lazy import) | partial | `tests/stt/test_diarization.py` (5 tests) | Assignment + lazy-import verified. "off_by_default" / "disabled skips pipeline" can't test the absent orchestrator path. |
| 3.9-EC | Mid-segment split into `.a/.b` with `metadata.split_from` | partial | `diarization.py:75-96` | Split implemented with `split_from` markers; second half text emptied (word-reassignment deferred per AC). Not reachable at runtime. |
| 3.9-EC | Diarization failure → commit without speakers, record on transcript, don't fail job | **missing** | `diarization.py:129-144` | `run_pyannote` raises `RuntimeError` on import/failure; no catch, no transcript-row failure record, no graceful continue (no orchestrator to do it). |

---

## Top gaps by impact

1. **No transcribe stage orchestrator (epic-wide, critical).**
   `pipeline/stages/transcribe.py` does not exist; `runtime.py:192`
   wires `Stage.TRANSCRIBE` to a no-op placeholder that marks jobs
   `done` without transcribing. Every "orchestrator/worker" AC across
   all 9 stories is unreachable. The epic produces **zero transcript
   rows in production**. All backend/registry/commit/pause/diarize code
   is dead w.r.t. the runtime.

2. **No conformance suite (Story 3.1-4, critical).** The single
   mechanism the spec defines for backend correctness
   (`stt_conformance_suite`, CI-gated, 7 named test cases) is entirely
   absent; `protocol.py:5` points to a non-existent file. None of the 3
   backends are verified against real Arabic/English audio, monotonicity,
   coverage, or pause-continuity. Backend correctness is unproven.

3. **OpenAI chunking & budget cap are facades (Story 3.4-3/3.4-4,
   high).** `compute_chunk_offsets` estimates byte offsets but never
   splits the file (ffmpeg split "is production" but unimplemented);
   `should_refuse_claim` is never called from `jobs_claim.py`, so the
   budget cap cannot prevent a costly claim. A real 90-min OpenAI job
   would mis-timestamp and bill uncapped.

4. **Pause/resume & crash recovery unwired (Stories 3.6-3, 3.7, 3.8,
   high).** The "most-demanded feature" helpers exist and unit-pass,
   but no worker observes `pause_requested` after commit, seeks the
   decoder, rebuilds the prompt, or runs the chaos test. Resume-to-exact-
   second is not demonstrable end-to-end. `stt/crash_recovery.py`
   duplicates Epic-6 shutdown logic with zero callers.

5. **Streaming backends don't stream (Stories 3.2-1, 3.3-2, medium).**
   MLX and faster-whisper both materialize the full decode into a list
   in an executor before yielding, despite `supports_streaming=True`.
   Defeats per-segment durable commit's real-time guarantee for the
   80%-of-users default backend.

6. **`segments.committed` payload schema mismatch (Story 3.6-4,
   medium).** Emits `end_sec`; AC and the live-indexer (Epic 5.5)
   contract specify `last_segment_end_sec`. `transcript_words` is never
   inserted (3.6-1.3), so word-timestamp ACs (3.1, 3.3) have no
   persistence path.
