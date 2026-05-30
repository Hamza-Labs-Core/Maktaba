# Epic 09 — Library Management: Spec-vs-Implementation Gap Analysis

**One-line verdict:** The library-management *logic* is written (pure
Python modules in `pipeline/src/maktaba_pipeline/library_mgmt/` + Go REST
handlers) and the schema migrations exist, but **every behavioral story
that depends on the pipeline daemon is unwired** — the daemon's stage
dispatch table is 100% no-op placeholders and the watcher/sweep/topic/
chapter/categorize/speaker modules are never imported by any runtime
path; several API-only stories also have dead or unmounted code.

---

## Method

- Read `README.md`, all 18 `story-09-*.md`, sampled all 18 `plan-09-*.md`.
- Traced each AC to code; verified existence, runtime reachability, and
  behavioral satisfaction against the actual code (not audit/spec claims).
- Key runtime fact established by reading
  `pipeline/src/maktaba_pipeline/runtime.py:176-235` and
  `pipeline/src/maktaba_pipeline/__main__.py:93-118`:
  `build_default_dispatch` maps **every** stage
  (`scan, probe, extract, transcribe, subtitle_gen, index, thumbnail`)
  to `_placeholder_handler` (runtime.py:218-235) which only logs and
  calls `mark_done`. `__main__._serve` calls `run(cfg, db=database)`
  with **no `dispatch_overrides`**. Grep confirms **zero non-test
  references** to `library_mgmt` or `watcher.service` from any
  daemon/orchestrator/scanner code. The added stages
  (`topic_recluster, topic_assign, categorize, chapter_infer`,
  migration `0049`) are not even in the daemon's `Stage` enum dispatch
  map, so a job with those stages would be `mark_failed_or_retry`
  ("dispatch_unknown_stage", runtime.py:202-209).

Consequence: all auto-categorization, watching, sweeping, dedup,
chapter inference, speaker voiceprinting is **stub/unwired** at runtime
regardless of how complete the pure module is.

---

## Per-story AC tables

### Story 9.1 — Library config schema and validation

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 schema enforcement (recognised keys, unknown→warning) | **partial** | `library_mgmt/config.py:139-265` validates the full recognised key set with positive/negative checks and emits warnings for unknown keys. BUT this Python validator is **never invoked by the Go PATCH handler** (`libraries.go:268-369` does a blind `DeepMergeJSON` with no schema validation). A malformed `stt.backend="invalid"` returns 200, not the AC-required 422 with offending path. |
| AC-2 defaults inheritance | **partial** | `config.effective_config()` (config.py:289-305) correctly layers `EFFECTIVE_DEFAULTS ⊕ pipeline.toml ⊕ library`. Logic correct, but no worker reads it (daemon stages are no-ops), so never exercised in a runtime path. |
| AC-3 settings change → `library.settings_changed` NOTIFY | **missing** | `SETTINGS_CHANGED_EVENT` constant defined (config.py:50) but the Go `Patch` handler (libraries.go:347-362) issues a plain `UPDATE` with **no `pg_notify`/NOTIFY**. No orchestrator subscriber exists. |

### Story 9.2 — Filesystem watcher

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 debounced emission | **unwired** | `watcher/debouncer.py` + `watcher/service.py` (`Watcher`, `LibraryObserver`) implement debounce/settle logic with tests. But nothing constructs/starts `Watcher` — `__main__.py`/`runtime.py` never reference the watcher. No `watchdog` observer is ever scheduled in production. |
| AC-2 settling check | **unwired** | Logic present in `debouncer.py`; never run. |
| AC-3 move detection | **unwired** | `watcher/dispatch.py:217-260` handles moved/deleted+created; never invoked. |
| AC-4 restart resilience (one-shot sweep on boot) | **missing** | No boot path starts a watcher or a catch-up sweep; `SweepRunner` (sweep.py) is never instantiated by any scheduler. |

