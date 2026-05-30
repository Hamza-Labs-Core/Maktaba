# Epic 24 — Data Integrity: Spec-vs-Implementation Gap Analysis

**Verdict (one line):** Largely a façade — only Story 24.8 (identity hashing)
is genuinely complete and wired; Stories 24.1/24.2/24.5/24.7 exist as small
*unwired* helper modules that no production path invokes, and Stories
24.3/24.4/24.6/24.9 are mostly absent. The prior structural audit's 9/9 rating
is wrong, and its headline blocker (0036/0054 "incompatible schema, silently
skipped") is itself **factually incorrect**.

---

## Correction to the prior audit (specs/FULL_IMPLEMENTATION_AUDIT.md)

The prior audit claims a "duplicate `audit_log` table in migrations 0036 vs
0054 with incompatible schemas (0054 silently skipped)."

**This is false.** Inspected directly:

- `0036_audit_log.sql:13` — `CREATE TABLE IF NOT EXISTS audit_log (...)`
  (id BIGSERIAL, category, action, actor_user_id UUID, target_id, payload
  JSONB, ts).
- `0054_audit_log.sql:17-64` — **purely additive** `ALTER TABLE audit_log
  ADD COLUMN IF NOT EXISTS occurred_at / actor_ip / actor_source /
  target_kind / error_id`, plus a `DROP CONSTRAINT IF EXISTS` +
  re-`ADD CONSTRAINT ... CHECK (category IN (...)) NOT VALID`, plus two
  `CREATE INDEX CONCURRENTLY IF NOT EXISTS`. There is **no second
  `CREATE TABLE`**. The header comment explicitly documents it as the
  additive Epic-21 extension on top of slot 0036.
- The migration runner is `goose` (`api/migrate.go`), driven by numeric slot
  prefixes; both 0036 and 0054 are ordinary forward migrations. 0054 is
  **not skipped** — it runs after 0036 and mutates the same table in place.

Real (lesser) migration hazards that *do* exist, found during this review:

1. **SQLite `ADD COLUMN IF NOT EXISTS` dependency.** `0054_audit_log.sqlite.sql`
   and `0060`-style ALTERs rely on `ADD COLUMN IF NOT EXISTS` /
   `DROP COLUMN IF EXISTS`, which require SQLite ≥ 3.35. The file's own
   comment acknowledges the CHECK can't be added on SQLite (enforced "at the
   application layer in dev/test") — but no application-layer enforcement of
   the `audit_log.category` enum was found in code. (Story 24.3 AC3 SQLite
   parity gap.)
2. **Slot gap 0017–0027** is absent (jumps `0016 → 0028`). Harmless under
   goose (gaps allowed) but undocumented; a contiguous-sequence assumption
   anywhere would break.
3. **`audit_log` dual identity.** 0036 ships `ts` + `payload`; 0054 adds
   `occurred_at` + structured columns. Writers must agree which column set is
   canonical; this is a latent correctness footgun, not a migration failure.

---

## What actually exists

All of Epic 24's "core" lives in **4 tiny files (413 lines total)** under
`pipeline/src/maktaba_pipeline/integrity/`:

| File | Lines | Reality |
|---|---|---|
| `atomic.py` | 85 | Correct atomic-write recipe… but **zero production callers**. |
| `idempotency.py` | 86 | **In-memory only** store; no Postgres impl; no callers. |
| `backup.py` | 92 | JSON *manifest* dataclass only; no pg_dump/restore/CLI. |
| `verify.py` | 121 | Pure function; **wrong hash algorithm**; no DB/CLI/parity. |

Reachability check (`grep` across `pipeline/src`, excluding worktrees): every
symbol (`atomic_write_bytes`, `verify_video`, `IdempotencyKey`,
`BackupPlanner`) is imported **only by `integrity/__init__.py`**. Nothing in
the scanner, subtitle manager, job runner, or any stage calls them.

The one genuine success: **Story 24.8** is implemented separately and well in
`pipeline/src/maktaba_pipeline/identity/hasher.py` and **is wired** —
`scanner/service.py:495` and `watcher/dispatch.py:289` call `hash_file()`.

---

## Per-story AC tables

### Story 24.1 — Atomic writes for sidecar artifacts

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 atomic temp+rename for srt/vtt/json/thumb/sprite/poster | **unwired** | `integrity/atomic.py:32` implements the recipe correctly, but no sidecar writer imports it. `subtitle/manager.py` writes directly. Plan-24-01 §2.2 listed 6 writers to modify; none modified. |
| AC2 crash-safe + 24h reaper sweep | **missing** | No `sweep_stale_temps` / `STALE_TMP_AGE` anywhere in `pipeline/src` (grep empty). `pipeline/.../pipeline/reaper.py` exists but has no stale-temp sweep. |
| AC3 centralized helper + CI lint blocks bypass | **missing** | No `tools/atomic-write-lint.py` (`ls tools/` — absent). Bypassing the helper fails nothing. |
| AC4 non-atomic-rename FS fallback + warning | **missing** | No `fs_capabilities.py`, no probe, no documented fallback. `atomic.py:69` only best-effort dir fsync. |
| EC1 disk-full → `category=disk_full` | **partial** | `atomic.py:58` wraps `OSError`→`AtomicWriteError` but does **not** classify ENOSPC or emit `category=disk_full`. |

### Story 24.2 — Idempotent and resumable jobs

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 stable idempotency key `(content_hash,stage,backend,model,config_hash)` | **stub** | `idempotency.py:24` key is `(job_id, op, args_hash)` — **not the spec tuple**. No `compute_key`/`compute_config_hash`. Plan-24-02 §2.3 unmet. |
| AC2 per-segment commit + `last_segment_end_sec` + heartbeat; resume offset | **missing** | No `resume.py`, no `per_segment_commit`, no `last_segment_end_sec` usage in integrity module. STT backends not checked for `start_at_sec`. |
| AC3 sidecars regenerated from DB (projection) | **missing** | No `sidecars.py` / `regenerate()`. |
| AC4 `reprocess --from-stage` walks DAG | **missing** | No `cli/reprocess.py`; `find pipeline -name reprocess*` empty. |
| Persistence of idempotency keys | **missing** | `IdempotencyStore` Protocol's "production wraps a Postgres table" has **no implementation**; `MemoryIdempotencyStore` is process-local and uncalled. |

### Story 24.3 — Database consistency and constraints

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 FKs everywhere, explicit ON DELETE | **partial** | FKs exist per-migration (e.g., `0057:integrity_checks ... REFERENCES videos(id) ON DELETE CASCADE`). No system-wide inventory; not audited end-to-end. |
| AC2 unique invariants (content_hash, (lib,vid), (vid,seg_idx), username) | **partial/unverified** | Per-epic migrations carry some; no central enforcement. No `constraints.md`. |
| AC3 CHECK on `videos.state` / `processing_jobs.state`, SQLite parity | **partial — defect** | `0004:55` `videos_state_valid CHECK` uses **lowercase** values (`'discovered'`,`'ready'`,`'corrupted'`…). Architecture §3 / story AC3 specify **UPPERCASE** (`DISCOVERED…FAILED`). Mismatch vs spec source-of-truth. SQLite CHECK parity for audit_log enum explicitly not enforced (0054 sqlite comment). |
| AC4 soft-delete `deleted_at` + partial unique; hard delete only via gc | **missing/unverified** | No evidence of the documented soft-delete pattern in scope; `tools/constraint-lint.go` absent. |
| Typed error mapping `ErrUnique/ErrFk/ErrCheck/ErrStaleTransition` | **missing** | `grep ErrStaleTransition` in `api` — empty. No `api/internal/db/errors.go` constraint mapper found. |

### Story 24.4 — Concurrency and locking

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 job claim `FOR UPDATE SKIP LOCKED` | **partial — deviation** | `pipeline/src/maktaba_pipeline/db/jobs_claim.py:77` uses `FOR UPDATE SKIP LOCKED`. But ordering is `ORDER BY priority ASC, id ASC` (jobs_claim.py:33,77,111) vs plan-24-04 `priority DESC, created_at ASC`. Affects EC1 priority semantics. |
| AC2 watch-progress last-writer-wins, no monotonicity, audit row | **missing** | No `api/internal/store/watch_progress.go` or `http/watch_progress.go` (grep empty). |
| AC3 ChromaDB single-writer startup peer-detect | **missing** | No `chroma_lock.py` (`find -name chroma_lock*` empty). |
| AC4 pg advisory locks, released on crash/close | **missing** | No `pg_advisory` usage in `shared/api/pipeline` production code (grep only hits tests). No `advisory.py`. |

### Story 24.5 — Backup and restore

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 daily `pg_dump --format=custom`, retention, `restore --from` | **missing** | No Go backup code (`grep pg_dump\|pg_restore api` empty). `backup.py` is a JSON manifest dataclass only; `record()` even uses `path.write_text` (line 73), bypassing atomic helper. |
| AC2 SQLite `VACUUM INTO`; restore = copy | **missing** | No `VACUUM INTO` anywhere in api. |
| AC3 Chroma rebuild documented/tested | **missing** | No reprocess CLI to drive it. |
| AC4/AC5 caches & media documented out-of-scope | **partial** | `backup.py` module docstring states the intent; no operator doc. |
| EC2 verify via `pg_restore --list` | **missing** | No `verify.go`. |

### Story 24.6 — Disaster recovery

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 four documented scenarios + RTO/RPO | **missing** | No `docs/operations/disaster-recovery.md` (`ls docs/operations` empty). No `scenarios.go`. |
| AC2 `make dr-drill` nightly | **missing** | No `dr-drill` target in `Makefile` (grep empty). |
| AC3 admin Restore UI | **missing** | No `web/src/routes/admin/recovery.tsx`. |
| Scenario-3 probe→`CORRUPTED` transition | **partial** | `videos_state_valid` CHECK *does* include `corrupted` (0004:67), so the FSM value exists; but no probe code performing the hash-mismatch transition was found in scope. |

### Story 24.7 — Integrity verification

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 doctor: hash re-verify / sidecar / FK / FTS-Chroma parity | **stub — defect** | `verify.py:42` hashes **sha256 of first 16 MiB**. System identity (24.8/`hasher.py`) is **BLAKE3(head4‖tail4‖u64 size)**. The verifier's hash can **never** equal `videos.content_hash` → hash check is structurally broken. No sidecar/FK/parity checks; no `cli/integrity.py`; no sample/full modes. |
| AC2 reports → `audit_log`, admin panel | **unwired** | `verify_video()` is a pure function; never writes `integrity_checks` (table exists at `0057`) nor `audit_log`. No admin endpoint. |
| AC3 `--repair` opt-in | **missing** | No `repair.py`. |
| AC4 weekly scheduled, off in single-user | **missing** | No scheduler entry. |

### Story 24.8 — Identity stability

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 `BLAKE3(first4‖last4‖u64_le(size))`, small-file whole-hash | **complete** | `identity/hasher.py:107-184` matches the formula exactly; uniform double-feed at the 8 MiB boundary (hasher.py:166-177); size suffix always appended (hasher.py:182). |
| AC2 compute once; reuse on (path,size,mtime) unchanged | **partial** | `FileSignature` / `file_signature()` (hasher.py:101) provides the primitive and is wired into scanner; but no `resolve.py` performing the DB short-circuit decision was found. |
| AC3 move/rename keeps hash+updates path; copy reuses ready row | **missing** | No `resolve.py`, no `Update`/`Supersede` decision type, no `superseded_by` column (`grep superseded_by pipeline/src` empty; no `0060_videos_supersede.sql`). |
| AC4 boundary suite (small/sparse/8MiB/in-place) | **partial** | `hasher.py` handles all boundary cases correctly in code; dedicated identity boundary integration suite not confirmed present. |

### Story 24.9 — Forward and backward compatibility

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 add-nullable→backfill→NOT-NULL playbook (22.4 lint) | **out-of-scope/unverified** | `tools/migration-lint/main.go` exists but its rule set was not verified to enforce this. |
| AC2 artifacts carry `schema_version`; readers tolerate | **missing** | No `compat/schema_version.py` (`find -name schema_version.py` empty); no `read_versioned`. |
| AC3 cache keys carry platform-major prefix | **missing/unverified** | No `api/internal/cache/keys.go` major-prefix helper confirmed. |
| AC4 forward-compat fixture suite | **missing** | No `tests/forward_compat/` directory. |
| Client major-version refusal (WS 4001) | **missing** | No WS major-mismatch close-code logic found. |

---

## Status roll-up (≈ AC-level)

- **Complete:** 1 (24.8 AC1).
- **Partial:** ~9 (24.1 EC1; 24.3 AC1/AC2/AC3; 24.4 AC1; 24.6 scenario-3 FSM
  value; 24.8 AC2/AC4; 24.9 AC1).
- **Stub:** 3 (24.2 AC1; 24.7 AC1; — wrong-shape implementations present).
- **Unwired:** 3 (24.1 AC1 atomic helper; 24.7 AC2 verify; 24.2 idempotency
  store).
- **Missing:** ~22 (all of 24.5, 24.6 doc/drill/UI, 24.9 AC2–client,
  24.2 AC2–AC4, 24.4 AC2–AC4, 24.1 AC2–AC4, 24.3 AC4+errors, 24.8 AC3).

Net: **far below** the prior audit's 9/9. Of 9 stories, **1 substantially
implemented (24.8)**, 0 fully meeting all ACs, 8 partial-to-absent.

---

## Top gaps by impact

1. **Atomic-write helper is dead code (24.1).** `integrity/atomic.py` is
   correct but **no sidecar writer calls it** and no CI lint forces it.
   Subtitle/sprite/thumbnail/poster/segment-json writers still write
   non-atomically. The epic's central durability promise ("artifacts never
   appear half-written") is **not delivered on any real path**.

2. **Integrity verifier uses the wrong hash (24.7 AC1).** `verify.py:42`
   computes sha256 over the first 16 MiB; the system identity is
   BLAKE3(head4‖tail4‖u64 size) (`identity/hasher.py`). The
   `expected_hash` comparison can never succeed against a real
   `videos.content_hash`, so corruption detection — the entire point of
   Story 24.7 and DR scenario #3 — **silently never fires**. Compounded by
   `verify_video()` never writing the `integrity_checks` table (which exists,
   `0057`) and having no CLI/schedule.

3. **Backup/restore entirely absent (24.5).** No `pg_dump`, `pg_restore`,
   `VACUUM INTO`, retention GC, verify, or `restore --from` CLI exists;
   `backup.py` is just a manifest dataclass. There is **no tested recovery
   path**, which also voids Story 24.6 (DR doc/drill/UI all missing).

4. **Idempotency/resume unimplemented (24.2).** Key shape is wrong
   (`job_id,op,args_hash` vs `content_hash,stage,backend,model,config_hash`),
   store is in-memory and uncalled, and there is no per-segment-commit /
   `last_segment_end_sec` resume protocol — a 60-min crash restarts from
   zero.

5. **Prior-audit blocker is a misdiagnosis.** The flagged 0036/0054
   "incompatible duplicate, silently skipped" does not exist; 0054 is a
   well-formed additive ALTER applied by goose after 0036. Acting on the
   audit's recommendation would waste effort while the real gaps (1–4 above)
   stay unaddressed. The genuine migration nits are minor: undocumented
   0017–0027 slot gap, SQLite ≥3.35 `ADD COLUMN IF NOT EXISTS` dependency,
   and lowercase `videos.state` CHECK values diverging from the
   uppercase architecture-§3 source-of-truth (24.3 AC3).

---

## Wave-2 residual-closure progress (branch `w2/e24`)

### CLOSED

- **HLB-406 (24.7 AC1, verify.py wrong hash)** — DONE in Wave-1 (W1-C1,
  commits `d8bbcdd` + `9e72046`). `verify.py` now uses the canonical
  BLAKE3 identity; not re-touched in Wave-2.

- **HLB-405 (24.1 AC1, atomic-write helper dead code)** — **CLOSED**
  (commit `dc55435`). Finding refined: the real on-disk artifact write
  path is `subtitle/manager.write_atomic` (reached in production via
  `subtitle_gen.commit_subtitles` ← the `SUBTITLE_GEN` handler). It was
  a *divergent reimplementation* of the atomic recipe **missing the
  canonical directory fsync** — a crash after `os.replace` but before
  the dir entry was durable could lose the rename. The canonical
  `integrity.atomic_write_bytes` (correct: `O_EXCL` temp + file fsync +
  `os.replace` + dir fsync) was dead (only imported by
  `integrity/__init__.py`). Fix: `write_atomic` now **delegates** to the
  one canonical helper (DRY — no second impl; helper no longer dead).
  This is the only real Python on-disk artifact writer —
  transcript/probe/extract persist to DB JSON columns (not files) and
  the audio cache is ffmpeg subprocess output, so there is no other
  non-atomic file-publish path to wire. Pinned by two
  `@pytest.mark.unit` tests (delegation proof + interrupted-rename
  leaves-no-torn-file), both failing on the pre-fix divergent impl.

### DEFERRED (tracked — net-new subsystems, not stubbed)

These are large net-new subsystems; per Wave-2 discipline they are
explicitly deferred rather than fake-completed with non-functional
stubs. Concrete options recorded for the follow-up wave:

- **NOTE(HLB-407 — backup/restore, 24.5):** entirely absent (no
  `pg_dump`/`pg_restore`/`VACUUM INTO`/retention/verify/`restore --from`;
  `backup.py` is a manifest dataclass). Deferred. Tractable next slice:
  a Go `pg_dump --format=custom` + retention-GC + `restore --from` CLI
  in `api/` with a `pg_restore --list` verify, plus a
  `docs/operations/disaster-recovery.md` runbook driven by a
  `make dr-drill` smoke target. Not started in Wave-2 — wiring the dead
  atomic helper (HLB-405) was the higher-value tractable prize and was
  prioritised to avoid a shallow stub here.

- **NOTE(HLB-408 — idempotency/resume, 24.2):** key shape wrong
  (`job_id,op,args_hash` vs spec
  `content_hash,stage,backend,model,config_hash`), store in-memory and
  uncalled, no per-segment-commit / `last_segment_end_sec` resume
  protocol. Deferred. Tractable next slice: (a) `compute_key` /
  `compute_config_hash` to the spec tuple, (b) a Postgres-backed
  `IdempotencyStore` impl, (c) an idempotent-resume guard on the one
  concrete `transcript_segments` per-segment-commit path with a
  `last_segment_end_sec` resume offset. Not started in Wave-2.

- **NOTE(HLB-409 — DB consistency/locking, 24.3/24.4):** mostly absent
  (no typed constraint-error mapper, no `watch_progress` LWW, no
  ChromaDB single-writer lock, no pg advisory-lock helper; `jobs_claim`
  ordering deviates `priority ASC` vs spec `priority DESC`). Deferred —
  net-new and lower per-unit value than HLB-405.

Pre-existing, out-of-scope observation (not changed): the existing
`tests/subtitle/test_manager.py` / `tests/integrity/test_integrity.py`
suites carry **no** `@pytest.mark.unit` marker, so they are *not* run by
CI's `make test-unit` (`pytest -m unit`). The two Wave-2 tests added
here ARE `@pytest.mark.unit`-decorated so they execute in CI. Marking
the legacy suites is a separate hygiene task outside Epic-24 Wave-2
scope.
