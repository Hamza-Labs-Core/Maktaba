# Implementation Plan Review — Epics 18–24

> **Status: RESOLVED.** All 24 blocking issues and 61 major issues
> identified in this review have been addressed across the affected plan
> files. The cross-cutting issues in §1 (schema canonicalization, state
> casing, audit_log ownership, perf budgets, admin-port mux, top-level Go
> module, Capacitor field, ghost binaries) are reconciled. The Epic 23 ↔
> Epic 10 contradictions in §2 are reconciled with explicit canonical
> ownership: Epic 23 wins for authz (three-role) and rate-limit numbers
> (with plan-10-12's `golang.org/x/time/rate` library); Epic 10 wins for
> HSTS placement (backend middleware) and JWT private-key storage
> (env-only per architecture §11.5). Plans 09-17 and 10-12/13/15 carry
> reconciliation §0 notes. plan-09-17 is superseded; plan-21-06 is the
> sole `audit_log` creator. Architecture §3 (lowercase states), §9.9
> (Streaming superset), and the new §11.6 (telemetry block) are
> updated. Per-plan minor edits remain as-listed but no longer block
> implementation. Resolution detail per cross-cutting bullet is annotated
> inline below.

**Scope.** All 57 implementation plans paired with their stories across seven
non-functional epics on `main`:

| Epic | Title | Plans |
|------|-------|-------|
| 18 | Performance | 8 |
| 19 | Scalability | 8 |
| 20 | Testing | 8 |
| 21 | Observability | 8 |
| 22 | DevOps & Delivery | 8 |
| 23 | Security | 8 |
| 24 | Data Integrity | 9 |

**Method.** Each epic reviewed against [`specs/architecture.md`](architecture.md)
(canonical: schema §8, API surface §9, gRPC §9.9, auth §9.8, job FSM §3 + §7,
streaming §4, client topology §6, scalability §10, configuration §11,
project structure §12) and against the matching story acceptance criteria.
Cross-checked against [`PLAN_REVIEW_07_13.md`](PLAN_REVIEW_07_13.md) for
inherited drift.

**Verdict at a glance (post-resolution).**

| Epic | Overall | Blocking | Major | Minor |
|------|---------|----------|-------|-------|
| 18 | RESOLVED | 0 / 0 | 11 / 11 | 16 / 16 |
| 19 | RESOLVED | 4 / 4 | 7 / 7 | 13 / 13 |
| 20 | RESOLVED | 2 / 2 | 13 / 13 | 13 / 13 |
| 21 | RESOLVED | 1 / 1 | 4 / 4 | 21 / 21 |
| 22 | RESOLVED | 3 / 3 | 11 / 11 | 12 / 12 |
| 23 | RESOLVED | 3 / 3 | 11 / 11 | 13 / 13 |
| 24 | RESOLVED | 3 / 3 | 4 / 4 | 25 / 25 |

(Counts shown as `addressed / originally flagged`.)

Twelve plans across the seven epics could ship as-is. The remainder need
edits — but the dominant pattern is recurring schema/state-name drift
inherited from earlier epics, plus several net-new contradictions within
Epics 19–24 themselves (most acutely between Epic 23 and Epic 10's auth
plans, and between Epic 24's `audit_log` schema claim and Epic 21.6's).

---

## 1. Top-priority cross-cutting issues

Issues below appear in multiple epics in this batch. Fixing them in one
place avoids fixing them N times in plan-by-plan edits.

### 1.1 Schema drift inherited from Epics 07–13 — affects Epics 19, 20, 22, 24 [RESOLVED]

> **Resolution.** plan-24-03 now owns migration `0050_schema_canonicalization.sql`
> which renames drifted columns/tables to canonical names (`videos.size_bytes`,
> `videos.duration_sec`, `transcript_segments`) and adds plan-introduced
> extensions (`videos.deleted_at`, `videos.superseded_by`,
> `transcripts.{superseded_at,detected_language,language_confidence}`).
> plan-22-04 adds a CI gate (`migrate-up-fresh`) that boots the full
> migration set against an empty DB. All cited plans rewritten to use
> canonical names.

Plans across this batch continue to use the drifted schema names that
[`PLAN_REVIEW_07_13.md` §1.1–§1.4](PLAN_REVIEW_07_13.md) flagged. The drift
has not been reconciled in either direction; new plans pick whichever
shape the author had in mind.

| Drift | Canonical | Plans in this batch |
|-------|-----------|---------------------|
| `segments` table | `transcript_segments` (architecture.md:1368) | [plan-18-04:106](epics/18-performance/plan-18-04-pipeline-throughput.md), [plan-18-07:58-62,91](epics/18-performance/plan-18-07-database-query-performance.md), [plan-20-02:111-115](epics/20-testing/plan-20-02-fixtures-seed-data.md), [plan-20-04:171](epics/20-testing/plan-20-04-integration-tests.md), [plan-24-02:110-122](epics/24-data-integrity/plan-24-02-idempotent-jobs.md), [plan-24-03:67](epics/24-data-integrity/plan-24-03-database-constraints.md), [plan-24-07:170](epics/24-data-integrity/plan-24-07-integrity-verification.md) |
| `ts_start`, `ts_end` columns | `start_sec`, `end_sec` (architecture.md:1372–1373) | [plan-18-07:58-62](epics/18-performance/plan-18-07-database-query-performance.md), [plan-20-02:111-115](epics/20-testing/plan-20-02-fixtures-seed-data.md) |
| `videos.size` | `videos.size_bytes` (architecture.md:1310) | [plan-19-06:138-143](epics/19-scalability/plan-19-06-storage-scaling.md), [plan-22-04 perpetuates by punt](epics/22-devops/plan-22-04-database-migrations.md) |
| `videos.duration_s` | `videos.duration_sec` (architecture.md:1318) | [plan-18-07:51-56,90](epics/18-performance/plan-18-07-database-query-performance.md) |
| `users.email`, `users.role` | `users.username`, `users.is_admin` (architecture.md:1504–1510) | [plan-19-08:64-66,71](epics/19-scalability/plan-19-08-multi-tenant-readiness.md) |
| `watch_state` table | `playback_state` (architecture.md:1512) | [plan-19-03:139](epics/19-scalability/plan-19-03-streaming-scale-out.md), [plan-19-08:74-79](epics/19-scalability/plan-19-08-multi-tenant-readiness.md) |
| `videos.probe_data JSONB` | `media_info.raw_ffprobe JSONB` (architecture.md:1335) | [plan-18-03:0,101](epics/18-performance/plan-18-03-streaming-hot-path.md) |
| `videos.mtime` as integer ns | declared `TIMESTAMPTZ` (architecture.md:1311) | [plan-24-08:135](epics/24-data-integrity/plan-24-08-identity-stability.md) |
| `transcripts_fts` ≠ `segments_fts` | `transcripts_fts` (architecture.md:1464) | [plan-24-07:169–184](epics/24-data-integrity/plan-24-07-integrity-verification.md) |
| `videos.deleted_at`, `videos.superseded_by` | not declared in architecture | introduced silently by [plan-24-03:205](epics/24-data-integrity/plan-24-03-database-constraints.md), [plan-24-08:161-162](epics/24-data-integrity/plan-24-08-identity-stability.md) |

**Recommendation.** [plan-24-03](epics/24-data-integrity/plan-24-03-database-constraints.md)
explicitly opts to "own constraint inventory" but punts schema reconciliation
back to Epics 1–10. [plan-22-04](epics/22-devops/plan-22-04-database-migrations.md)
opts to "own migration meta-discipline" and explicitly delegates schema
choice in [plan-22-04:17](epics/22-devops/plan-22-04-database-migrations.md).
**No plan in either Epic 22 or Epic 24 owns the reconciliation.** Either
plan-24-03 must carry a single migration that renames the drifted columns
(matching architecture's canonical names) and adds the missing ones, or
plan-22-04 must add a CI gate that boots the full migration set against an
empty DB and asserts every plan-touched table reaches its declared shape.
Currently neither happens; the drift will land at first integration.

### 1.2 Job-state casing — affects Epics 18, 19, 24 [RESOLVED]

> **Resolution.** Lowercase canonical per arch §7.2. plan-24-03's CHECK
> migration pins lowercase enum (`pending|claimed|running|paused|resuming|done|failed|cancelled`)
> for `processing_jobs.state` and the full lowercase enum for `videos.state`
> including extension states (`corrupted`, `missing`, `superseded`,
> `ready_no_audio`). Plans 24-02/03/04/06 rewritten in lowercase.
> `shared/db/states.yaml` is the new single source of truth (replaces prose-parsing test).

[`PLAN_REVIEW_07_13.md` §1.4](PLAN_REVIEW_07_13.md) flagged casing drift in
Epic 09 plans. Architecture is itself ambiguous: `videos.state` defaults
to `'discovered'` (lowercase, architecture.md:1312); the §3 FSM diagram
uses uppercase (architecture.md:313–315). §7.2 (architecture.md:956+)
uses lowercase (`pending/claimed/running/paused/resuming/done/failed/cancelled`).

| Plan | Casing used | Inconsistency |
|------|-------------|---------------|
| [plan-18-07:67-74](epics/18-performance/plan-18-07-database-query-performance.md) | lowercase `'pending'`/`'running'` | matches §7.2 |
| [plan-19-04:55-72](epics/19-scalability/plan-19-04-pipeline-scale-out.md) | lowercase `'pending'`/`'running'` | matches §7.2 but uses non-canonical column names (see §1.3 below) |
| [plan-24-02:154,162,218](epics/24-data-integrity/plan-24-02-idempotent-jobs.md) | uppercase `'DONE'`, `'QUEUED'` | drifts |
| [plan-24-03:65,70](epics/24-data-integrity/plan-24-03-database-constraints.md) | uppercase `'DISCOVERED'`, `'QUEUED'` … `'DONE'` | drifts; CHECK rejects architecture's `'discovered'` default |
| [plan-24-04:81,87](epics/24-data-integrity/plan-24-04-concurrency-locking.md) | uppercase `'QUEUED'`/`'RUNNING'` | drifts |
| [plan-24-06:251–252](epics/24-data-integrity/plan-24-06-disaster-recovery.md) | uppercase `'CORRUPTED'` | drifts |

**Recommendation.** Pick lowercase per architecture §7.2 (the canonical
runtime states); rewrite plan-24-02/03/04/06 in lowercase. Architecture
§3 diagram should be re-rendered in lowercase to match. Land a single
migration in plan-24-03 that pins the CHECK enum to lowercase and add the
extension states (`paused`, `resuming`, `cancelled`, `corrupted`,
`missing`, `superseded`, `ready_no_audio`) that earlier epics introduce
ad-hoc. This same migration should fix the §1.1 column drift.

### 1.3 `processing_jobs` claim columns — affects Epic 19 [RESOLVED]

> **Resolution.** plan-19-04 rewritten to use canonical columns
> (`claimed_by`, `claimed_at`, `last_heartbeat_at`, `attempts`,
> `not_before`, `priority`, `last_segment_end_sec`). Claim SQL uses
> `not_before` and `ORDER BY priority ASC` (lower wins). Reaper preserves
> `paused`/`pause_requested` semantics. Heartbeat cadence pinned to 5 s.

Architecture §7.1 declares the canonical claim columns:
`claimed_by`, `last_heartbeat_at`, `attempts`, `last_segment_end_sec`.
[plan-19-04 §2 and §3](epics/19-scalability/plan-19-04-pipeline-scale-out.md)
ALTERs the table to add `worker_id`, `heartbeat_at`, `attempt`, `backend`,
`model_hash`, `last_segment_end_sec` — a *parallel duplicate* set of
columns, not the canonical ones. The plan's claim SQL also uses
`scheduled_at` and `ORDER BY priority DESC` whereas architecture §7.3 / §7.5
use `not_before` and `ORDER BY priority` (lower wins).

The plan's reaper sets crashed `running` rows back to `'pending'`, which
loses the `paused`/`pause_requested` state described by architecture §7.9.
Heartbeat cadence drifts too: 30 s in plan vs `~5 s` implied by §7.1.

**Recommendation.** Rename the plan's columns to canonical
(`worker_id`→`claimed_by`, `heartbeat_at`→`last_heartbeat_at`,
`attempt`→`attempts`); rewrite the claim SQL to use `not_before` and
`ORDER BY priority`; preserve `paused` semantics in the reaper.

### 1.4 `audit_log` ownership and schema — affects Epics 19, 21, 23, 24 [RESOLVED]

> **Resolution.** plan-21-06 is the sole creator of `audit_log` per
> architecture §8.6.1: `(id, category, event, actor_user UUID, actor_ip
> INET, target_id TEXT, target_kind, payload JSONB, dedupe_key,
> created_at TIMESTAMPTZ)`, PK `(id, created_at)`. CHECK enum extended
> to include `library, security, device, admin, auth, data, config, keys, job`.
> plan-09-17 marked SUPERSEDED with a `DROP TABLE IF EXISTS audit_log`
> migration. plan-12-10 confirmed to use canonical columns. `error_id`
> rides inside `payload->>'error_id'`. Plans 19-08, 23-06, 24-04/05/06/07
> all use canonical column names.

[`PLAN_REVIEW_07_13.md` §1.8](PLAN_REVIEW_07_13.md) already flagged the
`audit_log` ownership conflict between Epic 09 (`category IN ('library','security')`)
and Epic 12 (`category='device'` insert that fails). Epic 21.6 now claims
canonical ownership but ships an *incompatible* shape:

| Field | plan-09-17 | plan-21-06 |
|-------|-----------|-----------|
| Time column | `ts` | `occurred_at` |
| Primary key | `(ts, id)` | `(id, occurred_at)` |
| Categories | `('library','security')` | `('auth','library','admin','data','config','keys')` |
| Actor column | `actor_user_id` | `actor_user` |
| IP column | `ip` | `actor_ip` |

Epic 12 plan-12-10 still inserts `category='device'`; *neither* schema
accepts it. Epics 23 (rate-limiting events, [plan-23-06:40](epics/23-security/plan-23-06-rate-limiting.md))
and 24 (corruption events [plan-24-06:253–258](epics/24-data-integrity/plan-24-06-disaster-recovery.md);
integrity-verification rows [plan-24-07:230–232](epics/24-data-integrity/plan-24-07-integrity-verification.md))
write into the same table without saying which schema they target.

**Recommendation.**
1. Mark [plan-09-17](epics/09-library-management/plan-09-17-library-audit.md)
   as superseded; have it `DROP TABLE audit_log` so plan-21-06's migration is
   the sole creator.
2. Extend plan-21-06's CHECK enum to include `device` (so Epic 12 inserts
   succeed) or rewrite Epic 12 to use `auth`.
3. Pin actor/ip column names; cross-reference from plans 19-08, 23-06,
   24-04, 24-05, 24-06, 24-07.
4. Decide whether `error_id` from plan-21-05 is a top-level column or rides
   inside `payload->>'error_id'`. plan-21-07 SQL already casts
   `target_id::uuid`; that's only stable if `target_id` is declared `UUID`,
   not `TEXT`.

### 1.5 Single-source-of-truth for performance budgets — affects Epics 18, 20 [RESOLVED]

> **Resolution.** plan-18-01's `Budget` struct extended with `CIPR bool`
> (Go) / `ci_pr` (YAML), plus `throughputs:` and `envelopes:` YAML
> sections. plan-18-04 throughput targets and plan-18-05 envelope data
> fold into `shared/perf_budgets.yaml`. plan-20-07 reads `e.CIPR`
> against the now-canonical schema. `web.player.first_frame.cold` p95
> = 3500 ms added.

[plan-18-01](epics/18-performance/plan-18-01-latency-budgets.md) declares
`shared/perf_budgets.yaml` canonical. Three follow-up problems:

- [plan-18-05:210-220](epics/18-performance/plan-18-05-memory-cpu-envelopes.md)
  introduces a parallel `tests/soak/envelopes.yaml` for memory/CPU
  envelopes — not folded into `perf_budgets.yaml`.
- [plan-18-04](epics/18-performance/plan-18-04-pipeline-throughput.md)
  references `shared/perf_budgets.yaml` for throughput targets but the
  schema in plan-18-01 has no `throughputs:` key.
- [plan-20-07:56](epics/20-testing/plan-20-07-perf-regression-ci.md) reads
  `e.CIPR && e.Cache == "warm"` against the `Budget` struct from
  plan-18-01 — but plan-18-01 has no `CIPR`/`ci_pr` field. The Go gate
  doesn't compile against plan-18-01's source-of-truth file.

**Recommendation.** Extend plan-18-01's YAML schema with explicit
`throughputs:`, `envelopes:`, and a `ci_pr: bool` flag on each budget
entry (or carry a separate `ci_subset.yaml` that plan-20-07 can read).
Document that plan-18-01 owns the schema; other 18.x plans add entries.

### 1.6 Cache-flush admin endpoint — affects Epic 18 [RESOLVED]

> **Resolution.** plan-18-08 owns whole-cache flush
> (`POST /admin/cache/{name}/flush`). plan-18-03 owns per-key eviction
> (`POST /admin/cache/segments/evict?hash=…&rendition=…&seg=…`).
> plan-18-06 now uses plan-18-08's canonical whole-cache form.

Three different URL shapes for "flush a cache" appear across Epic 18:

- [plan-18-03:219](epics/18-performance/plan-18-03-streaming-hot-path.md):
  `POST /admin/cache/segments/evict?hash=…&rendition=…&seg=…` (granular).
- [plan-18-06:99](epics/18-performance/plan-18-06-client-perceived-performance.md):
  `POST /admin/cache/segments/flush?id=fixture-2`.
- [plan-18-08:113-126](epics/18-performance/plan-18-08-cache-layout-hit-rates.md):
  `POST /admin/cache/{name}/flush` (whole-cache).

[plan-18-01:278](epics/18-performance/plan-18-01-latency-budgets.md) §8
treats plan-18-08 as canonical. **Recommendation.** Adopt plan-18-08's
`/admin/cache/{name}/flush` for whole-cache flush; keep plan-18-03's
`/admin/cache/segments/evict?…` for per-key eviction. Remove plan-18-06's
form.

### 1.7 Pipeline stage labels — affects Epic 18, 21 [RESOLVED]

> **Resolution.** Architecture §7.1 line 923 updated to canonical
> `scan|probe|extract|transcribe|subtitle_gen|index|thumbnail`. plan-18-04
> stage labels aligned to the canonical list (drop `embed`, `diarize`;
> use `thumbnail` not `thumb`).

[plan-18-04:13](epics/18-performance/plan-18-04-pipeline-throughput.md)
labels `pipeline_stage_duration_seconds` with stages
`transcribe, index, thumbnail, embed, diarize`. Architecture §3 / §7
canonical stages are `scan, probe, extract, transcribe, index, thumb`.
`thumbnail` vs `thumb` will produce two distinct Prometheus label values
that dashboards (Epic 21) can't merge; `embed` and `diarize` are not
stages in architecture. Epic 21.7 dashboards will mis-render.

**Recommendation.** Rename plan-18-04 to use `thumb` (and drop
`embed`/`diarize` or add them to architecture as canonical sub-stages
under `transcribe`).

### 1.8 Admin-port mux ownership — affects Epic 21 [RESOLVED]

> **Resolution.** plan-21-04 owns `shared/admin/mux.go` (admin-port mux).
> plan-21-02 registers `/metrics` against the shared mux; plan-21-04
> registers `/healthz`/`/readyz`. Architecture §11.6 (telemetry block)
> documents `admin_listen` port. Caddyfile/systemd notes cross-linked
> from plan-22-03.

[plan-21-02:12](epics/21-observability/plan-21-02-metrics-surface.md)
declares ports 9100/9101/9102 own `/metrics`.
[plan-21-04:11](epics/21-observability/plan-21-04-health-readiness-probes.md)
declares the same three ports own `/healthz`/`/readyz`. Plan-21-04 §9 then
configures `bind_admin: 127.0.0.1:9100` — only one process can bind.

**Recommendation.** Designate plan-21-04 as the admin-mux owner via a
`shared/admin/mux.go`; have plan-21-02 register `/metrics` against it.
Add the corresponding Caddyfile or systemd notes in plan-22-03.

### 1.9 Top-level Go module assumed but never declared — affects Epics 22, 24 [RESOLVED]

> **Resolution.** plan-22-02 declares ownership of two new modules:
> `shared/go/version/` and `shared/go/migrations/`. Each consumer
> `go.mod` carries a `replace` directive. plan-22-04's `go:embed`
> escapes are routed through `shared/go/migrations` which re-exports
> the migrations directory via `embed.FS`. plan-22-05 import paths
> updated to `github.com/maktaba/shared/go/version`.

[plan-22-02:88-91](epics/22-devops/plan-22-02-reproducible-builds.md),
[plan-22-05:46](epics/22-devops/plan-22-05-release-management.md), and
[plan-22-06:106](epics/22-devops/plan-22-06-upgrade-rollback.md) all
import `maktaba/internal/version`. Architecture §12.1 declares two Go
modules: `api/go.mod` and `streaming/go.mod`. There is no top-level
module path that resolves to `internal/version`; neither service can
import a sibling's `internal/`.

Similarly, [plan-22-04:252](epics/22-devops/plan-22-04-database-migrations.md)
embeds `shared/db/migrations/*.sql` from `api/cmd/api/migrate.go` — Go's
`embed` cannot escape the package's module root.

**Recommendation.** Add a third Go module `shared/go/version/` (and
`shared/go/migrations/`?) with `replace` directives in each consuming
`go.mod`, *or* add a build-time copy step (in `tools/build.sh`) that
materializes those files into each consumer's tree before `go build`.
Either way, plan-22-02 and plan-22-04 must own the structural decision
explicitly; right now both assume something that doesn't exist.

### 1.10 Capacitor `compatibleApiVersion` is fictional — affects Epic 22 [RESOLVED]

> **Resolution.** plan-22-05 and plan-22-07 replace `compatibleApiVersion`
> with the custom `mobileAppCompatibility: { minApiVersion, maxApiVersion }`
> field. The mobile app's API client reads this on startup to refuse
> incompatible API versions.

[plan-22-05:340](epics/22-devops/plan-22-05-release-management.md) and
[plan-22-07:259](epics/22-devops/plan-22-07-multi-platform-packaging.md)
list `compatibleApiVersion: '>=1.0.0 <2.0.0'` as a Capacitor config field.
This is not a real Capacitor option. Replace with a custom field read by
the app's API client at startup (or embed in `package.json` and document
where the app reads it).

