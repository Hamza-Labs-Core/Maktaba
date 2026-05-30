# Epic 19 — Scalability: Spec vs Implementation Gap Analysis

**Verdict (one line):** Single-host primitives that scalability *reuses* exist (content_hash, transcode slot cap, job-queue SKIP LOCKED/heartbeat/reaper, in-process GPU lock, single-user-sentinel auth, STT budget cap) — but **every multi-host artifact Epic 19 actually owns is missing**: no `events`/`streaming_replicas`/`library_budgets` tables, no Postgres LISTEN/NOTIFY WS bus, no `last_event_id` cross-replica replay, no read-replica routing, no backup/restore/replica ops, no `make capacity` harness, no migration-safety lint, no ChromaDB single-writer guard, no cross-host GPU advisory lock. `api/internal/scale/scale.go` is an in-process stub explicitly disclaiming the adapters.

## Methodology / classification

Each AC was traced to code and classified:

- **complete** — code exists, reachable, behaviorally satisfies the AC.
- **partial** — core exists but a material sub-requirement is unmet.
- **missing** — no code addresses the AC.
- **unwired** — code exists but nothing calls it / not reachable in product path.
- **stub** — interface/placeholder exists, explicitly non-functional.

Key interpretation per the brief: for each AC I judged whether the spec demanded a **real multi-host adapter** or only an **interface contract**. Epic 19's stories (19.2–19.5) and their plans are unambiguous: they specify concrete tables (`events`, `streaming_replicas`, `library_budgets`), concrete SQL (`pg_notify`, `pg_last_xact_replay_timestamp`), concrete ops scripts (`setup-replica.sh`, `pg_dump.sh`), and concrete test harnesses (`make capacity`, `make migrate:lint`). These are deliverables, not "interface only." `scale.go`'s own header concedes the Postgres/Redis adapters are deferred — that deferral is itself the gap, because the stories require the adapters, not just the interface.

---

## Story 19.1 — Single-host capacity floor

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — Mac mini sustains 50k videos / 1M segments / 8 direct / 4 transcoded / 1 transcribe + 4 index | **missing** | No reference profile. `shared/perf_budgets.yaml:25-27` defines only `mac-m2-8gb` and `linux-x86-16gb`; no `mac-mini-m2-16gb-30tb`, no `videos_max`/`segments_max` capacity numbers (plan §0/§8 require them). Nothing asserts the floor. |
| AC2 — library landing p95 ≤ 500 ms with catalog loaded | **partial** | `perf_budgets.yaml:30-37` sets `libraries_list` p95=80ms and `search_warm` p95=500ms on `linux-x86-16gb`, but not measured against a 50k-video / 1M-segment loaded catalog on the reference profile. Budget exists; the *capacity-loaded* assertion does not. |
| AC3 — survives 30 TB scan, FDs documented (`ulimit -n ≥ 4096`) | **missing** | No `scripts/set-ulimit-mac.sh`/`set-ulimit-linux.sh`/`check-fd-limit.sh` (plan §1, §10). `ls scripts/` has no ulimit/fd tooling. Bounded-memory walker exists (`pipeline/src/maktaba_pipeline/scanner/walker.py:1-19`, iterative scandir) but the 30 TB survival is unasserted. |
| AC4 — `make capacity` runs 30-min mix, fails on budget breach | **missing** | No `tests/capacity/` directory; no `capacity` target in `Makefile` (only `migrate`/`migrate-status` at 272-284). The entire workload-mix driver (plan §2, §10) is absent. |
| EC1 — slow USB caps direct play at 720p | **partial** | Streaming has a 720p direct-degraded fallback path (`streaming/internal/slots/slots.go:23-24` `DecisionDirectCap` "capped at 720p"), but it is triggered by slot exhaustion, not by a storage-speed probe. No `streaming/internal/quality/cap.go` / `ProbeSeqMBps` (plan §6). |
| EC2 — file-watch falls back to polling | **partial** | Watcher debouncer exists (`pipeline/src/maktaba_pipeline/watcher/debouncer.py:1-30`); a poll fallback config knob is referenced in plan-19-06 §9 but no inotify-exhaustion `OSError(ENOSPC/EMFILE)` → polling switch verified in `watcher/service.py`. |
| EC3 — SQLite profile documented at 1/4 | **missing** | No `mac-mini-m2-16gb-sqlite` profile in `perf_budgets.yaml`; no `make capacity-sqlite`. |