### Story 9.3 — Periodic full sweep

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 diff against catalog | **unwired** | `library_mgmt/sweep.py:163-228` implements the size+mtime fast path, move detection handoff, MISSING marking. Pure logic only — `SweepRunner` is never created; no scheduler ticks it. |
| AC-2 single-flight | **complete (logic) / unwired** | `asyncio.Lock` guard (sweep.py:151-161) is correct but never reached at runtime. |
| AC-3 configurable interval (`0` disables) | **complete (logic) / unwired** | `stream_sweeps` honours `interval_sec<=0` (sweep.py:243-249); no caller. |
| AC-4 sweep telemetry → `library_sweeps` | **partial** | Table exists (`0044_library_sweeps.sql`, schema matches README). `SweepStore.write_sweep_report` Protocol declared but **no concrete DB implementation** exists; no production writer. |

### Story 9.4 — Content-hash dedup

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 hash computation (head/tail/size, path-out-of-root) | **partial** | `identity/hasher.py:107-184` computes BLAKE3 over first/last `HEAD_TAIL_BYTES`+size correctly; `<8 MiB` full-hash boundary correct. Path-out-of-root assertion is **not in the hasher** but is in `library_mgmt/dedup.py:90-131` (`is_path_in_roots`, raises `PathOutOfRootError`). Wired together only if a scanner calls dedup — no runtime scanner stage does (placeholder). |
| AC-2 global hash uniqueness, dup→`audit_log` event | **partial/unwired** | `videos.content_hash UNIQUE` exists (`0003_videos_content_hash.sql`). Dedup decision logic in `dedup.py` exists, but the `duplicate-detected` audit row is never written (no runtime scanner; API never calls dedup). |
| AC-3 performance / `hash_timeout_sec` | **missing** | No timeout enforcement in `hasher.py`; no `hash_timeout_sec` config plumbed. |

### Story 9.5 — Ignore rules and extension filtering

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 built-in ignores | **complete (logic) / unwired** | `library_mgmt/ignore.py` implements the built-in patterns (tests in `test_ignore.py`). Used by scanner walker, but the scan stage is a no-op placeholder so not reachable from job dispatch. |
| AC-2 supported extensions | **complete (logic)** | Extension set implemented in `ignore.py`. |
| AC-3 user globs applied to scan + watcher | **partial** | Glob filtering implemented; watcher integration unreachable (watcher never started). |

### Story 9.6 — Manual scan trigger and progress

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 default mode, priority 50 | **partial** | `libraries.go:436-463` `Scan` handler enqueues at priority 50 — but production wiring `&libraries.Handler{DB: d.DB}` (router/p6.go:58) sets **no `JobEnqueuer`**, so `h.JobEnqueuer == nil` → returns 202 with empty `job_id`, **no job is ever enqueued** (libraries.go:450-456). |
| AC-2 `?rehash=true` mode | **missing** | The Go `Scan` handler never reads `?rehash`. `ScanMode.REHASH`/`should_rehash` exist in `library_mgmt/manual_scan.py:48-89` but no API path or worker uses them. |
| AC-3 progress reporting (processed_seconds repurpose, 1 Hz WS) | **stub** | `manual_scan.py` `ScanProgress` models the field-munging correctly but no scan worker emits it; no WS progress events for scans. |
| FSM extension (MISSING/SUPERSEDED/READY_NO_AUDIO/CORRUPTED) | **complete** | `domain/states.py:60-63` + migration `0004_video_states_and_stages.sql` include all four auxiliary states and transitions. |

### Story 9.7 — Library stats query

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 composition (full by_state/by_language/by_content_type/jobs/last_sweep) | **partial** | The **mounted** handler is `libraries.go:478-549` `Stats`, which returns only `total_videos, total_duration_sec, by_state, processed_pct, by_language` — **missing** `by_content_type`, `storage`, `jobs`, `last_sweep`. The fuller `stats.go:70 StatsCached` exists but `MountStatsCached` is **never called** (grep: only definition, no caller); router/p6.go:59 uses `libHandler.Mount` which binds `h.Stats`. So the cache-backed AC-1 shape is dead code. |
| AC-2 sub-50ms via `library_stats_cache` + triggers | **missing** | Table exists (`0047`). No triggers on `videos`/`processing_jobs` exist (no migration creates them). `stats-rebuild` CLI command does not exist (grep finds only a doc-comment in stats.go). The mounted handler reads `videos` directly every call. |
| AC-3 empty-library defaults (`processed_pct=null`) | **partial** | Mounted `Stats` returns `processed_pct: 0` (a float, not null) for empty libraries (libraries.go:521-523, `ProcessedPct float64`). `StatsCached` would return `*float64` nil but is unmounted. |