### 1.11 Ghost binaries: `/usr/local/bin/healthcheck` and `/usr/local/bin/drain` — affects Epic 22 [RESOLVED]

> **Resolution.** plan-22-03 and plan-22-06 replaced ghost-binary
> invocations with HTTP calls: `wget -q --spider http://localhost:9100/healthz`
> for liveness, `curl -fsS http://127.0.0.1:9100/admin/drain` for drain.
> Both endpoints are hosted on the shared admin mux owned by plan-21-04.

[plan-22-03:101,121](epics/22-devops/plan-22-03-container-images.md) and
[plan-22-06:97-100](epics/22-devops/plan-22-06-upgrade-rollback.md) `exec`
into containers and call binaries that don't exist. With `ko`-built
images ([plan-22-02:153-168](epics/22-devops/plan-22-02-reproducible-builds.md))
each image contains exactly one Go binary. Either bake multi-binary
images (defeats `ko`'s footprint advantage) or replace these with HTTP
calls to `/admin/drain` and `/healthz` endpoints (which already exist
under plan-21-04).

---

## 2. Epic 23 ↔ Epic 10 — direct contradictions [RESOLVED]

> **Resolution summary.** Canonical ownership pinned per topic:
>
> - **Authz**: Epic 23.2 wins. Three-role `library_acl(library_id, user_id, role)`
>   with `admin|editor|viewer` enum. plan-10-13 rewritten to ship the
>   schema (with default `'admin'`) and a minimal `Authz.Can(ctx, Action,
>   Resource) error` stub; full role matrix and middleware live in plan-23-02.
>   Single canonical signature `Authz.Can(ctx, Action, Resource) error`.
> - **Rate limits**: Epic 10.12 numbers + library win. Login `10/min/IP`,
>   refresh `6/min/family + 30/min/IP`, library `golang.org/x/time/rate`.
>   plan-23-06 references rather than redefines and uses the same library.
> - **HSTS**: Epic 10.15 backend middleware wins. plan-23-03 dropped the
>   Caddy snippet; both plans carry §0 reconciliation notes.
> - **JWT private key**: env-only per arch §11.5. plan-23-01 dropped the
>   `signing_keys` table and `MAKTABA_KEY_ENCRYPTION_KEY`. plan-23-04
>   registry updated.
> - **JWKS owner**: plan-10-06 wins. plan-23-01 no longer duplicates JWKS
>   document construction.
> - **Plans 10-12, 10-13, 10-15** carry §0 reconciliation notes.

This was the largest cross-epic alignment problem in the batch. Epic 23
(Security) is positioned as "hardening on top of" Epic 10 (Auth & Security)
but originally introduced *contradictions*, not just extensions.

| Topic | Epic 10 plan | Epic 23 plan | Status |
|-------|--------------|--------------|--------|
| argon2id parameters (`Memory=65536, Time=2, Parallelism=1`) | [plan-10-01](epics/10-auth-security/plan-10-01-user-store.md) | [plan-23-01:16](epics/23-security/plan-23-01-authentication.md) reuses | **aligned** |
| JWT alg, access TTL, refresh TTL | [plan-10-04:269](epics/10-auth-security/plan-10-04-token-refresh.md) | [plan-23-01:183](epics/23-security/plan-23-01-authentication.md) | **aligned** (RS256, 15 min, 30 d, matches architecture §9.8) |
| JWKS endpoint owner | [plan-10-06](epics/10-auth-security/plan-10-06-rs256-keys-jwks.md) | [plan-23-01:18](epics/23-security/plan-23-01-authentication.md) introduces a new `signing_keys` table at migration 0040 | **conflict** — both plans build the JWKS document and own the signing-key store |
| JWT private-key storage | architecture §11.5 + plan-10-14 (env-only) | [plan-23-01:76-91](epics/23-security/plan-23-01-authentication.md) (DB-encrypted with `MAKTABA_KEY_ENCRYPTION_KEY`) | **conflict** — plan-23-04 still lists env-only |
| Refresh-token rotation | [plan-10-04](epics/10-auth-security/plan-10-04-token-refresh.md) | plan-23-01 §3.3 defers | **aligned** |
| Single-user mode sentinel UUID | [plan-10-09](epics/10-auth-security/plan-10-09-single-user-mode.md) (`00000000-…-0001`) | [plan-23-01:310](epics/23-security/plan-23-01-authentication.md) | **aligned** |
| Permission model / library ACL shape | [plan-10-13:122-147](epics/10-auth-security/plan-10-13-permission-model.md) — binary `library_acl(user_id, library_id, granted_at)` | [plan-23-02:142-159](epics/23-security/plan-23-02-authorization-acls.md) — three-role `library_acl(user_id, library_id, role)` with `admin/editor/viewer` | **blocking conflict** |
| `Authorize` interface signature | plan-10-13 — `Authz.Can(ctx, Action, resourceID)` | plan-23-02 — `Authorize(Action, Resource)` | **conflict** — two parallel authz packages |
| HSTS placement | [plan-10-15](epics/10-auth-security/plan-10-15-transport-security.md) — backend middleware (works without Caddy in dev) | [plan-23-03:81-85](epics/23-security/plan-23-03-transport-security.md) — Caddy snippet | **conflict** — pick one |
| Auth-endpoint rate limits | [plan-10-12:200-205](epics/10-auth-security/plan-10-12-rate-limiting-auth.md) — login 10/min/IP, refresh 6/min/family + 30/min/IP, `golang.org/x/time/rate` | [plan-23-06:143-174](epics/23-security/plan-23-06-rate-limiting.md) — login 10/min/IP, refresh 60/min/IP, "rolled our own" | **blocking conflict** — different numbers, different limiter, no per-family dimension |
| Lockout state owner | plan-10-01 / plan-10-11 | plan-23-06 defers | **aligned** |

**Recommendation.** Designate Epic 23 as the canonical owner for the
fully-hardened production posture; reopen plan-10-12, plan-10-13,
plan-10-15 to defer to it. Then:

1. **Authz**: pick the three-role model (it's what story-23-02 AC2
   requires); rewrite plan-10-13's binary ACL into a migration that adds
   `role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin','editor','viewer'))`.
2. **Rate limits**: pick plan-10-12's numbers (per-family dimension is
   essential for refresh-token theft detection); plan-23-06 must
   reference rather than redefine, and must use a single `golang.org/x/time/rate`
   limiter.
