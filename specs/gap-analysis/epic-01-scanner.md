# Epic 01 — Scanner: Spec-vs-Implementation Gap Analysis

**Verdict:** Unit-level building blocks (walker, hasher, FSM, watcher dispatch, migrations) are well-built and tested, but **the entire epic is unwired**: no production `ScanStore`/`WatcherStore` exists, the `scan` stage handler is a placeholder, the `Watcher` is never started, and the `videos.new` → WebSocket fan-out has no Postgres listener. A discovered file never becomes a `videos` row in any real boot path.

---

## Method

For each AC I located the implementing code, traced it from a real entrypoint (`pipeline/src/maktaba_pipeline/__main__.py` → `runtime.run` → `build_default_dispatch`; API `api/internal/handlers/libraries/libraries.go`), and classified:

- **complete** — code exists, reachable from a live boot path, behaviorally satisfies the AC.
- **partial** — implemented but missing required behavior.
- **missing** — no implementing code.
- **unwired** — code exists and is correct in isolation but never invoked from any live path.
- **stub** — returns a placeholder/fake instead of doing the work.

---

## Story 1.1 — Bootstrap a library and walk its roots

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 1.1-AC1 | Scan inserts 1,000 `discovered` rows w/ hash/path/size/mtime + one `probe` job each | **unwired** | `scanner/service.py:330-441` (Scanner.run), `:466-568` (_process_candidate). No production `ScanStore`: only `DryRunStore` (`scanner/dryrun.py:27`) + test fakes implement `save_candidate`. `runtime.py:189` maps `Stage.SCAN` to `_placeholder_handler("scan")` which only logs + `mark_done` (`runtime.py:218-235`). `__main__.py:35-42` `_DEFAULT_STAGES` **excludes `Stage.SCAN`** entirely. No `dispatch_overrides` passed in `__main__.py:118`. | Orchestrator logic is correct but never executed: a `scan` job is marked `done` without walking/hashing/inserting anything. No live code constructs a `Scanner` with a real DB-backed store. |
| 1.1-AC2 | API receives `videos.new` LISTEN/NOTIFY; WS fan-out count == inserted-row count | **unwired** | Trigger exists: `shared/db/migrations/0005_videos_new_notify.sql:29-57` (`videos_notify_new_trg`). API side: `api/internal/handlers/ws/ws.go:182-187` `PublishFromCtx` is "Currently a no-op adapter shape; left in place so service bootstrap can wire it." `grep` for `videos.new` / `videos_new` across `api/` Go finds **zero** LISTEN consumers. | The SQL trigger fires `pg_notify('videos.new',…)` but nothing in the API LISTENs and routes it into the WS Hub. End-to-end frame-count invariant is not enforceable; no integration path. |
| 1.1-AC3 | Only the 7 supported extensions ingested; others ignored, no log noise > DEBUG | **complete** | `scanner/walker.py:42-44` `DEFAULT_VIDEO_EXTENSIONS`, `:240-248` `_is_acceptable_basename` (lowercased ext check), `:185-186` hidden skip. Non-matches return silently (no log). | None — walker logic is correct and used by both Scanner and Watcher. (Still gated by AC1's unwired path for end-to-end effect.) |

Edge cases: symlink-loop guard (`walker.py:201-208`), permission-denied logged once at WARN (`walker.py:265-281`), zero-byte skip (`service.py:487-492`), small-file hash fallback (`identity/hasher.py:158-178`), zero-roots WARN (`service.py:358-365`) — all implemented correctly at unit level.

## Story 1.2 — Content-addressable identity (BLAKE3)

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 1.2-AC1 | `content_hash = lowerhex(BLAKE3(first4MiB ‖ last4MiB ‖ size_le_u64))` | **complete** | `identity/hasher.py:133-184` `hash_reader`: head/tail reads, `:182` `size.to_bytes(8,"little",signed=False)`, `:184` `h.hexdigest()`. Format `^[0-9a-f]{64}$` enforced by `shared/db/migrations/0003_videos_content_hash.sql:53-65`. | None at unit level. |
| 1.2-AC2 | Duplicate bytes in same library → only first inserts; second logs `duplicate_content_hash` (INFO) and is linked via `metadata.additional_paths` | **missing** | `scanner/service.py:556-568`: on `ON CONFLICT` (not inserted) the code only does `result.files_skipped += 1` and logs `scanner.video_already_present` at **DEBUG**. No `additional_paths` write, no `duplicate_content_hash` INFO log anywhere (`grep` finds zero `additional_paths`/`duplicate_content_hash` in `pipeline/src/`). A `dedup.decide()` with a `DUPLICATE` outcome exists (`library_mgmt/dedup.py:40-46`) but is an Epic 9.4 module never imported by the scanner orchestrator, and it records to "audit log" not `metadata.additional_paths`. | The `additional_paths` JSON-list linking required by the AC does not exist; wrong log level (DEBUG vs INFO) and wrong event name. |
| 1.2-AC3 | Duplicate bytes across libraries → independent rows keyed `(library_id, content_hash)` | **complete** | `shared/db/migrations/0003_videos_content_hash.sql:36-37` drops global `videos_content_hash_key`, adds `UNIQUE (library_id, content_hash)`. Save path keys on `(library_id, content_hash)` (`service.py` SaveCandidateParams + ON CONFLICT comment `:557`). | Schema correct; behavioral verification blocked by AC1 unwired save path. |
| 1.2-AC4 | 30 GB file → ≤ 8 MiB read, two seeks + 8 MiB sequential | **complete** | `identity/hasher.py:158-178`: `ht=min(head_tail,size)`, one seek+read of head, one seek+read of tail, no full read. Peak mem `2×head_tail`. | None. |

Edge cases: small-file full-content fallback (`hasher.py:166-173`), sparse/size-suffix (`:182`), recompute-on-size-change handled by signature mismatch (`service.py:571-583`) — implemented.

## Story 1.3 — Watch for live filesystem changes

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 1.3-AC1 | `watch=true`: copied file → 1 new row + 1 probe within 2×debounce+1 s | **unwired** | `watcher/service.py:302-484` `Watcher`, `:178-401` `WatcherDispatcher`. Debouncer defaults `debounce_sec=2, settle_sec=1, settle_ticks=2` (`watcher/debouncer.py:80-82`). But `grep` for `Watcher(` / `watcher.start` / `add_library` outside `watcher/` finds **zero** — nothing in `__main__.py`/`runtime.py` ever constructs or starts a `Watcher`. No production `WatcherStore`. | Watcher is never started by the daemon; `WatcherStore` has no real implementation. Inert. |
| 1.3-AC2 | In-progress copy (mtime advancing) not ingested until size stable for one debounce interval | **unwired** | Size-stability + mtime-quarantine logic in `watcher/debouncer.py` (`:8-17` docstring, `settle_ticks` gate). Correct at unit level. | Same as AC1 — debouncer never runs because Watcher is never started. |
| 1.3-AC3 | Rename within library → `videos.path` updated; no new row, no stage re-run | **unwired** | `watcher/dispatch.py:239-278` `_handle_move` → `update_video_path` (no FSM, no probe). Logic correct. | Unwired (no Watcher boot, no store impl). |
| 1.3-AC4 | File deleted → row soft-deleted to `MISSING`; derived data preserved | **unwired** | `watcher/dispatch.py:213-237` `_handle_delete` → `soft_delete_by_path`, which (per Protocol docstring `:136-147`) must call `advance_after_stage(FILESYSTEM, DELETED)` → `MISSING`. `orchestrator/advance.py:84-143` implements that transition; FSM allows it (`domain/states.py:194-200` broadcast row). Logic correct. | Unwired: no production `WatcherStore.soft_delete_by_path`; Watcher never started. |

Edge cases: NFS/no-inotify fallback — `sweep.py` (Epic **9.3** module, `:1-3`) provides a periodic re-walk, but it is not wired into the watcher's per-root `statvfs`-driven fallback the story describes; `grep statvfs` finds none. `*.part`/`*.crdownload`/`*.tmp` ignore (`walker.py:49-54`, `watcher/service.py:267-269`) and `.maktaba/` ignore (`walker.py:59`) implemented.

## Story 1.4 — Manual control surface

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 1.4-AC1 | Re-`POST /scan` while running → 200 `{status:"already_running", progress}` | **missing** | `api/internal/handlers/libraries/libraries.go:436-463` `Scan`: only validates id, loads library, calls `JobEnqueuer.EnqueueScan(...,50)` and returns `202 {job_id}`. No running-scan check, no `already_running` status, no `progress`. If `JobEnqueuer==nil` it soft-returns `202 {job_id:""}` (`:450-455`). No advisory lock anywhere (`grep pg_try_advisory_lock` for scan → none; only reaper has one). | The idempotency/`already_running` contract is entirely absent from the only HTTP entrypoint. `pg_try_advisory_lock(hashtext('scan:'||library_id))` from the edge case does not exist. |
| 1.4-AC2 | `DELETE /api/libraries/{id}/scan` → scanner stops ≤5 s, rolls back batch, no orphaned jobs | **missing** | `libraries.go:124-132` `Mount` registers only `r.Post(".../scan", h.Scan)`. **No `DELETE` route.** Scanner-side cancel polling exists (`scanner/service.py:381-424`, `ScanControlStore`) and migration `0008_scan_control.sql` adds `cancel_requested`; but nothing sets it (no DELETE handler, CLI `--cancel` deferred at `scanner/cli.py:110-120` returning exit 64). | No API surface to request cancellation; the read side (`poll_scan_control`) has no producer. |
| 1.4-AC3 | `maktaba-pipeline scan --library NAME --dry-run` prints would-be inserts, writes nothing | **complete** | `scanner/cli.py:103-153` `main`: `--dry-run` builds `DryRunStore` (`scanner/dryrun.py:56-77` writes JSONL, returns synthetic id, no DB) and runs `Scanner` with `ScanOptions(dry_run=True)`. Non-dry-run/`--cancel` exit 64. | Works as a standalone CLI. (Only the dry-run subset is functional, which is exactly what the AC scopes.) |

Edge cases: CLI/gRPC advisory-lock coexistence — **not implemented** (no advisory lock for scan). Library-deleted-mid-scan — `scanner/service.py:416-424` handles it via `poll_scan_control().library_deleted`, but that poll has no production store, so unwired.

## Story 1.5 — Schema & ownership decisions

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 1.5-AC1 | Migration `000X_videos_unique_per_library.sql` drops global `UNIQUE(content_hash)`, adds `UNIQUE(library_id, content_hash)`; single owner | **partial** | Done by `shared/db/migrations/0003_videos_content_hash.sql:28` (drop global) `:36-37` (add per-library). Behaviorally correct + idempotent (`IF [NOT] EXISTS`), tested by `pipeline/tests/db/test_content_hash_migration.py`. | Filename differs from the spec's named `000X_videos_unique_per_library.sql` (it is `0003_videos_content_hash.sql`). Cosmetic but the story explicitly names the file as the "single owner"; constraint behavior itself is complete. |
| 1.5-AC2 | `domain/states.py` includes `MISSING` non-terminal sink with one transition back to `DISCOVERED` on rediscovery | **complete** | `domain/states.py:61` `MISSING`, `:135` `STATE_CLASS[MISSING]="sink"`, `:179` `_Key(MISSING, SCAN, REDISCOVERED) → DISCOVERED`, `:194-200` broadcast `→ MISSING` from open/terminal-good/sink. | None. |
| 1.5-AC3 | `--purge-missing` CLI flag, default off, prompts before delete unless `--yes` | **missing** | `scanner/cli.py:61-100` `build_parser` defines only `--library/--root/--library-id/--follow-symlinks/--dry-run/--cancel`. `grep purge.missing\|purge_missing\|--purge` across `pipeline/src/` → **zero hits**. | The `--purge-missing` flag and its ≥7-day age gate / confirmation prompt do not exist anywhere. |

## Story 1.6 — Video state machine

| AC | Text | Status | Evidence | Gap |
|----|------|--------|----------|-----|
| 1.6-AC1 | `states.py` lists exactly the 12 states; out-of-set write fails a unit test | **complete** | `domain/states.py:44-64` `State` enum = the 12 canonical values. Migration `0004_video_states_and_stages.sql` adds `videos_state_valid CHECK`. | None. |
| 1.6-AC2 | `domain/stages.py` lists exactly the 7 stages; `processing_jobs.stage` CHECK matches | **complete** | `domain/stages.py:1-15` re-exports `Stage` (`states.py:67-83`, 7 values). `0004_*.sql` re-asserts `processing_jobs_stage_valid`; `0002_processing_jobs.sql` has `processing_jobs_stage_chk`. | None. |
| 1.6-AC3 | Migration adds CHECKs; legacy `thumb` rewritten in same migration | **complete** | `0004_video_states_and_stages.sql:` `UPDATE processing_jobs SET stage='thumbnail' WHERE stage='thumb'` then ADD CHECKs; idempotent. | None. |
| 1.6-AC4 | `allowed_transitions: dict[State,set[State]]` exposed; every state-changing UPDATE reads it; illegal raises `IllegalStateTransition` | **partial** | `domain/states.py:253-274` exposes `allowed_targets: dict[State,frozenset[State]]` and `allowed_transitions` (triple-keyed). `orchestrator/advance.py:124-126` raises `IllegalStateTransition` on `lookup` miss. **But** the story names the symbol `allowed_transitions` as the `dict[State,set[State]]` shape; the actual `allowed_transitions` is a `dict[_Key,State]` and the `dict[State,set[State]]` view is named `allowed_targets` instead — a name/shape mismatch vs the AC. The "every state-changing UPDATE reads from this table" invariant relies on a lint quarantine (`advance.py:6-8` references it) that I could not confirm is configured. | Symbol naming/shape diverges from AC wording; enforcement that `advance_after_stage` is the *only* writer is asserted by docstring + a claimed (unverified) ruff/forbidigo rule, not structurally guaranteed. |
| 1.6-AC5 | `advance_after_stage(video_id, stage, outcome)` is the **only** code path mutating `videos.state`; other writers call it | **partial / unwired** | `orchestrator/advance.py:84-143` implements lock → re-read → terminal-drop no-op (`:114-122`) → `lookup` → UPDATE → NOTIFY/bus. Correct. However it takes a `DBConn` Protocol and has **no production caller**: `grep` shows it is invoked only via the watcher's `WatcherStore.soft_delete_by_path`/`rediscover` Protocols (no impl) and tests. The single-writer invariant is sound by design but unenforced at runtime (no caller, lint rule unverified). | Function is correct but never called from a live path; "all other writers call this" is unverifiable since no production state-mutating code is wired. |

State-machine test cases (round-trips, subtitle_gen-not-advancing, supersede-preserves-transcripts, corrupted-blocks) are supported by the table in `domain/states.py:168-219` and `TERMINAL_DROP_STATES` (`:146-148`) — logic complete; runtime exercise blocked by AC5 unwired caller.

---

## Top gaps (ordered by impact)

1. **The scanner never runs.** `runtime.py:189` binds `Stage.SCAN` to `_placeholder_handler` (log + `mark_done`, `runtime.py:218-235`); `__main__.py:35-42` even excludes `SCAN` from `_DEFAULT_STAGES`; no `dispatch_overrides` wire the real `Scanner` (`__main__.py:118`). No production `ScanStore` (`save_candidate`) exists — only `DryRunStore` and test fakes. **Net effect: a `scan` job is marked done without discovering, hashing, or inserting a single `videos` row.** This nullifies Story 1.1-AC1/AC2, Story 1.2-AC2/AC3 end-to-end, and is the prerequisite for every other Scanner story.

2. **`videos.new` → WebSocket fan-out is not connected (1.1-AC2).** The SQL trigger fires correctly but no Go LISTEN loop consumes it; `ws.go:182-187` `PublishFromCtx` is an explicit no-op placeholder. The headline real-time-discovery invariant cannot hold.

3. **The live watcher is never started (1.3-AC1..AC4).** `Watcher`/`WatcherDispatcher`/`Debouncer` are complete and well-tested in isolation but no boot path constructs a `Watcher` and no production `WatcherStore` exists. Live ingestion, rename→path-update, and delete→`MISSING` soft-delete are all inert.

4. **Manual control surface largely absent (1.4-AC1, 1.4-AC2).** `POST /scan` only enqueues a job and returns `202` — no `already_running`/progress, no advisory lock. There is **no `DELETE /scan` route at all**, so cancellation (and its scanner-side `poll_scan_control` plumbing) has no producer.

5. **Story 1.2-AC2 duplicate-linking missing.** On `(library_id, content_hash)` conflict the scanner only bumps a counter and logs at DEBUG; the required `metadata.additional_paths` JSON-list append and INFO `duplicate_content_hash` log do not exist. The Epic-9 `dedup.decide()` is not wired into the scanner.

6. **`--purge-missing` CLI flag missing entirely (1.5-AC3).** No flag, no ≥7-day age gate, no confirmation prompt anywhere in `pipeline/src/`.

7. **FSM single-writer guarantee is design-only (1.6-AC4/AC5).** `advance_after_stage` is correct but has no production caller; the "only sanctioned mutator" invariant rests on an unverified lint rule, and the `allowed_transitions` symbol shape/name diverges from the AC.