## Story 19.2 — API horizontal scale-out

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — 2 replicas behind LB, identical responses, JWT validates on either | **partial** | API auth is per-request JWT/JWKS (stateless by construction; `api/internal/auth/middleware/middleware.go`). No code prevents 2 replicas, but there is no test/harness proving cross-replica identical responses, and no LB config artifact. Statelessness is plausible; the AC's *demonstrated* multi-replica behavior is unverified. |
| AC2 — WS event on replica B reaches client on replica A ≤ 250 ms via Postgres LISTEN/NOTIFY | **missing** | `api/internal/handlers/ws/ws.go:16-18` Hub is in-memory single-process; comment says "Production wires the Hub to a Postgres LISTEN loop" but **no LISTEN loop, no `pg_notify`, no `eventbus/postgres.go` exists** (grep for `pg_notify`/`pq.NewListener` in api/ finds only comments + the `scale.go` stub). `scale.go:48-52` `EventBus` is an in-process `InMemoryBus` only. Cross-replica fan-out does not exist. |
| AC3 — `events` table durable replay; ≤8 KiB NOTIFY bound; `last_event_id` cursor; 7-day prune; monotonic id | **missing** | No `events` table in `shared/db/migrations/` (grep across all migrations: none). No `00xx_events_table.sql`, no `replay.go`, no `pruner.go`, no `ringbuf.go`, no `last_event_id` handshake (grep `last_event_id` in api/: none). Plan §1-§7 entirely unimplemented. |
| AC4 — rolling restart doesn't drop WS subs on other replica | **missing** | No `api/internal/healthz/` drain handler (`ls api/internal/` shows no `healthz`); no SIGTERM drain → 503 → `close 1012` flow (plan §8). Single-host only. |
| EC1 — NOTIFY overflow → poll fallback within 5 s | **missing** | No `overflow.go`/`OverflowDetector` (plan §6). |
| EC2 — `now()` from Postgres not Go for tie-breaking | **missing** | Not applicable/unimplemented because the cross-replica event path does not exist. |
| EC3 — JWKS rotation visible to other replica ≤ 5 min | **partial** | JWKS cache with TTL exists (`api/internal/auth/keys/`); the 5-min cross-replica propagation is a property of per-replica TTL caching and is plausibly satisfied for JWKS specifically, but untested in a 2-replica rig. |

## Story 19.3 — Streaming horizontal scale-out

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — 2 replicas behind consistent-hash LB, no cross-replica cache miss per session | **missing** | No `streaming/internal/replicas/` registry; no `streaming_replicas` table (migration 0039 `streaming_sessions` has no `replica_id`/`advertise_url` column — `grep replica_id` in 0039: none). `session/session.go:7` comment claims "consistent-hash routing" but no replica registry/pin code backs it. |
| AC2 — `OpenSession` selects local store, signed URL embeds replica cache origin | **missing** | No `replica_id` written on session open; no `advertise_url`/`ReplicaUrl` in OpenSession response (plan §4). Single-host sessions only. |
| AC3 — hashed replica down → reroute → `session_invalidated` → reopen, watch pos preserved | **missing** | No `session/invalidate.go`; grep `session_invalidated` in streaming/ → none. `streaming/internal/session/reaper.go` is the Story 8.9 *idle* reaper (closes idle FFmpeg), not a cross-replica failover handler. Watch-state resume exists elsewhere (Epic 9) but the failover trigger does not. |
| AC4 — `EvictHashCache` propagates to all replicas via gRPC fan-out | **partial→unwired** | `streaming/internal/grpcsrv/server.go:265-272` `EvictHashCache` exists but is **local-only** (`s.Probe.EvictHash(hash)`); no `cache/evict_fanout.go`, no peer-replica gRPC fan-out, no `NoFanout` loop-guard (plan §6). The single-replica behavior works; the AC's "all replicas" requirement is unmet. |
| EC1 — duplicate session_id → 409 | **partial** | `streaming_sessions(id PRIMARY KEY)` gives PK conflict, but no explicit 409→fresh-session client contract verified. |
| EC2 — disk full → LRU 503 + LB drains replica | **missing** | No `cache/disk_pressure.go`, no registry-backed drain (plan §8). |
| EC3 — segment PTS from FFmpeg not wall-clock | **partial** | FFmpeg segmenting exists (`streaming/internal/ffmpeg/`); `-copyts`/no-`PROGRAM-DATE-TIME` assertion (plan §9) not verified here. |