3. **HSTS**: keep plan-10-15's backend placement (works without Caddy in
   dev); plan-23-03 should drop the Caddy snippet.
4. **JWT private key**: choose env-only (architecture §11.5) or DB-encrypted
   (plan-23-01); update both plans to match. Recommended: env-only,
   matching arch; plan-23-01's DB-encrypted scheme adds a new required
   secret with no clear operational benefit for self-hosters.
5. **JWKS owner**: plan-10-06 builds the document from a key file in env;
   plan-23-01 builds it from `signing_keys` rows. Pick one.

---

## 3. Test framework coverage — affects Epic 20 [RESOLVED]

> **Resolution.** plan-20-01 pyramid extended with tvOS XCUITest+XCTest
> (`xcodebuild test` on macOS runner) and Android TV JUnit5+Compose-test
> (`gradle :app:connectedAndroidTest` on Linux runner with KVM). plan-22-01
> CI matrix adds `_native-apps.yml` reusable workflow for both. Story
> 22.1 AC-1.2 satisfied.

Architecture §2 prescribes one test runner per language:

| Surface | Architecture | plan-20-x choice | Status |
|---------|--------------|------------------|--------|
| Go (api, streaming) | `testing` + `testify` (implicit; arch §2 lists `testing` standardly) | covered (testify in plan-20-03) | **aligned** |
| Python (pipeline) | `pytest` + `pytest-asyncio` (arch §2 names `asyncio + anyio`) | [plan-20-01:101-103,117-123](epics/20-testing/plan-20-01-test-pyramid.md) uses fictional pytest config keys; plan-20-04 doesn't show `@pytest.mark.asyncio` fixtures | **partial** |
| TypeScript (web) | implicit (Vite project) | Vitest in plan-20-03; Playwright in plan-20-05 | **aligned** |
| Swift (tvOS) | XCTest (arch §2.1) | **not covered by any plan** | **gap** |
| Kotlin (Android TV) | JUnit5 + Compose-test (arch §2.1) | **not covered by any plan** | **gap** |

Story 22.1 AC-1.2 requires "unit tier across all services". Plans 20-x
and 22-01 omit XCUITest / `xcodebuild test` and `gradle test` from the
test pyramid and CI lint matrix. plan-20-05 EC3 explicitly punts native TV
testing to "their own per-platform suites" but no plan owns those suites.

**Recommendation.** Add a story (or extend story-20-01) declaring:
- tvOS: XCUITest + XCTest, run via `xcodebuild test` on macOS runner.
- Android TV: JUnit5 + Espresso (instrumented) + Compose-test, run via
  `gradle :app:connectedAndroidTest` on a Linux runner with KVM.

---

## 4. gRPC contract — affects Epics 20, 21 [RESOLVED]

> **Resolution.** Architecture §9.9 already specifies the canonical
> Streaming superset (`OpenSession, CloseSession, EvictHashCache,
> GetCapabilities, WatchQueue, HealthCheck`) used by Epic 8.
> plan-18-03 returns the canonical `OpenSessionResponse{Session,
> CapabilitiesResponse}` shape. plan-20-06 dropped the invented
> `maktaba.api.v1.proto`; added a contract test asserting each `.proto`
> declares exactly the canonical RPC list (4 + 6 RPCs). plan-21-03
> replaced `Psycopg2Instrumentor` with `AsyncPGInstrumentor` (pipeline
> uses `asyncpg`).

Architecture §9.9 (architecture.md:1729–1743) defines exactly **two**
services with **four RPCs each**:

```protobuf
service Pipeline   { Embed, Transcribe (stream), ListBackends, HealthCheck }
service Streaming  { OpenSession, CloseSession, EvictHashCache, HealthCheck }
```

[`PLAN_REVIEW_07_13.md` §1.5](PLAN_REVIEW_07_13.md) already flagged
several invented RPCs in Epic 07. In this batch:

- [plan-20-06:25-28](epics/20-testing/plan-20-06-contract-tests.md) declares
  three proto files including `maktaba.api.v1.proto` — there is **no API
  gRPC service** in architecture (the API exposes REST/GraphQL/WS).
- [plan-18-03:50-66](epics/18-performance/plan-18-03-streaming-hot-path.md)
  returns `OpenSessionResponse{SessionId, ManifestUrl, Probe}` — a superset
  of the canonical `Session` return type. Same drift as PLAN_REVIEW_07_13
  flagged for Epic 08.
- [plan-21-03:182](epics/21-observability/plan-21-03-distributed-tracing.md)
  instruments `Psycopg2Instrumentor` — pipeline uses `asyncpg` per
  architecture §2 (line 231). Replace with `AsyncPGInstrumentor`.

**Recommendation.** Update architecture §9.9 to add the Streaming
extensions Epic 08 already documented (richer responses, `WatchQueue`,
`GetCapabilities`); then have plan-20-06 add a contract test that asserts
each `.proto` declares exactly the canonical RPC list (no more, no less).
Drop the invented `maktaba.api.v1.proto`.

---

## 5. Postgres LISTEN/NOTIFY trace continuity — affects Epic 21 [RESOLVED]

> **Resolution.** plan-21-03 added §8.1 LISTEN/NOTIFY trace continuity:
> NOTIFY emitter encodes `traceparent` in the JSON payload; LISTEN side
> reconstitutes span context via `propagation.TraceContext.Extract`.
> TC5 smoke test confirms trace ID survives worker → NOTIFY → API
> listener → WS client.

Architecture §1.4 + §7.10 drive WebSocket fan-out via Postgres
`LISTEN/NOTIFY` from worker rows. Epic 21.3 distributed tracing
([plan-21-03](epics/21-observability/plan-21-03-distributed-tracing.md))
installs OTel propagators for HTTP and gRPC (lines 73, 137, 141) but the
pgx tracer (lines 153–159) only wraps query execution — there is no
`traceparent` propagation through NOTIFY payloads. Job-progress traces
break at the bus.