### Story 9.8 — Auto-categorization: language tag

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 single-language assignment in flip txn | **unwired** | `library_mgmt/language.py` resolves language; the transcribe stage is a no-op placeholder so `videos.detected_language` is never set by a worker. |
| AC-2 multi-audio primary track | **unwired** | Logic present; never run. |
| AC-3 confidence < 0.6 → `und` | **complete (logic) / unwired** | Threshold logic in `language.py`; no runtime caller. User PATCH override path: `videos` PATCH handler is Epic 7 (not verified to preserve across reprocess since reprocess is stubbed). |

### Story 9.9 — Auto-categorization: topic tag

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 nightly k-means recluster | **unwired** | `library_mgmt/topics.py` implements mini-batch k-means + upsert; no nightly scheduler, `topic_recluster` stage not in daemon dispatch map. |
| AC-2 topic labeling + PATCH rename | **missing (API)** | `PATCH /api/libraries/{id}/topics/{topic_id}` endpoint **does not exist** in any handler (no topics route in router). |
| AC-3 per-video assignment top-3 | **unwired** | Logic in `topics.py`; `video_topics` table exists (`0046`) but no writer. |
| AC-4 disabled by `auto_tag_topics:false` | **complete (logic) / unwired** | Skip logic present; never reached. |

### Story 9.10 — Content type classifier

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 feature extraction during probe → `media_features` | **stub** | Probe stage is a no-op placeholder; `media_features` (`0045`) is never populated. |
| AC-2 classifier inference → `videos.content_type` | **unwired** | `library_mgmt/content_type.py` implements the classifier; `categorize` stage not in daemon dispatch. |
| AC-3 manual override preserved unless `?force` | **missing** | No API path applies/respects this. |
| AC-4 `videos_content_type` index | **complete** | `0045_media_features.sql` adds `videos.content_type` + partial index `videos_content_type_idx`. |

### Story 9.11 — Speakers, voiceprints, naming, merge

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 new voice → unknown speaker | **unwired** | `library_mgmt/speakers.py` implements voiceprint matching + unknown-index allocation; diarization stage stubbed, never run. |
| AC-2 match assignment, no voiceprint drift | **unwired** | Logic present; no runtime caller. |
| AC-3 user naming relabels prior segments | **complete** | `speakers.go:85-121` Rename works; segment display is by FK reference. Mounted (router/p6.go:88). |
| AC-4 merge in one txn | **complete** | `speakers.go:125-173` Merge is a single transaction rewriting `segment_speakers` then dropping the row. Mounted. |
| AC-5 cross-library isolation | **partial** | Migration `0048` adds `speakers.library_id` + `voiceprint` + `unknown_index`, but the base schema (`0035`) keys uniqueness on `(video_id, name)` (per-video), not per-library; the README/story require a per-library `speakers(library_id, name, voiceprint)`. The Go handler still treats speakers as per-video (`speakers.go:50-59` filters by `video_id`). Schema is a bolt-on, not the canonical per-library model. |

### Story 9.12 — Tag CRUD and normalization

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 `tags` schema (`display_name`, `normalized_name`, DROP `name`, unique index) | **missing** | No migration applies the AC-1 ALTER. `0034_tags.sql` keeps `tags(id, name, name_norm UNIQUE)`. The literal schema requirement is unmet (functionally similar columns exist under different names). |
| AC-2 normalization on insert (trim + NFC casefold) | **complete** | `tags.go:222-226 NormaliseTagName` does TrimSpace + NFC + ToLower; insert uses it (tags.go:203-218). Mounted. |
| AC-3 conflict on normalized collision reuses row | **complete** | `INSERT … ON CONFLICT (name_norm) DO NOTHING` then re-selects (tags.go:206-217). |
| AC-4 rename preserves links / 409 on collision | **missing** | No tag-rename (`PATCH /api/tags/{id}`) endpoint — `tags.Mount` (tags.go:44-49) registers GET/POST/DELETE and `PATCH /api/videos/{id}/tags` only; no display-name rename path, so the 409 `tag-name-exists` behavior is absent. |