## Story 19.4 — Pipeline horizontal scale-out

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — N workers on distinct hosts drain queue in ≤ T/N+ε, ≤1 Chroma writer | **partial** | Multi-worker claim works (`pipeline/src/maktaba_pipeline/db/jobs_claim.py:63-81` `FOR UPDATE SKIP LOCKED`, Epic 6). But the ChromaDB single-writer constraint is **unenforced** (see TC4) so the "≤1 ChromaDB-writing job" invariant is not guaranteed; no two-host drain harness. |
| AC2 — `SELECT … FOR UPDATE SKIP LOCKED` exactly-once | **complete** | `jobs_claim.py:63-81` atomic claim with `FOR UPDATE SKIP LOCKED` + `state='claimed'` UPDATE; SQLite emulation noted (`jobs_claim.py:9-13`). Behaviorally exactly-once across workers. (Delivered by Epic 6, reused here.) |
| AC3 — GPU job claims per-device advisory lock keyed by `(host_id, device_id)` | **partial** | `pipeline/src/maktaba_pipeline/pipeline/concurrency.py:1-21` uses an **in-process `asyncio.Lock` per GPU device** (Story 6.7). This serializes only *within one host's worker process*. The spec/plan §6 require `pg_advisory_xact_lock(hashtext(host||':'||device))` so two GPU jobs on the same physical device **across hosts** serialize — that cross-host lock does not exist. Single-host correct; multi-host unsatisfied. |
| AC4 — adding a worker host needs only config, no code change | **partial** | Plausibly true for claim/heartbeat (config-driven `worker_id`), but unverified and undermined by AC3 (cross-host GPU) and TC4 (Chroma) gaps. |
| EC1 — worker dies → reaper requeues after heartbeat×3, resume from `last_segment_end_sec` | **complete** | Reaper (`pipeline/src/maktaba_pipeline/pipeline/reaper.py:1-23`, `db/jobs_reaper.py:59` SKIP LOCKED) requeues stale claims; stale=18×heartbeat enforced. Resume offset persisted (`jobs_claim.py:223` `last_segment_end_sec`, `pipeline/shutdown.py:78-91`). (Epic 6 delivery; satisfies the EC.) |
| EC2 — NFS hiccup → backoff retry → requeue after 3 | **partial** | Crash-recovery/retry exists (`stt/crash_recovery.py`) but the specific `read_with_retry` exponential-backoff IO helper (plan §10) not located as a generic path. |
| EC3 — worker version/backend/model_hash mismatch → retry pile | **missing** | No `coordinator/version_check.py`; claim SQL (`jobs_claim.py:63-81`) has **no `backend`/`model_hash` columns or check** (grep `model_hash`/`IncompatibleJob` in pipeline/src: none). Plan §8 unimplemented. |
| TC4 — second pipeline worker refuses ChromaDB write with log line | **missing** | No `chroma_writer_lock.py`, no `pg_try_advisory_lock('chroma:writer')` (grep `chroma.*writer`/`single.writer` in pipeline/src: none). The single-writer guard the story's preamble mandates is absent. |