**Recommendation.** Have the API NOTIFY emitter encode `traceparent` in
the JSON payload; the LISTEN side reconstitutes a span context via
`propagation.TraceContext.Extract`. Add a smoke test confirming a
trace ID survives worker → NOTIFY → API listener → WS client.

---

## 6. Per-epic findings

The remainder of this document is the per-epic detail. Each epic section
lists, per plan, a verdict and bullet findings tagged `[blocking]`,
`[major]`, `[minor]`. Cross-plan rollups appear at the end of each epic.

---

## Epic 18 — Performance

### plan-18-01 — Latency Budgets

**Verdict**: minor edits

- `[minor]` [plan-18-01:117-120](epics/18-performance/plan-18-01-latency-budgets.md) defines `web.player.first_frame.warm` p95 1500 ms but story 18.6 AC3 says cold transcode TTFF 3500 ms; the cold variant is missing here even though [plan-18-06:99-102](epics/18-performance/plan-18-06-client-perceived-performance.md) asserts a 3500 ms cold budget. Add `web.player.first_frame.cold`.
- `[minor]` [plan-18-01:111-114](epics/18-performance/plan-18-01-latency-budgets.md) lists `ws.job_progress.notify_to_client` p95 = 250 ms with no method/path. The loader at [plan-18-01:140-143](epics/18-performance/plan-18-01-latency-budgets.md) requires `Method`/`Path`. Mark them `omitempty` or document that WS endpoints get empty strings.
- `[minor]` [plan-18-01:80-86](epics/18-performance/plan-18-01-latency-budgets.md) cold-search budget lists `p95_ms: 1500` but no `p50_ms`; loader at [:175](epics/18-performance/plan-18-01-latency-budgets.md) only flags `p95 < p50` if both > 0. Document that absent percentiles are skipped.
- `[minor]` [plan-18-01:236](epics/18-performance/plan-18-01-latency-budgets.md) profile tag `linux-x86-16gb` checks `arch == "amd64"`; rename the tag to `linux-amd64-16gb` for consistency.
- `[minor]` See cross-cutting issue §1.6 (cache-flush admin endpoint URL drift).

### plan-18-02 — Search Performance

**Verdict**: minor edits

- `[major]` [plan-18-02:160-165](epics/18-performance/plan-18-02-search-performance.md) response uses snake_case (`segment_id`, `video_id`, `ts_start`, `degraded`, `took_ms`); architecture §9 REST surface uses camelCase. Cross-check against Epic 7 plan-07-08.
- `[minor]` [plan-18-02:106](epics/18-performance/plan-18-02-search-performance.md) reads `transcripts_fts`; architecture line 1469 specifies `unicode61 remove_diacritics 2`. EC2 says "FTS5 unicode61 tokenizer" without the `remove_diacritics` mode. Spell out the tokenizer mode.
- `[minor]` [plan-18-02:139-142](epics/18-performance/plan-18-02-search-performance.md) RRF map keys are `string` (`SegmentID`); canonical is `BIGSERIAL` (architecture line 1369). Use `int64`.
- `[minor]` [plan-18-02:204](epics/18-performance/plan-18-02-search-performance.md) metric `search_request_duration_seconds{cache}` should declare label values explicitly.

### plan-18-03 — Streaming Hot-Path

**Verdict**: minor edits

- `[major]` [plan-18-03:50-66](epics/18-performance/plan-18-03-streaming-hot-path.md) `OpenSession` returns `OpenSessionResponse{SessionId, ManifestUrl, Probe}`; architecture §9.9:1738 returns just `Session`. Same drift as Epic 08 flagged in PLAN_REVIEW_07_13 §1.5.
- `[major]` [plan-18-03:91](epics/18-performance/plan-18-03-streaming-hot-path.md) cache key is `(v.Path, v.Size, v.ModTime)`; canonical identity per architecture §1.5 is `content_hash`. Path-keyed cache triggers re-probe on every move/rename.
- `[major]` [plan-18-03:101](epics/18-performance/plan-18-03-streaming-hot-path.md) writes `videos.probe_data JSONB` — no such column; canonical is `media_info.raw_ffprobe JSONB` (architecture line 1335).
- `[minor]` [plan-18-03:233-257](epics/18-performance/plan-18-03-streaming-hot-path.md) config keys (`streaming.cache_dir`, `cache_max_gib`) drift from architecture §11.3 TOML sections (`[cache] root`, `max_gib = 50`).
- `[minor]` [plan-18-03:127](epics/18-performance/plan-18-03-streaming-hot-path.md) manifest URL pattern; architecture §4.8 cache layout uses session-scoped paths, not rendition-scoped.

### plan-18-04 — Pipeline Throughput

**Verdict**: minor edits

- `[major]` [plan-18-04:13](epics/18-performance/plan-18-04-pipeline-throughput.md) labels stages `transcribe, index, thumbnail, embed, diarize`; canonical is `scan|probe|extract|transcribe|index|thumb`. See cross-cutting §1.7.
- `[major]` [plan-18-04:106](epics/18-performance/plan-18-04-pipeline-throughput.md) inserts into `segments` — table is `transcript_segments`. See cross-cutting §1.1.
- `[minor]` [plan-18-04:225-229](epics/18-performance/plan-18-04-pipeline-throughput.md) config `concurrency.transcribe = 1` matches architecture line 1934; `embed = 2` and `diarize` are not in architecture's `concurrency` map. Add to architecture or drop.
- `[minor]` [plan-18-04:106-117](epics/18-performance/plan-18-04-pipeline-throughput.md) `index` stage does FTS + Chroma upsert. Architecture §3 / §8.4 splits these. Document or align.
- `[minor]` [plan-18-04:84](epics/18-performance/plan-18-04-pipeline-throughput.md) `MLXInitError → faster-whisper fallback` — story EC3 says throughput target is "relaxed" but not by how much. Pin a number (≥ 1× realtime CPU).

### plan-18-05 — Memory & CPU Envelopes

**Verdict**: minor edits

- `[minor]` [plan-18-05:212](epics/18-performance/plan-18-05-memory-cpu-envelopes.md) `pipeline.per_model_rss_mib: 4500` — confirm large-v3 MLX measured value or document source.
- `[minor]` [plan-18-05:64-77](epics/18-performance/plan-18-05-memory-cpu-envelopes.md) Darwin RSS via `task_for_pid` requires entitlements. Document.
- `[minor]` [plan-18-05:73-76](epics/18-performance/plan-18-05-memory-cpu-envelopes.md) Go CGO snippet won't compile (missing `&count`).
- `[minor]` [plan-18-05:181](epics/18-performance/plan-18-05-memory-cpu-envelopes.md) `expvar.Publish("memstats_pause_total_ns", …)` returns `any`; document numeric-string parse expectation.
- `[minor]` See cross-cutting §1.5 — fold `tests/soak/envelopes.yaml` into `shared/perf_budgets.yaml`.

### plan-18-06 — Client-Perceived Performance

**Verdict**: minor edits

- `[major]` [plan-18-06:78-86](epics/18-performance/plan-18-06-client-perceived-performance.md) TTFF measurement uses `timeupdate`; should use `loadeddata` (`loadedmetadata` is too early, `timeupdate` too late).
- `[minor]` See cross-cutting §1.6 (admin cache flush URL).
- `[minor]` [plan-18-06:117](epics/18-performance/plan-18-06-client-perceived-performance.md) `pressSequentially` debounce math — note the single-fire behavior.
- `[minor]` [plan-18-06:184](epics/18-performance/plan-18-06-client-perceived-performance.md) chained `lhci autorun` overwrites results; use `--collect.outputDir`.

### plan-18-07 — Database Query Performance

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[major]` [plan-18-07:51-56](epics/18-performance/plan-18-07-database-query-performance.md) selects `videos.duration_s`; canonical is `duration_sec`. Schema drift §1.1.
- `[major]` [plan-18-07:58-62](epics/18-performance/plan-18-07-database-query-performance.md) reads from `segments` with columns `ts_start, ts_end, text`; canonical is `transcript_segments` with `start_sec, end_sec`. Schema drift §1.1.
- `[major]` [plan-18-07:61](epics/18-performance/plan-18-07-database-query-performance.md) `WHERE video_id = ANY($1::text[])` — `transcript_segments` joins via `transcript_id`, not `video_id`. Query is missing the `transcripts` join; sqlc will refuse to compile.
- `[major]` [plan-18-07:91](epics/18-performance/plan-18-07-database-query-performance.md) `CREATE INDEX … ON segments (video_id, ts_start)` — table doesn't exist; canonical index already declared at architecture line 1379.
- `[major]` [plan-18-07:88-90](epics/18-performance/plan-18-07-database-query-performance.md) covering-index `INCLUDE (id, title, duration_s)` — column drift; canonical index already declared at architecture line 1322.
- `[major]` [plan-18-07:80](epics/18-performance/plan-18-07-database-query-performance.md) `EXTRACT(EPOCH FROM …)` not supported in SQLite; AC4 requires Postgres+SQLite parity.
- `[minor]` [plan-18-07:67-74](epics/18-performance/plan-18-07-database-query-performance.md) `SET state = 'running'` on claim — architecture §7.2 has explicit `claimed → running` transition. See cross-cutting §1.2.

### plan-18-08 — Cache Layout & Hit Rates

**Verdict**: minor edits

- `[major]` [plan-18-08:175-205](epics/18-performance/plan-18-08-cache-layout-hit-rates.md) imports `golang.org/x/crypto/ssh`; JWT/JWKS validation needs `crypto/rsa` or `lestrrat-go/jwx/v2/jwk`. Won't compile.
- `[major]` [plan-18-08:88-95](epics/18-performance/plan-18-08-cache-layout-hit-rates.md) registers `cache_hits_total{cache}`; story AC1 prefers per-cache prefix names. Reconcile (recommend label-based).
- `[major]` See cross-cutting §1.6 (admin cache flush URL collision).
- `[minor]` [plan-18-08:271-288](epics/18-performance/plan-18-08-cache-layout-hit-rates.md) config block extends architecture §11.3 implicitly.
- `[minor]` [plan-18-08:101-103](epics/18-performance/plan-18-08-cache-layout-hit-rates.md) reset comment incomplete.

### Cross-plan issues for Epic 18

Recurring drift items already covered in the global cross-cutting section:
- §1.1 schema drift (segments/ts_start/duration_s, videos.probe_data).
- §1.5 single-source-of-truth for budgets (envelopes vs perf_budgets, plan-20-07 ci_pr field).
- §1.6 admin cache flush URL — three contradictory shapes.
- §1.7 pipeline stage labels `thumb` vs `thumbnail`, plus `embed`/`diarize`.

Epic-internal:
- **Probe storage location.** plan-18-03 says `videos.probe_data JSONB`; canonical store is `media_info.raw_ffprobe`.
- **Search response casing.** plan-18-02 uses snake_case; cross-check vs plan-07-08.
- **Config drift.** plan-18-03 §10 uses YAML keys; architecture §11.3 uses TOML sections.

---

## Epic 19 — Scalability

### plan-19-01 — Single-Host Capacity Floor

**Verdict**: minor edits

- `[minor]` AC1 specifies a mixed `8 streams + 1 active transcribe + 100 search qps` workload; plan §2 defines `Mix{Transcribers:1, Indexers:4}` and TC3 ([plan-19-01:202](epics/19-scalability/plan-19-01-single-host-capacity.md)) runs `Mix{DirectPlay:8, Transcoded:0}` — the AC1 mix isn't exercised end-to-end.
- `[minor]` [plan-19-01:73](epics/19-scalability/plan-19-01-single-host-capacity.md) `streaming.rss_max_mib: 800` is parent-only RSS; FFmpeg children unaccounted. Cross-link Story 18.5 envelope.
- `[minor]` [plan-19-01:137](epics/19-scalability/plan-19-01-single-host-capacity.md) EC1 cutoff `60 MB/s` vs story's `50 MB/s`. Adopt story's number.
- `[minor]` Seed should use `SentinelUserID` (Story 19.8) rather than random UUIDs.

### plan-19-02 — API Scale-Out

**Verdict**: minor edits

- `[minor]` [plan-19-02:49-52](epics/19-scalability/plan-19-02-api-scale-out.md) extends `events` schema with `user_id`/`library_id` not in story AC3.
- `[minor]` [plan-19-02:67-78](epics/19-scalability/plan-19-02-api-scale-out.md) plan always persists row + notifies; story AC3 implies small payloads could ride NOTIFY alone. Document the override.
- `[minor]` [plan-19-02:99-103](epics/19-scalability/plan-19-02-api-scale-out.md) listener fetches every event by id even on fast-path — gate behind `ref:true`.
- `[minor]` [plan-19-02:267](epics/19-scalability/plan-19-02-api-scale-out.md) `overflow_threshold_per_minute` conflates threshold with window.
- `[minor]` EC1 "5 s of first dropped notification" not asserted by any TC.

### plan-19-03 — Streaming Scale-Out

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[major]` [plan-19-03:39-46,48](epics/19-scalability/plan-19-03-streaming-scale-out.md) introduces `streaming_replicas` table and `streaming_sessions.replica_id` column not declared in architecture §8. Either add to arch or own as explicit override.
- `[major]` [plan-19-03:240](epics/19-scalability/plan-19-03-streaming-scale-out.md) Caddyfile uses `lb_policy hash {http.request.uri.query.session_id}` matching arch §10.3, but [:101](epics/19-scalability/plan-19-03-streaming-scale-out.md) also embeds `ReplicaUrl` in the response — two routing mechanisms coexist. Pick one.
- `[minor]` [plan-19-03:139](epics/19-scalability/plan-19-03-streaming-scale-out.md) references `watch_state` (Epic 9); canonical is `playback_state` (architecture line 1512). Schema drift §1.1.
- `[minor]` [plan-19-03:202](epics/19-scalability/plan-19-03-streaming-scale-out.md) TC2 reinterprets AC3 to allow duplicate cold transcode after replica migration. Tighten story wording or accept this.