### Story 9.13 — Collections (manual ordered)

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 ordered insertion | **complete** | `collections.go` AddItem/Patch persist `position`; GetItems orders by `position ASC` (collections.go:300-303). Mounted. |
| AC-2 insertion at end = MAX(position)+10 | **missing** | `AddItem` (collections.go:321-343) requires the caller to pass `position`; a POST without `position` defaults to Go zero (0), not `MAX(position)+10`. The sparse-append AC is not implemented server-side. |
| AC-3 compaction via `maktaba-api compact-collections` | **partial** | `CompactPositions` function exists (smart.go:231-244) and is correct, but there is **no `compact-collections` CLI command** wiring it (grep finds no command). |

### Story 9.14 — Smart collections

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 filter shape == saved search | **partial** | `smart.go:180-225 buildSmartSQL` builds a query, but it is a bespoke subset (`SmartFilter`) — it does **not** share Epic 7 Story 7.9's saved-search resolver, so result-set equivalence with `/search` is not guaranteed. |
| AC-2 live computation on GET items | **unwired** | `smart.go:57 LiveItems` implements live evaluation, but `collections.go:281-299 GetItems` for `is_smart` returns the **hardcoded stub** `{"items":[], "warning":"smart-collection-evaluation-not-yet-implemented"}` and **never calls `LiveItems`**. The resolver is dead code on the read path. |
| AC-3 conversion to manual (`/convert?freeze=true`) | **partial** | `smart.go:90 Freeze` materializes items and flips `is_smart=false`, but sets `smart_query = NULL` (smart.go:142) instead of moving it to `frozen_from_query` for audit as the AC requires; no `frozen_from_query` column exists. |

### Story 9.15 — Library deletion

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 catalog deletion + FK cascade incl. closing streaming sessions via gRPC | **partial** | `libraries.go:373-433 Delete` does `DELETE FROM libraries` (FK cascade handles dependent tables). But there is **no gRPC call to close streaming sessions first** (AC-1 explicitly requires closing each `streaming_sessions` via gRPC). |
| AC-2 file purge + audit row | **partial** | Purge does `os.RemoveAll(root)` per root (libraries.go:419-423) — this deletes the **entire root tree**, not "files matching `supported_video_exts` and not in `ignore_globs`" + `.maktaba/` as the AC specifies. Audit `file-purge-results` event: `AuditedDelete` (audit.go:187-213) writes a generic `library-deleted` audit but is **not mounted** (router uses plain `Delete` via `Mount`; only `MountAudit` adds the GET feed). No `?dry_run`. |
| AC-3 atomicity / 207 Multi-Status | **complete** | `libraries.go:424-430` returns 207 with `failed_paths` when unlink fails; catalog not rolled back. |

### Story 9.16 — Multi-root and overlap detection

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 N roots in one library | **complete** | `libraries.go` stores `roots []string`; create/patch accept multiple. |
| AC-2 overlap rejection at create/update | **partial** | `libraries.go:608-654 checkRootOverlap`/`pathsOverlap` rejects prefix overlap with 422 `library-roots-overlap`. But it reads the **deprecated `libraries.roots` array**, not the canonical `library_roots` table (`0043`); README says new code reads `library_roots`. |
| AC-3 canonicalization (resolve symlinks/`..`/trailing) | **partial** | `pathsOverlap` only `filepath.Clean`s — it does **not resolve symlinks** (`filepath.EvalSymlinks`), so the AC-3 symlink-resolution requirement is unmet. (`library_mgmt/roots.py:canonicalise` does resolve symlinks but is unused by the Go path.) |
| AC-4 periodic remount-overlap warning + audit | **missing** | No sweep runs (Story 9.3 unwired), so the `library-roots-runtime-overlap` warning/audit is never emitted. |