## Story 19.5 — Database scaling & failover

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — schema sustains 1M segments + 50k videos in perf budget | **partial** | Schema exists; query budgets defined in `perf_budgets.yaml` — but no load proving it at 1M/50k scale (depends on Story 18.7 + the missing 19.1 fixture). |
| AC2 — streaming replica add (`pg_basebackup` + recovery); read-only replica serves search | **missing** | No `ops/postgres/setup-replica.sh`, no `primary.conf`/`replica.conf` (no `ops/` dir at all). No `api/internal/dbroute/` router or `lag_monitor.go` (grep `replica`/`pg_last_xact_replay` in api/: only `scale.go` stub comments). Search has no replica routing. |
| AC3 — daily `pg_dump`, 14-day retention, CI-tested one-line restore | **missing** | `pipeline/src/maktaba_pipeline/integrity/backup.py:12` is a **manifest format only** — "The actual rsync / pg_dump … out of scope" of that module. No `ops/backup/pg_dump.sh`/`restore.sh`/`retention.sh`, no systemd/launchd units, no CI restore drill. |
| AC4 — `goose up` on 30 TB fixture ≤ 60 s; long migrations blocked by pre-merge lint | **missing** | No migration-safety lint: no `shared/db/migrations/lint_long_running.go`, no `migrate:lint` make target (grep: only prose guidance in `migrations/README.md:88` and `MANIFEST.md:82`). Convention is documented, not enforced. |
| EC1/EC2/EC3 (dump throttle / lag alert / SQLite VACUUM INTO) | **missing** | Lag monitor + alert metric absent (no `dbroute`); SQLite `VACUUM INTO` backup path not implemented. |

## Story 19.6 — Storage scaling & large library handling

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — cold scan 30 TB / 50k files ≤ 30 min, RSS ≤ 800 MiB | **partial** | Bounded-memory iterative walker exists (`scanner/walker.py:1-19`), concurrency-capped scanner orchestrator exists (Epic 1). But the budget is **unasserted** — no `tests/scan/test_scanner_perf.py` perf harness wired (depends on missing 19.1 fixture). Mechanism present; assertion missing. |
| AC2 — `content_hash` = BLAKE3(first+last 4 MiB + size); zero-byte/8-MiB/sparse correct | **complete** | `pipeline/src/maktaba_pipeline/identity/hasher.py:107-184` implements exactly `BLAKE3(head ‖ tail ‖ size_le_u64)`, `HEAD_TAIL_BYTES=4 MiB`, deterministic zero-byte path (size suffix only), uniform across the 8 MiB boundary (double-write of head), size suffix disambiguates same-edge different-size. Matches AC2 and TC3. (Epic 1 delivery; fully satisfies.) |
| AC3 — rename/move 10% triggers no re-process | **complete** | `videos.content_hash` is identity; `library_mgmt/dedup.py` + scanner upsert `ON CONFLICT (content_hash) DO UPDATE SET path` (Epic 1/9). Rename = path UPDATE, no job enqueue. |
| AC4 — watcher debounces FS events at 2 s; atomic mv → one job | **complete** | `watcher/debouncer.py:1-30` per-path timer + stable-size probe + mtime quarantine; collapses copy-then-rename into one tick (Story 1.3). |
| EC1 — skip files with mtime < 30 s | **partial** | Mtime-quarantine logic in debouncer (`debouncer.py:14-18`); scanner-side `skip_younger_than_s` config in plan-19-06 §9 — present in watcher, scanner-side cutoff unverified here. |
| EC2 — 60 s per-file hash timeout → requeue | **missing** | No `asyncio.wait_for(content_hash, timeout=60)` requeue path located in scanner service. |
| EC3 — file deleted mid-scan → graceful skip | **complete** | Walker swallows `OSError`/`FileNotFoundError` per scandir entry (`walker.py:6-11` documents permission/loop handling; entry-level try/except). |