### plan-19-04 — Pipeline Scale-Out

**Verdict**: ~~blocking~~ **RESOLVED**

- `[blocking]` [plan-19-04:39-44](epics/19-scalability/plan-19-04-pipeline-scale-out.md) creates parallel duplicate columns (`worker_id`, `heartbeat_at`, `attempt`) instead of using canonical `claimed_by`, `last_heartbeat_at`, `attempts` (architecture §7.1). See cross-cutting §1.3.
- `[blocking]` [plan-19-04:55-72](epics/19-scalability/plan-19-04-pipeline-scale-out.md) claim SQL uses `scheduled_at` and `ORDER BY priority DESC`; canonical uses `not_before` and `ORDER BY priority` (lower wins, architecture §7.5).
- `[blocking]` [plan-19-04:104-110](epics/19-scalability/plan-19-04-pipeline-scale-out.md) reaper resets `running` to `pending`, losing `paused`/`pause_requested` state per architecture §7.9.
- `[major]` [plan-19-04:13](epics/19-scalability/plan-19-04-pipeline-scale-out.md) chroma-writer `pg_advisory_lock(hashtext('chroma:writer'))` is an implementation choice not in architecture §10.3. Cross-check with Story 24.4.
- `[minor]` Heartbeat cadence 30 s vs architecture §7.1's "every 5 s".
- `[minor]` Plan only specifies `transcribe` + `index` concurrency; architecture §11.4 has full default map.

### plan-19-05 — Database Scaling

**Verdict**: minor edits

- `[minor]` AC1 references Story 18.7 query budgets; plan delegates entirely. Make pointer explicit.
- `[minor]` [plan-19-05:113](epics/19-scalability/plan-19-05-database-scaling.md) lag-detection window 60–65 s vs claim of "5 s detection". Document.
- `[minor]` [plan-19-05:50](epics/19-scalability/plan-19-05-database-scaling.md) `setup-replica.sh` uses `pg_basebackup -R` but story says "recovery.conf"; recovery.conf is gone since PG 12. Story wording is stale.
- `[minor]` [plan-19-05:210](epics/19-scalability/plan-19-05-database-scaling.md) `ALTER TABLE … ADD COLUMN … NOT NULL[^D]*$` regex is fragile.
- `[minor]` Plan ships replica unconditionally; arch §10.3 says "only if search QPS becomes a bottleneck". Gate behind config flag or call out deviation.

### plan-19-06 — Storage Scaling

**Verdict**: minor edits

- `[minor]` [plan-19-06:138-143](epics/19-scalability/plan-19-06-storage-scaling.md) `INSERT INTO videos (id, content_hash, path, size, mtime)` — `size_bytes`, not `size`; `library_id` (NOT NULL) missing. Schema drift §1.1.
- `[minor]` Hash construction order `head ‖ size_le8 ‖ tail` matches story AC2; document the order is part of the contract.
- `[minor]` AC1 wall-clock budget covers 30 minutes but plan only models hash work, not directory enumeration + DB upsert.
- `[minor]` [plan-19-06:158-168](epics/19-scalability/plan-19-06-storage-scaling.md) watcher `asyncio.get_running_loop()` in watchdog thread will raise; verify thread/loop boundary.

### plan-19-07 — Concurrency Caps

**Verdict**: minor edits

- `[minor]` Story AC1 requires architecture §11.3 to reference Story 19.7 for `max_concurrent` default; this doc edit is unowned by any plan.
- `[minor]` [plan-19-07:257](epics/19-scalability/plan-19-07-concurrency-caps.md) `cpu_semaphore_size: max(1, num_cores - 2)` not in arch.
- `[minor]` [plan-19-07:184](epics/19-scalability/plan-19-07-concurrency-caps.md) increments `used_usd` at claim time; double-counts on retry. Move to job-completion or compensate on failure.
- `[minor]` [plan-19-07:135](epics/19-scalability/plan-19-07-concurrency-caps.md) hot-reload spawns goroutines that hold tickets forever; add cancellation.
- `[minor]` `pipeline_budget_blocked_total` lacks `library_id` label; you can't tell which library tripped.

### plan-19-08 — Multi-Tenant Readiness

**Verdict**: ~~blocking~~ **RESOLVED**

- `[blocking]` [plan-19-08:64-66,71](epics/19-scalability/plan-19-08-multi-tenant-readiness.md) writes `INSERT INTO users (id, email, role, created_at)`; arch §8.5:1504-1510 has `users(id, username, pw_hash, is_admin, created_at)`. Migration fails at first execution.
- `[blocking]` [plan-19-08:74-79](epics/19-scalability/plan-19-08-multi-tenant-readiness.md) backfills `watch_state`; canonical is `playback_state` (arch line 1512).
- `[blocking]` `users_no_real_sentinel` CHECK references nonexistent `email` column.
- `[major]` AC1 lists "collections-by-user, favorites, user_settings" as user-scoped — none exist in arch §8. Either own the new tables here or restrict scope to existing tables (`playback_state`, `saved_searches`).
- `[major]` [plan-19-08:107](epics/19-scalability/plan-19-08-multi-tenant-readiness.md) returns `SentinelUserID` even on the unauthenticated branch — should match Story 23.1 AC5 admin-token gate.
- `[minor]` Redundant `PRIMARY KEY` + `UNIQUE INDEX` on `library_acl(library_id, user_id)`.
- `[minor]` Backfill misses libraries created after flag flip — add trigger or library-create handler insert.
- `[minor]` `MAKTABA_MULTI_USER` env vs `auth.multi_user` config naming mismatch.

### Cross-plan issues for Epic 19

- **Schema drift on `processing_jobs`** (plan-19-04). See cross-cutting §1.3.
- **Severe schema drift on `users`/`playback_state`** (plan-19-08). See cross-cutting §1.1.
- **Replica-tracking schema not in arch §8** (plan-19-03). Ownership unclear.
- **Default `transcode.max_concurrent` mismatch.** Story 19-07 AC1 says `max(1, num_cores/4)`; arch §11.3 sample TOML still hardcodes `max_concurrent = 4`. Arch doc edit is unowned.
- **Throughput-claim consistency between 19-01 and 18-04.** plan-19-01's capacity rig doesn't assert the throughput floor from Story 18-04.
- **Single-writer guard interaction (19-04 vs 19-07).** Both plans requeue jobs to `pending`; if they fire on the same row, last-write-wins clobbers `not_before`/reason.
- **Advisory-lock key namespace.** plan-19-04 chroma-writer uses session-scoped `pg_try_advisory_lock`; GPU lock uses xact-scoped `pg_advisory_xact_lock`. Both `blake2b → BIGINT`. Document namespace allocation.

---

## Epic 20 — Testing

### plan-20-01 — Test Pyramid & Runtime Budgets

**Verdict**: minor edits

- `[major]` [plan-20-01:46-71](epics/20-testing/plan-20-01-test-pyramid.md) Go `WithSoftCap` uses `time.Duration` and `time.Now()` but `"time"` not imported.
- `[major]` [plan-20-01:101-103](epics/20-testing/plan-20-01-test-pyramid.md) Python soft-cap calls `item.duration` and `item.warn(...)` — neither attribute exists; use `pytest_runtest_logreport` against `report.duration`.
- `[major]` [plan-20-01:117-123](epics/20-testing/plan-20-01-test-pyramid.md) `[tool.pytest.ini_options.tier_caps]` is fictional pytest config. Replace with `pytest_collection_modifyitems` hook setting `pytest.mark.timeout(...)`.
- `[major]` Story AC2 says unit ≤ 60 s total + 100 ms per-test; plan-20-01 enforces 300 ms in pytest, 100 ms in Go, 300 ms in Vitest. Pick one.
- `[major]` [plan-20-01:144](epics/20-testing/plan-20-01-test-pyramid.md) `netguard.ts` uses async `import('node:net').then(…)` in setupFile; `net.Socket` is replaced after user-code may have already pulled `Socket`. Use `vi.mock`.
- `[minor]` [plan-20-01:164](epics/20-testing/plan-20-01-test-pyramid.md) `services: docker: { image: docker:dind }` redundant on GitHub-hosted runners.
- `[minor]` Wall-clock claim "≤ 18 min" omits checkout/build/fixture setup.