### Story 9.17 — Library audit log

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 append-only (BEFORE UPDATE/DELETE triggers raise) | **missing** | `0036_audit_log.sql` creates `audit_log` with **no triggers/functions** — only a comment says "append-only". The README's canonical schema (partitioned, `audit_log_no_mutation()` triggers, `category CHECK`, v7 UUID PK) is **not** what was migrated (BIGSERIAL id, `action`/`target_id`/`ts` columns, no partitioning). UPDATE/DELETE on `audit_log` succeed. |
| AC-2 surfaced in API (paginated, newest-first, category='library') | **partial** | `audit.go:69-137 Audit` is mounted (`MountAudit`, p6.go:61) and paginates by `id DESC` filtered to `category='library' AND target_id=$1`. Works, but no writer populates library events at runtime (settings-changed, scan-triggered, duplicate-detected are never written — those paths are stubbed/unwired). |
| AC-3 retention via monthly partitioning | **missing** | Table is not partitioned; no nightly trim/detach job exists. |

### Story 9.18 — Chapter inference

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC-1 pipeline stage (`chapter_infer`, cosine-drop windows) | **unwired** | `library_mgmt/chapter.py` implements adjacent-window cosine-drop boundary detection with `min_chapter_sec`. The `chapter_infer` stage is in migration `0049`'s CHECK but **not in the daemon's `Stage` dispatch map** — a `chapter_infer` job would be failed as "dispatch_unknown_stage". |
| AC-2 output table (N+1 `chapters`, idempotent delete) | **unwired** | Logic present in `chapter.py`; no runtime caller; `chapters` rows never written by inference. |
| AC-3 title generation via embedder | **unwired** | Implemented in `chapter.py`; embedder call path not run. |
| AC-4 suppression/override + `POST /videos/{id}/chapters/reinfer` | **missing** | No `/chapters/reinfer` endpoint exists in any handler. |
| AC-5 disabled per library | **complete (logic) / unwired** | Skip logic present in `chapter.py`; orchestrator never schedules the stage. |

---

## Top gaps by impact

1. **Pipeline daemon has no real stage handlers (systemic).**
   `runtime.py:218-235` — all 7 stages are no-op placeholders; the
   added Epic-9 stages (`topic_recluster, topic_assign, categorize,
   chapter_infer`) aren't even in the dispatch map. The watcher,
   sweep, dedup, language, content-type, speaker, topic, and chapter
   modules are fully written pure logic but **imported by nothing
   outside tests**. This single gap makes Stories 9.2, 9.3, 9.4, 9.8,
   9.9, 9.10, 9.11(AC1-2), 9.18 unsatisfiable at runtime no matter how
   correct the modules are. Impact: ~9 of 18 stories non-functional.

2. **`audit_log` is not append-only and not partitioned (Story 9.17
   AC-1/AC-3).** `0036_audit_log.sql` ships a plain table with a
   misleading "append-only" comment and no `BEFORE UPDATE/DELETE`
   triggers, no partitioning, no retention. The README's canonical
   schema was never migrated. Security/compliance impact: audit rows
   are silently mutable/deletable.

3. **Story 9.7 stats: the mounted handler is the wrong one.**
   `router/p6.go:59` mounts `libHandler.Mount` → `h.Stats`
   (libraries.go:478) which omits `by_content_type`, `storage`,
   `jobs`, `last_sweep` and returns `processed_pct:0` for empty
   libraries. The AC-compliant cache-backed `StatsCached` (stats.go)
   and `MountStatsCached` are dead code. No cache triggers exist; the
   <50ms SLA is unmet.

4. **Story 9.14 smart collections read path is a stub.**
   `collections.go:292-298` returns a hardcoded
   "not-yet-implemented" warning for smart collections; the working
   `LiveItems` resolver (smart.go) is never called from `GetItems`.
   Smart collections return nothing.

5. **Manual scan never enqueues a job (Story 9.6 AC-1).**
   Production wiring `&libraries.Handler{DB: d.DB}` (p6.go:58) leaves
   `JobEnqueuer` nil, so `POST /libraries/{id}/scan` returns 202 with
   empty `job_id` and no job is created; `?rehash` is not parsed at
   all.

6. **Missing API endpoints:** topic rename
   (`PATCH /libraries/{id}/topics/{id}` — 9.9 AC-2), tag rename
   (9.12 AC-4), chapter reinfer (`POST /videos/{id}/chapters/reinfer`
   — 9.18 AC-4) — none exist in any router.

7. **Library config validation bypassed (9.1 AC-1/AC-3).** Go PATCH
   blind-merges settings with no schema check and emits no
   `library.settings_changed` NOTIFY; the Python validator is unused
   by the API.