## Story 19.7 — Concurrency caps & quotas

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — `transcode.max_concurrent` default `max(1, cores/4)`, canonical; over-cap → direct-play cap or queue | **complete** | `streaming/internal/slots/slots.go:50-59` `Effective()` returns `runtime.NumCPU()/4` floor 1, override via `MaxTranscode`; `Decide()` (108-126) returns `DecisionDirectCap` (720p) or `DecisionQueue`/`DecisionExhausted`. Matches AC1/TC1/TC4. (Story 8.10 delivery; satisfies 19.7 AC1.) |
| AC2 — `concurrency.transcribe` defaults 1/GPU via advisory lock; CPU stages host-wide semaphore | **partial** | `pipeline/concurrency.py:48-58` `DEFAULT_CONCURRENCY[TRANSCRIBE]=1` + per-device `asyncio.Lock` + per-stage semaphore. Correct **per host**; the spec says "enforced by an advisory lock" implying cross-host (see 19.4 AC3) — that is in-process only. CPU semaphore present. |
| AC3 — per-library `max_usd_per_month` enforced at job-claim; over-budget → pending, `not_before = next month` | **complete** | `pipeline/src/maktaba_pipeline/stt/openai_api.py:211-230` `should_refuse_claim` is a pre-claim projection that refuses with `not_before = first of next month` when monthly cap exceeded; local backend bypassed (EC3). Config validated (`library_mgmt/config.py:86,247`). Note: no dedicated `library_budgets` table (cap is per-library STT config), so the *materialized used_usd ledger* of plan §5 is absent — but the AC behavior (enforced at claim, bumped to next month) holds. |
| AC4 — all caps visible in `/api/system/health` and exported as metrics | **partial** | `streaming` reports `TranscodeUsed`/`TranscodeCapacity` via gRPC `GetCapabilities` (`grpcsrv/server.go:283-284`); `api/internal/system/` exists. Whether `/api/system/health` aggregates *all* caps (transcribe, budgets) + Prometheus metrics (plan §10) is not confirmed; budget gauges (`library_budget_used_usd`) not located. |
| EC1 — cap reduced at runtime; running jobs finish, new claims respect new cap | **partial** | Slot allocator supports a live cap, but no `ReloadFromConfig`/30 s `system_config` poller (plan §4) located. |
| EC2 — hot-reload budget mid-month | **partial** | Budget read from per-library config at claim time (effective immediately if config reloaded); no `PATCH /admin/.../budget` endpoint or DB-row source-of-truth (plan §6). |
| EC3 — local STT bypasses budget | **complete** | `should_refuse_claim` returns no-cap for `monthly_cap_usd is None`; local backend explicitly bypassed (`openai_api.py:227-230`). |

## Story 19.8 — Multi-tenant readiness

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — every user-scoped row has `user_id`; single-user sentinel `…0001` | **partial** | Sentinel constant exists (`api/internal/auth/users/users.go:31-35` `SentinelAdminID = "00000000-0000-0000-0000-000000000001"`, seeded by migration 0029). But there is **no schema-audit asserting every user-bearing table (`watch_state`, `collections_by_user`, `favorites`, …) has `user_id NOT NULL`** (plan §7 TC1 `schema_audit_test.go` absent), and no `00xx_multi_tenant_readiness.sql` backfill migration. Sentinel present; schema-completeness unproven. |
| AC2 — auth treats single-user as all-authorized-as-sentinel; admin-token maps to **same** sentinel UUID | **complete** | `api/internal/auth/middleware/middleware.go:74-76` admin-token path sets `UserID = users.SentinelAdminID`, `AccessAllLibraries=true`; single-user mode gate `SingleUserMode` in `authz/authz.go:78-106`. Admin-token and single-user resolve to the identical sentinel UUID — AC2 / TC4 linkage satisfied. |
| AC3 — `library_acl(library_id, user_id, role admin\|editor\|viewer)`, FK, partial unique index; this story owns the migration | **partial** | `shared/db/migrations/0030_library_acl.sql:21-31` exists **but with a different schema and a different owner**: it is **Story 10.13's** table, has **no `role` column** (header line 8: "v1 only models a single read role; v2 may add `role`"), no `admin\|editor\|viewer` CHECK, PK is `(user_id, library_id)` (spec wants `(library_id, user_id)`), has `granted_at` not `created_at`. The "role-bearing ACL owned by 19.8" does not exist. |
| AC4 — single→multi-user migration documented + integration test flips flag, asserts continuity incl. implicit-ACL backfill | **missing** | No `tests/migrations/multi_user_flip_test.go`, no `BackfillSentinelACL`/`EnableMultiUser` (grep `EnableMultiUser`/`BackfillSentinel` in api/: none), no `docs/runbooks/single-to-multi-user.md` (no `docs/runbooks/` entries). `FeatureMultiUser` in `subscriptions.go:45` is a licensing feature flag, not the auth gate flip. |
| EC1/EC2/EC3 (import backfill / JWT subject mismatch / sentinel collision check constraint) | **missing** | No `users_no_real_sentinel` CHECK constraint, no import backfill migration, JWT-subject-mismatch write-rejection on `watch_state` not asserted. |