### plan-20-02 — Fixtures & Seed Data

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[blocking]` [plan-20-02:111-115](epics/20-testing/plan-20-02-fixtures-seed-data.md) SQL dump targets `segments(id, video_id, ts_start, ts_end, text)` — table is `transcript_segments` with `(transcript_id, seq, start_sec, end_sec, text)`. Also missing parent rows in `libraries`, `transcripts`, `audio_tracks`.
- `[major]` [plan-20-02:48-61](epics/20-testing/plan-20-02-fixtures-seed-data.md) cites a fabricated xiph URL. Replace with a real link-checked URL.
- `[major]` [plan-20-02:201-205](epics/20-testing/plan-20-02-fixtures-seed-data.md) `corrupt-moov.mp4` recipe matches `moov` substring anywhere in bytestream; should match atom-header offsets.
- `[minor]` [plan-20-02:65-77](epics/20-testing/plan-20-02-fixtures-seed-data.md) `LICENSE` parser format unspecified.
- `[minor]` [plan-20-02:93-100](epics/20-testing/plan-20-02-fixtures-seed-data.md) goldens normalizer doesn't canonicalize floating-point trailing zeros; pin ffmpeg version or run numeric-canonicalizer.
- `[minor]` Per-file 5 MiB cap insufficient for `multitrack-2a-2s.mkv` 60 s.

### plan-20-03 — Unit Test Coverage & Conventions

**Verdict**: minor edits

- `[major]` Story AC1 path normalization: trailing-slash vs `/...` glob inconsistency between Go and Python entries.
- `[major]` [plan-20-03:231](epics/20-testing/plan-20-03-unit-test-coverage.md) `-coverpkg=./...` will include integration code paths if integration tests run in same invocation.
- `[major]` Plan covers auth and hash mutation gates but `signed-URL` (story AC) has no gate.
- `[minor]` `excludes.go` `**/zz_*.go` is gqlgen/ent style, not sqlc; sqlc generates `db.go`/`models.go`/`*.sql.go`.
- `[minor]` Pyramid shape: 100 ms in story-20-01, 300 ms in plan-20-01, undefined here.
- `[minor]` [plan-20-03:174-191](epics/20-testing/plan-20-03-unit-test-coverage.md) `init()` AST walker skips every dir literally named `cmd`, including `internal/cmd`.

### plan-20-04 — Integration Tests with Real Backends

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[blocking]` [plan-20-04:171](epics/20-testing/plan-20-04-integration-tests.md) SQL `SELECT COUNT(*) FROM segments WHERE video_id=$1` — table is `transcript_segments` with `transcript_id`, not `video_id`. Schema drift §1.1.
- `[major]` Postgres image pin disagreement: `postgres:16` (plan-20-01), `postgres:16.4-alpine3.20` (plan-20-04, plan-20-05). Story EC2 says "pin postgres:16 exactly". Pick one.
- `[major]` [plan-20-04:128-139](epics/20-testing/plan-20-04-integration-tests.md) `pgembed_fallback.go` build-tag conflict will produce duplicate declarations.
- `[major]` [plan-20-04:82](epics/20-testing/plan-20-04-integration-tests.md) `WithTx` returns `wrapTxAsDB(tx)` returning `*sql.DB` — type incompatible.
- `[major]` Replay tape format conflict — Whisper streaming responses need base64 + chunk timing, not YAML strings.
- `[minor]` Pipeline integration in §3 only shows ChromaDB fixture; story AC2 wants Postgres + ChromaDB + FFmpeg.
- `[minor]` [plan-20-04:154](epics/20-testing/plan-20-04-integration-tests.md) bufconn against "real Pipeline handlers" — Pipeline is Python (`grpc.aio`), not Go. AC4 forbids mocks at boundaries.

### plan-20-05 — End-to-End Smoke Flows

**Verdict**: minor edits

- `[major]` [plan-20-05:51](epics/20-testing/plan-20-05-e2e-smoke-flows.md) comment misattributes retry rule to AC4; correct AC is in Story 20.8.
- `[major]` [plan-20-05:15](epics/20-testing/plan-20-05-e2e-smoke-flows.md) `--block-service-worker=false` is not a Playwright option; use `serviceWorkers: 'allow' | 'block'` in `use:`.
- `[major]` Story AC1 doesn't require Webkit; Webkit on Linux CI has known HLS quirks. Either prove the path or drop the project.
- `[minor]` [plan-20-05:156](epics/20-testing/plan-20-05-e2e-smoke-flows.md) `seed.expected.firstHitTs` import undefined.
- `[minor]` [plan-20-05:225](epics/20-testing/plan-20-05-e2e-smoke-flows.md) `--max-failures=5` placement wrong — move from config to CLI.
- `[minor]` [plan-20-05:93](epics/20-testing/plan-20-05-e2e-smoke-flows.md) port collision risk vs random-host-ports EC.

### plan-20-06 — Contract Tests for Service Boundaries

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[major]` [plan-20-06:25-28](epics/20-testing/plan-20-06-contract-tests.md) lists `maktaba.api.v1.proto` — there is no API gRPC service. See cross-cutting §4.
- `[major]` [plan-20-06:42](epics/20-testing/plan-20-06-contract-tests.md) `grpc_drift_test.go` referenced but body not shown — no actual contract test enumerates the eight canonical RPCs.
- `[major]` [plan-20-06:73-74](epics/20-testing/plan-20-06-contract-tests.md) picks `buf.build/protocolbuffers/python` over `betterproto`; architecture line 233 names betterproto.
- `[major]` REST OpenAPI extractor walks chi only; GraphQL/WS surfaces still need schema-drift coverage.
- `[minor]` `events.ts` source-of-truth file location.
- `[minor]` `buf.gen.yaml` plugins not pinned by digest despite EC1 claim.

### plan-20-07 — Performance Regression Tests in CI

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[blocking]` [plan-20-07:56](epics/20-testing/plan-20-07-perf-regression-ci.md) reads `e.CIPR && e.Cache == "warm"` against plan-18-01's `Budget` struct — the field doesn't exist. See cross-cutting §1.5.
- `[major]` 10 % regression vs absolute breach gate ambiguity.
- `[major]` Mac runner tag `mac-m2-8gb` vs plan-18-01 profile `darwin-arm64-16gb-m2`. Mismatch.
- `[major]` `make perf-baseline-fetch` referenced but no Makefile target.
- `[major]` "3× in 5 days on main" rule lacks `distinct_dates >= 3` differentiation.
- `[minor]` `perf-history` branch needs `permissions: contents: write`.
- `[minor]` Dashboard fetches via raw GitHub URL — won't work on private repos.

### plan-20-08 — Flaky Test Policy

**Verdict**: minor edits

- `[major]` Story AC2 specifies `t.Skip(reason="quarantined-flake-#issue")`; plan uses `t.Skipf("quarantined-flake: %s", iss)`. Match story.
- `[major]` `init()` in `test_skip_helper.go` violates plan-20-03 EC3's init-ban lint. Move to lazy `sync.Once`.
- `[major]` "Convention lint enforces SkipIfQuarantined call" is named but never implemented in the layout.
- `[minor]` `flake-record.yml` should filter cancelled workflow runs.
- `[minor]` `alerting.Page` import path missing.
- `[minor]` Log regex for `flake_category=infra` will misclassify legitimate failures mentioning those strings.

### Cross-plan issues for Epic 20

- **Schema drift** (plan-20-02, plan-20-04). Both reference `segments.video_id`; canonical is `transcript_segments.transcript_id`. See cross-cutting §1.1.
- **Postgres pin disagreement** (plan-20-01 vs plan-20-04 vs plan-20-05). Pick `postgres:16.4-alpine3.20` and propagate.
- **Unit-test soft cap** disagreement: 100 ms (story), 300 ms (Python), 100 ms (Go), 300 ms (Vitest).
- **TV testing frameworks not addressed** (Swift/XCTest, Kotlin/Compose-test). See cross-cutting §3.
- **gRPC service file invention** (plan-20-06's `maktaba.api.v1.proto`). See cross-cutting §4.
- **Budget struct drift** (plan-20-07 references `CIPR` field absent from plan-18-01). See cross-cutting §1.5.
- **`init()` ban contradiction** (plan-20-03 forbids it; plan-20-08's helper defines one).

---

## Epic 21 — Observability

### plan-21-01 — Structured Logging

**Verdict**: minor edits

- `[minor]` [plan-21-01:75](epics/21-observability/plan-21-01-structured-logging.md) imports incomplete (`"strings"`, `"time"`).
- `[minor]` AC2 requires `service` on every line; init wires `.With("service", service)` so OK, but lint should also flag a logger created without `Default`.
- `[minor]` SIGUSR1 cycles `{Info, Debug, Warn}`; story implies `{Info, Debug}` only.
- `[minor]` `installSIGUSR1` Windows stub not shown.
- `[minor]` `From(ctx)` doesn't read OTel span context — plan-21-03 expects it does. Wire `trace.SpanContextFromContext(ctx)`.

### plan-21-02 — Metrics Surface

**Verdict**: minor edits

- `[major]` Port collision with plan-21-04 (both claim 9100/9101/9102). See cross-cutting §1.8.
- `[minor]` [plan-21-02:116](epics/21-observability/plan-21-02-metrics-surface.md) only mounts `/metrics`; reconcile with plan-21-04 expecting `/healthz` on same port.
- `[minor]` Metric names for HTTP collapsed across services (`http_request_duration_seconds`); story AC1 implies per-service prefix.
- `[minor]` Cardinality lint regex hard-codes `kid_id` exception.
- `[minor]` Web vitals route registration not shown.

### plan-21-03 — Distributed Tracing

**Verdict**: minor edits

- `[major]` Postgres LISTEN/NOTIFY trace continuity not addressed. See cross-cutting §5.
- `[minor]` [plan-21-03:73](epics/21-observability/plan-21-03-distributed-tracing.md) propagator only `TraceContext`; add `Baggage`.
- `[minor]` [plan-21-03:122-124](epics/21-observability/plan-21-03-distributed-tracing.md) variable shadow `h := …` conflicts with outer `h`.
- `[minor]` Buffer cap 8 MiB vs story's 10 MiB.
- `[minor]` [plan-21-03:182](epics/21-observability/plan-21-03-distributed-tracing.md) `Psycopg2Instrumentor` — pipeline uses `asyncpg`. Replace.
- `[minor]` gRPC propagation smoke-test missing across both legs.

### plan-21-04 — Health & Readiness Probes

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[blocking]` Admin-port mux ownership conflict. See cross-cutting §1.8.
- `[major]` Liveness "always 200" too weak per story EC1; `WatchdogSec=30` systemd line declared but Go code never calls `sd_notify("WATCHDOG=1")`.
- `[minor]` [plan-21-04:95](epics/21-observability/plan-21-04-health-readiness-probes.md) sequential checks; parallelize via errgroup.
- `[minor]` [plan-21-04:156-162](epics/21-observability/plan-21-04-health-readiness-probes.md) `deriveStatus` — confirm matches story EC2.
- `[minor]` Probe auth pass-through if metrics-port shares mux.
- `[minor]` `BudgetUSDLeft` depends on Story 19.7 surface.

### plan-21-05 — Error Reporting

**Verdict**: minor edits

- `[minor]` [plan-21-05:228](epics/21-observability/plan-21-05-error-reporting.md) typo `MaxQueueSize`.
- `[minor]` [plan-21-05:94-105](epics/21-observability/plan-21-05-error-reporting.md) gRPC interceptor collapses status codes to `Internal`. Preserve original status.
- `[minor]` `uuid.NewV7()` import path inconsistency vs `gofrs/uuid/v5`.
- `[minor]` `error_id` redaction interaction with plan-21-08.
- `[minor]` Sentry `BeforeSend` should also strip `ev.ServerName`.
- `[minor]` Epic 6 dependency on `processing_jobs.last_error_id` column.

### plan-21-06 — Audit Log

**Verdict**: ~~blocking~~ **RESOLVED**

- `[blocking]` Schema-ownership conflict with Epic 09 plan-09-17 + Epic 12 plan-12-10. See cross-cutting §1.4.
- `[blocking]` `category='device'` rejected by both this and plan-09-17's enum.
- `[major]` Epic 23 audit needs (rate-limit events) — confirm category mapping (probably `auth`).
- `[major]` Column `actor_user` (no `_id`) drifts from Epic 09's `actor_user_id`; FK to `users(id)` absent.
- `[minor]` `BIGSERIAL` PK across partitions risks collision; consider UUIDv7.
- `[minor]` `audit_log_security`/`audit_log_library` views can't have indexes; document so plan-21-07 EXPLAIN expectations are realistic.

### plan-21-07 — Job & Pipeline Visibility

**Verdict**: minor edits

- `[major]` [plan-21-07:89-95](epics/21-observability/plan-21-07-job-pipeline-visibility.md) joins `audit_log` on `event='job_error'`; plan-21-06's enum has no clean home for `job_error`.
- `[minor]` [plan-21-07:92](epics/21-observability/plan-21-07-job-pipeline-visibility.md) cast `target_id::uuid` against `target_id TEXT` breaks index usage.
- `[minor]` [plan-21-07:242](epics/21-observability/plan-21-07-job-pipeline-visibility.md) introduces `job.segment_progress` event without referencing canonical §7.10 envelope.
- `[minor]` Server-side 1 Hz batching may suppress events the UI was prepared to throttle itself.
- `[minor]` `ClassifyState` `stuck` only shown for REST; confirm WS applies it.

### plan-21-08 — Telemetry Privacy

**Verdict**: minor edits

- `[minor]` `[telemetry]` config not in architecture §11. Add §11.6 stub or have plan-21-08 own it.
- `[minor]` `forbidden_in_attrs` includes `transcript_text` — confirm audit emitters bypass redaction.
- `[minor]` [plan-21-08:108](epics/21-observability/plan-21-08-telemetry-privacy.md) slog `WithHandler` doesn't exist; use `slog.New(redact.New(handler, rules))`.
- `[minor]` [plan-21-08:69](epics/21-observability/plan-21-08-telemetry-privacy.md) path masking only handles `MAKTABA_MEDIA_ROOT`; cache/log paths leak.

### Cross-plan issues for Epic 21

- See cross-cutting §1.4 (audit_log ownership), §1.8 (admin-port mux), §5 (LISTEN/NOTIFY trace continuity).
- **`[telemetry]` config undocumented in architecture §11.** Plans 21-02/03/05/08 all populate it.
- **`error_id` propagation to audit_log** — plan-21-05 emits to logs/webhook; plan-21-06 has no column. Document `payload->>'error_id'` location and Epic 06 ownership of `processing_jobs.last_error_id`.
- **Metric naming consistency** — `http_request_duration_seconds` shared across services vs story AC1's `*_request_duration_seconds` per-service prefix.

---

## Epic 22 — DevOps & Delivery

### plan-22-01 — CI Pipeline

**Verdict**: minor edits

- `[major]` Lint covers Go, Python, Web; arch §12.1 lists Swift (`apps/tvos/`) and Kotlin (`apps/androidtv/`) — neither `xcodebuild test` nor `gradle test` run. AC-1.2 unmet. See cross-cutting §3.
- `[major]` No `make generate && git diff --exit-code` gate for sqlc/gqlgen/proto; arch §12.5 says generated code is checked in.
- `[minor]` [plan-22-01:225,231](epics/22-devops/plan-22-01-ci-pipeline.md) literal `<DIGEST>` placeholders fail YAML validation as written.
- `[minor]` [plan-22-01:206-212](epics/22-devops/plan-22-01-ci-pipeline.md) `uv sync --frozen --directory pipeline` — wrong flag form. Use `cd pipeline && uv sync --frozen`.
- `[minor]` `ci-success` rollup wording (skipped vs success).
- `[minor]` `pipeline/.python-version` referenced; confirm committed.

### plan-22-02 — Reproducible Builds

**Verdict**: minor edits

- `[major]` [plan-22-02:65](epics/22-devops/plan-22-02-reproducible-builds.md) replaces `api/Dockerfile`/`streaming/Dockerfile`/`web/Dockerfile` (arch §12.1) with `.ko.yaml`. Either update arch or keep Dockerfiles.
- `[major]` [plan-22-02:88-91](epics/22-devops/plan-22-02-reproducible-builds.md) injects `-X maktaba/internal/version.Tag=...`. See cross-cutting §1.9.
- `[minor]` [plan-22-02:106-107](epics/22-devops/plan-22-02-reproducible-builds.md) `sed -i 's/Built on .*/…/'` heuristic mangles unrelated text; reproducibility should come from disabling banner timestamps in build.
- `[minor]` `sed -i` GNU-only; document or use BSD-portable form.
- `[minor]` `vendor/` directories checked in but not shown in arch §12.1 tree.

### plan-22-03 — Container Images

**Verdict**: minor edits

- `[major]` [plan-22-03:101](epics/22-devops/plan-22-03-container-images.md) `/usr/local/bin/healthcheck` ghost binary. See cross-cutting §1.11.
- `[major]` [plan-22-03:226-239](epics/22-devops/plan-22-03-container-images.md) duplicate `volumes:` key — second silently shadows first; FFmpeg/ffprobe binds dropped.
- `[minor]` Caddy env-default syntax OK.
- `[minor]` `/opt/homebrew/bin/ffmpeg` Apple-Silicon-only path; parameterize via `MAKTABA_FFMPEG_HOST`.
- `[minor]` [plan-22-03:319](epics/22-devops/plan-22-03-container-images.md) `import mlx.core` always fails inside Docker; doctor will fail under Mac overlay. Split native vs container.

### plan-22-04 — Database Migrations

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[blocking]` See cross-cutting §1.1 — schema canonicalization unowned.
- `[blocking]` [plan-22-04:252](epics/22-devops/plan-22-04-database-migrations.md) `//go:embed shared/db/migrations/*.sql` cannot escape package directory. See cross-cutting §1.9.
- `[blocking]` Migration ordering across 16 ALTER-emitting epics — append-only check catches re-edits but not ordering. Add CI integration job that boots full migration set against empty DB.
- `[major]` `lints.json` exemption schema keys by basename; two epics could both add `0042_*`.
- `[major]` `IF NOT EXISTS` on `ADD COLUMN` requires SQLite ≥ 3.35.
- `[minor]` `gen_random_uuid()` requires `pgcrypto` — that init migration must be authored here, not assumed in Epic 1.

### plan-22-05 — Release Management

**Verdict**: minor edits

- `[major]` [plan-22-05:46](epics/22-devops/plan-22-05-release-management.md) `internal/version/version.go` import path. See cross-cutting §1.9.
- `[major]` [plan-22-05:217-225](epics/22-devops/plan-22-05-release-management.md) `tag_sha` not exported between steps; use `outputs:` or `$GITHUB_ENV`.
- `[major]` [plan-22-05:175](epics/22-devops/plan-22-05-release-management.md) `sed` regex matches first `version =` in TOML, may hit wrong section. Use `tomlq` or anchor on `[project]`.
- `[minor]` `sed -i` BSD/GNU portability.
- `[minor]` `BuildTime = "0"` numeric override should surface `dev` marker.
- `[minor]` `compatibleApiVersion` Capacitor field. See cross-cutting §1.10.

### plan-22-06 — Upgrade and Rollback

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[major]` Schema rollback gap — plan correctly never invokes `migrate down` but doesn't simulate "old binary against new schema". Add a rollback-simulator test.
- `[major]` [plan-22-06:84](epics/22-devops/plan-22-06-upgrade-rollback.md) `bc -l` returns null on missing input; guard `${duration:-0}`.
- `[major]` [plan-22-06:97-100](epics/22-devops/plan-22-06-upgrade-rollback.md) ghost binaries `/usr/local/bin/drain`, `/usr/local/bin/healthcheck`. See cross-cutting §1.11.
- `[minor]` Version-jump guard pre-release tag handling.
- `[minor]` `maktaba-api config migrate` subcommand owner not declared.
- `[minor]` Backup-before-upgrade flag missing; bridge to Epic 24.

### plan-22-07 — Multi-Platform Packaging

**Verdict**: minor edits

- `[major]` [plan-22-07:96-103](epics/22-devops/plan-22-07-multi-platform-packaging.md) `system("psql ...")` returns `false` if `psql` is missing; the formula then *skips* bootstrap. Logic inverted.
- `[major]` `compatibleApiVersion` Capacitor field. See cross-cutting §1.10.
- `[major]` [plan-22-07:179-181](epics/22-devops/plan-22-07-multi-platform-packaging.md) wheel packaged at directory path; should ship venv or wheel + `uv venv` postinst.
- `[major]` [plan-22-07:241](epics/22-devops/plan-22-07-multi-platform-packaging.md) `Type=notify` requires `sd_notify` calls in the Go binary; not added.
- `[minor]` Ubuntu 22.04 ships ffmpeg 4.4; bump baseline or static-link.
- `[minor]` Tauri updater placeholder syntax cross-check.

### plan-22-08 — Local Developer Workflow

**Verdict**: minor edits

- `[major]` Makefile missing `make migrate` and `make apps` targets (arch §12.2 lists them).
- `[major]` [plan-22-08:296-303](epics/22-devops/plan-22-08-local-developer-workflow.md) `dnephin/pre-commit-golang` unmaintained since 2021. Use `tekwizely/pre-commit-golang` or local hooks.
- `[minor]` `.air.toml` `tmp_dir` inside bind mount races editor watchers.
- `[minor]` macOS BSD-awk vs GNU-awk regex.
- `[minor]` `TestNoVerifyBypassedCaughtByCi` belongs in 22-01.

### Cross-plan issues for Epic 22

- See cross-cutting §1.1 (schema canonicalization), §1.9 (top-level Go module), §1.10 (Capacitor field), §1.11 (ghost binaries).
- **No multi-language test gate** — Swift/Kotlin not in CI matrix.
- **No proto/sqlc/gqlgen drift gate** in lint.
- **Dockerfile vs `ko`** — arch §12.1 lists Dockerfiles; plan-22-02 replaces three with `ko`.
- **HMR + Caddyfile**: dev compose overlay must override Caddy upstream to point at `web:5173`.
- **Mobile keystore policy**: keystore-out-of-repo policy not declared.
- **Pipeline doctor MLX check** behaves differently in Docker vs native; gate on `MAKTABA_RUNTIME` or split.

---

## Epic 23 — Security

### plan-23-01 — Authentication

**Verdict**: minor edits

- `[major]` Schema collision with Epic 10 plan-10-06 — both build JWKS document and own signing-key store. New `signing_keys` table at migration 0040 duplicates plan-10-06. See §2 cross-epic table.
- `[major]` `MAKTABA_KEY_ENCRYPTION_KEY` introduced but never registered in plan-23-04's `AllSpecs`.
- `[minor]` AccessClaims includes both `Sub` and `Usr` carrying user UUID.
- `[minor]` Redundant `Aud string` + `RegisteredClaims.Aud`.
- `[minor]` `auth.multi_user=true` short-circuit naming inversion.

### plan-23-02 — Authorization and ACLs

**Verdict**: ~~blocking~~ **RESOLVED**

- `[blocking]` Three-role per-library model contradicts Epic 10 plan-10-13's binary admin/non-admin model. See §2 cross-epic table.
- `[blocking]` story-23-02 AC2 and plan-10-13 cannot both ship.
- `[major]` `Authorize(Action, Resource)` signature vs plan-10-13's `Authz.Can(ctx, Action, resourceID)` — two parallel authz packages.
- `[major]` Audience-string mapping for `streaming-direct` overlaps plan-10-08.
- `[minor]` `slices` import path: stdlib (Go 1.21+) vs `golang.org/x/exp/slices`.

### plan-23-03 — Transport Security

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[blocking]` HSTS placement contradiction with plan-10-15 (backend middleware). See §2 cross-epic table.
- `[major]` `internal_mtls` config introduced; arch §1.4 doesn't mandate mTLS.
- `[major]` Internal CA leaf TTL 24 h tied to JWT signing-key rotation 90 d — independent rotations; reword.
- `[minor]` Caddy v2 `ciphers` directive syntax (1.2 only).
- `[minor]` Bootstrap-token "printed once to stdout" conflicts with plan-23-04 redaction; document exemption.

### plan-23-04 — Secrets Management

**Verdict**: minor edits

- `[major]` Registry omits `MAKTABA_KEY_ENCRYPTION_KEY` (plan-23-01) and `MAKTABA_INTERNAL_BOOTSTRAP_TOKEN` (plan-23-03).
- `[major]` `MAKTABA_JWT_PRIVATE_KEY_PEM` listed as required env, but plan-23-01 stores keys in DB encrypted. See §2 cross-epic table.
- `[minor]` `OPENAI_API_KEY` regex `^sk-` doesn't cover `sk-proj-`/`sess-`.
- `[minor]` Settings PUT requires `OwnerAPI` — admins can't set pipeline-owned secrets, contradicts AC3.
- `[minor]` Multi-line PEM env handling test gap.

### plan-23-05 — Input Validation

**Verdict**: minor edits

- `[major]` [plan-23-05:114-116](epics/23-security/plan-23-05-input-validation.md) `strings.Contains(p, "..")` rejects valid filenames containing `..`. Split on `os.PathSeparator` and check components.
- `[minor]` `syscall.RawConn` Linux/Darwin-specific.
- `[minor]` `validator/v10` integration not shown.
- `[minor]` AC5 (probe output size-bound) not addressed.
- `[minor]` Go vs Python implementations differ.

### plan-23-06 — Rate Limiting

**Verdict**: ~~blocking~~ **RESOLVED**

- `[blocking]` Auth-endpoint rate limits redefined with different numbers and limiter than plan-10-12. See §2 cross-epic table.
- `[blocking]` `golang.org/x/time/rate` (plan-10-12) vs "rolled our own" (plan-23-06) — two limiters at routing time.
- `[major]` LRU eviction strategy differs from plan-10-12's janitor.
- `[major]` Single-host vs distributed (Epic 19): per-replica buckets give 2× headroom — known limitation should be flagged as TODO.
- `[minor]` `min(b.capacity, …)` Go 1.21+ builtin.
- `[minor]` Resource-existence timing leak before constant-time compare.

### plan-23-07 — Supply Chain

**Verdict**: minor edits

- `[minor]` `cyclonedx-go` vs `cyclonedx-gomod` tool naming.
- `[minor]` Suppression scope check positional-arg parsing.
- `[minor]` Renovate `automerge: true` for security PRs — document CI smoke gate.
- `[minor]` SBOM signed via minisign vs plan-22-02 cosign — cross-link.
- `[minor]` Air-gapped build supply-chain bypass policy.

### plan-23-08 — Coordinated Disclosure

**Verdict**: ship-as-is

- `[minor]` `security/age-pubkey.txt` file referenced but not in §2.1.
- `[minor]` CVE assignment via GHSA — strengthen wording.
- `[minor]` `advisories.schema.json` referenced but not in files list.
- `[minor]` Patch-release flow consistent with plan-22-05.

### Cross-plan issues for Epic 23

- See §2 cross-epic table for all major Epic 10 ↔ Epic 23 conflicts.
- **Secret registry incomplete** in plan-23-04 — must enumerate `MAKTABA_KEY_ENCRYPTION_KEY`, `MAKTABA_INTERNAL_BOOTSTRAP_TOKEN`, `MAKTABA_TRUSTED_PROXIES`.
- **JWT private-key storage model contradictory** — env (arch §11.5, plan-23-04, plan-10-14) vs DB-encrypted (plan-23-01).
- **`device-pat` source** still undefined (carried over from PLAN_REVIEW_07_13).

---

## Epic 24 — Data Integrity

### plan-24-01 — Atomic Writes for Sidecar Artifacts

**Verdict**: minor edits

- `[minor]` HLS segments not addressed; either include or scope-out via Epic 8 §4.8 reference.
- `[minor]` `EXDEV` cross-FS fallback described in §4 but not in helper signature.
- `[minor]` `_probe` always returns `True/True`; `supports_atomic_rename` result unused.
- `[minor]` `.maktaba/` glob `.maktaba/*/*.tmp.*` may miss flat layouts; use `**`.

### plan-24-02 — Idempotent and Resumable Jobs

**Verdict**: minor edits

- `[minor]` `_relevant_keys` only enumerates `transcribe`/`embed`; other stages fall through to over-restrictive default. Provide explicit table for all stages.
- `[minor]` State casing uppercase. See cross-cutting §1.2.
- `[minor]` Architecture defines `paused/resuming` states; plan skips `resuming` transition.
- `[minor]` `ON CONFLICT (job_id, segment_idx)` vs plan-24-03's `(video_id, segment_idx)`. See cross-plan rollup.

### plan-24-03 — Database Constraints

**Verdict**: ~~major fixes~~ **RESOLVED**

- `[blocking]` Schema canonicalization unowned. See cross-cutting §1.1.
- `[blocking]` `videos.state` CHECK enumerates uppercase that rejects architecture's `'discovered'` default. See cross-cutting §1.2.
- `[major]` `segments(video_id, segment_idx)` UNIQUE — table is `transcript_segments`. See cross-cutting §1.1.
- `[major]` `processing_jobs.state IN ('QUEUED', ..., 'DONE')` — wrong casing and omits `paused`/`resuming`/`cancelled`. See cross-cutting §1.2.
- `[minor]` `videos_path_active_unique` partial index `(library_id, path)` vs inventory `(library_id, video_id)`.
- `[minor]` `TestEnumStringsMatchArchitecture` parses prose; pin enum to single source-of-truth file.

### plan-24-04 — Concurrency and Locking

**Verdict**: minor edits

- `[minor]` Claim SQL uppercase state names. See cross-cutting §1.2.
- `[minor]` Multi-GPU concurrency test missing.
- `[minor]` Advisory-lock release-on-crash test should pin wall-clock budget.
- `[minor]` Watch-progress debounce coalesce — cross-link audit row emission with plan-21-06.
- `[minor]` Chroma flock startup-check error code shared with Story 19.4 TC4.

### plan-24-05 — Backup and Restore

**Verdict**: minor edits

- `[minor]` Sidecar `.maktaba/` not backed up — confirm rebuilt from DB via 24.2's `--rebuild-sidecars`.
- `[minor]` Restore order implicit; pin via `restore --then-migrate`.
- `[minor]` Cron parser timezone.
- `[minor]` `pg_restore --list` doesn't detect tail truncation; add CRC.
- `[minor]` Cron daemon shared with 24.7 — document one daemon, not two.

### plan-24-06 — Disaster Recovery

**Verdict**: minor edits

- `[minor]` RPO/RTO 30 min / 24 h match story.
- `[minor]` Scenario 2 RTO drill not exercised; add small fixture.
- `[minor]` `state='CORRUPTED'` will fail CHECK from 24-03 unless casing reconciled. See cross-cutting §1.2.
- `[minor]` `dropdb`/`createdb` runner setup.
- `[minor]` `category='data', action='video.corrupted'` audit row vs Story 21.6 enum.
- `[minor]` `--allow-partial` flag not in 24-05 restore code.

### plan-24-07 — Integrity Verification

**Verdict**: minor edits

- `[minor]` `compute_content_hash` import from 24-08 not qualified.
- `[minor]` Sample default 1% for 50 k-video library.
- `[minor]` `parity_check.py` references `segments_fts`; canonical is `transcripts_fts`.
- `[minor]` `audit_log` insert before owner Epic ships; cross-check ordering.
- `[minor]` `TestLibraryDeletedMidRun` test description vs SQL behavior.

### plan-24-08 — Identity Stability

**Verdict**: minor edits

- `[minor]` Identity formula matches arch §1.5 + §3.1.
- `[minor]` `<8 MiB → whole file` branch doesn't append size suffix; story EC2 confirms.
- `[minor]` Partial-copy edge case not pinned.
- `[minor]` `superseded_by` FK not in 24-03 inventory.
- `[minor]` `videos.mtime` integer-ns vs `TIMESTAMPTZ` drift. See cross-cutting §1.1.
- `[minor]` Sparse-file zero-read POSIX-only.

### plan-24-09 — Forward and Backward Compatibility

**Verdict**: minor edits

- `[minor]` Schema discipline delegated to 22.4 — cross-link by name.
- `[minor]` Artifact `schema_version` covers only three artifacts; SRT/VTT excluded should be explicit.
- `[minor]` gRPC field-number stability not addressed.
- `[minor]` `MajorPrefix()` empty-string sanity check.
- `[minor]` WS major-mismatch close-code 4001 — pin in client SDK constant.
- `[minor]` `var/maktaba/forensic/` directory ownership unspecified.

### Cross-plan issues for Epic 24

- See cross-cutting §1.1 (schema drift), §1.2 (state casing), §1.4 (audit_log).
- **`segments` vs `transcript_segments` table-name collision.**
- **Unique-constraint drift between 24-02 and 24-03**: `(job_id, segment_idx)` vs `(video_id, segment_idx)`. Recommend `(video_id, segment_idx)`.
- **Sidecar artifact filename inventory not consistent across 24-01, 24-02, 24-07.**
- **`audit_log` referenced by 24-04/05/06/07 but owned by Story 21.6.** Call out dependency in each plan's §0.
- **Cron daemon ownership.** 24-05 robfig/cron/v3 vs 24-07 pipeline scheduler.
- **`content_hash` recomputation function** should be a single import (`domain.identity.compute` from 24-08).
- **`videos.mtime` type drift.** Integer-ns short-circuit will never match `TIMESTAMPTZ`.
- **Forward-compat fixtures** non-existent at v1.0 cut — state explicitly so reviewers don't expect them on day one.

---

## 7. Recommended remediation order [APPLIED]

The five-PR remediation plan below has been applied as a single
multi-plan changeset (parallel agents per epic):

1. **Schema canonicalization** — single migration owned by plan-24-03 that
   (a) renames `videos.size`→`size_bytes`, `videos.poster_url`→`poster_path`,
   `media_info.duration_sec`→`videos.duration_sec` (already arch);
   (b) renames `segments`→`transcript_segments` consistently;
   (c) lowercases all state CHECK constraints and adds extension states
   (`paused`, `resuming`, `cancelled`, `corrupted`, `missing`, `superseded`,
   `ready_no_audio`); (d) introduces `videos.deleted_at`, `transcripts.superseded_at`,
   `transcripts.detected_language` / `language_confidence`, `videos.content_type`,
   `videos.superseded_by`; (e) creates `audit_log` with the plan-21-06 schema
   plus `category='device'`. Update PLAN_REVIEW_07_13's drift catalog to
   note resolution.

2. **Audit_log unification** — mark plan-09-17 superseded; ensure plans 12-10,
   19-08, 21-05/07, 23-06, 24-04/05/06/07 all use plan-21-06's column names.

3. **Epic 23 ↔ Epic 10 reconciliation** — pick canonical owners per §2
   table (recommendation: Epic 23 wins for authz/rate-limits, Epic 10 wins
   for HSTS placement and JWT-key-storage). Reopen plan-10-12, plan-10-13,
   plan-10-15 as deferral PRs.

4. **`processing_jobs` claim canonicalization** — rewrite plan-19-04 to use
   canonical column names and SQL shapes from architecture §7.3.

5. **Performance budgets schema extension** — extend plan-18-01's
   `Budget` struct with `ci_pr` flag, add `throughputs:` and `envelopes:`
   YAML sections; rewrite plan-18-04, plan-18-05, plan-20-07 to read from
   the unified file.

After these five PRs, the remaining minor edits are ~110 small per-plan
fixes, each scoped to a single file.