---

## Top gaps by impact

1. **Cross-replica WS event bus is entirely absent (Story 19.2 AC2/AC3/AC4).** The WS Hub (`api/internal/handlers/ws/ws.go`) is in-memory single-process and `api/internal/scale/scale.go` openly ships only an `InMemoryBus` stub. There is **no `events` table, no `pg_notify`/`LISTEN` loop, no `last_event_id` replay, no pruner, no drain handler.** A second API replica cannot deliver job/library events to clients on another replica, and reconnecting clients cannot recover missed events. This is the load-bearing claim of the whole "scales horizontally without architectural rewrites" epic goal, and it is not started — the spec demanded a real Postgres adapter + durable `events` surface, not merely the interface. **Worst gap.**

2. **No database scaling/failover/backup deliverables (Story 19.5, all ACs).** No `ops/` tree at all: no replica setup, no `pg_dump`/restore scripts, no systemd/launchd units, no read-replica routing (`api/internal/dbroute/` absent), no lag monitor, no migration-safety lint (`migrate:lint`). `integrity/backup.py` is a manifest format that explicitly disclaims doing the actual dump. Operationally there is no documented or tested recovery path — a data-durability risk.

3. **No capacity floor harness (Story 19.1 AC1/AC3/AC4).** No reference profile in `perf_budgets.yaml`, no `tests/capacity/`, no `make capacity`, no ulimit/FD scripts. The epic's premise — "bottlenecks detected by load test, not production incident" — has no enforcing artifact; capacity is asserted nowhere.

4. **Streaming multi-host scale-out unimplemented (Story 19.3 AC1–AC4).** No `streaming_replicas` table/registry, no `replica_id` on sessions, no `session_invalidated` failover, and `EvictHashCache` is local-only with no peer gRPC fan-out — a stale-cache correctness bug the moment a second streaming replica exists.

5. **Pipeline multi-host correctness guards missing (Story 19.4 AC3/EC3/TC4).** GPU serialization is in-process `asyncio.Lock` only (no cross-host `pg_advisory_xact_lock`), there is no ChromaDB single-writer guard (the story's stated invariant), and no worker version/`model_hash` compatibility check. Two hosts sharing a GPU or one ChromaDB would corrupt or oversubscribe. (The job-queue exactly-once claim, heartbeat, reaper, and resume-offset — Story 19.4 AC2/EC1 — *are* complete, inherited from Epic 6.)

6. **Story 19.8 ACL ownership gap (AC3/AC4).** The `library_acl` table that exists is Story 10.13's roleless read-grant table, not the `(library_id, user_id, role)` table 19.8 says it owns; no flag-flip migration, backfill, integration test, or runbook. Multi-user "readiness" is partially true (sentinel + single-user auth work) but the migration-safety net 19.8 promises is unbuilt.

**Net:** Single-host functionality that scalability *reuses* is solid (content_hash, slot cap, job-queue, single-user auth, STT budget). Everything Epic 19 itself is supposed to add for multi-host — the adapters the spec explicitly requires, not just interfaces — is missing or stubbed.
